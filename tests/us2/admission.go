package us2

import (
	"context"
	"encoding/json"
	"errors"
	"flash-sale/gateway"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/httpapi"
	"flash-sale/internal/platform/telemetry"
	admission "flash-sale/services/admission"
	order "flash-sale/services/order"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"
)

func Admission(ctx context.Context) error {
	pool, cleanup, err := database(ctx, "admission")
	if err != nil {
		return err
	}
	defer cleanup()
	if err = admission.Migrate(ctx, pool); err != nil {
		return err
	}
	c := clock.NewManual(time.Now().UTC().Truncate(time.Second))
	q := admission.QuotaStore{Pool: pool, Clock: c}
	saleID, shopper := clock.UUID(), clock.UUID()
	var wg sync.WaitGroup
	results := make(chan bool, 40)
	errs := make(chan error, 40)
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			replica := q
			d, e := replica.Acquire(ctx, clock.UUID(), saleID, shopper, admission.Limits{ShopperPerSecond: 5, SalePerSecond: 100, MaxInFlight: 50})
			results <- d.Admitted
			errs <- e
		}()
	}
	wg.Wait()
	admitted := 0
	for range 40 {
		if <-results {
			admitted++
		}
		if e := <-errs; e != nil {
			return e
		}
	}
	if admitted != 5 {
		return fmt.Errorf("shared shopper quota admitted %d", admitted)
	}
	c.Advance(time.Second)
	saleID = clock.UUID()
	ids := []string{clock.UUID(), clock.UUID()}
	for _, id := range ids {
		d, e := q.Acquire(ctx, id, saleID, shopper, admission.Limits{ShopperPerSecond: 100, SalePerSecond: 100, MaxInFlight: 2})
		if e != nil || !d.Admitted {
			return fmt.Errorf("lease acquisition %v %v", d, e)
		}
	}
	d, err := q.Acquire(ctx, clock.UUID(), saleID, clock.UUID(), admission.Limits{ShopperPerSecond: 100, SalePerSecond: 100, MaxInFlight: 2})
	if err != nil || d.Reason != "OVERLOADED" {
		return fmt.Errorf("inflight limit %v %v", d, err)
	}
	if err = q.Release(ctx, ids[0]); err != nil {
		return err
	}
	if err = q.Release(ctx, ids[0]); err != nil {
		return err
	}
	d, err = q.Acquire(ctx, clock.UUID(), saleID, shopper, admission.Limits{ShopperPerSecond: 100, SalePerSecond: 100, MaxInFlight: 2})
	if err != nil || !d.Admitted {
		return fmt.Errorf("repeat-safe release %v %v", d, err)
	}
	c.Advance(2 * time.Second)
	d, err = q.Acquire(ctx, clock.UUID(), saleID, shopper, admission.Limits{ShopperPerSecond: 100, SalePerSecond: 100, MaxInFlight: 2})
	if err != nil || !d.Admitted {
		return fmt.Errorf("crashed leases not reclaimed %v %v", d, err)
	}
	// Distinct shoppers share the sale limit too.
	saleID = clock.UUID()
	for n := range 4 {
		d, e := q.Acquire(ctx, clock.UUID(), saleID, clock.UUID(), admission.Limits{ShopperPerSecond: 100, SalePerSecond: 3, MaxInFlight: 50})
		if e != nil || d.Admitted != (n < 3) {
			return fmt.Errorf("shared sale quota %d %v %v", n, d, e)
		}
	}
	if err = quotaAfterLock(ctx, pool, c); err != nil {
		return err
	}
	return eligibility(ctx, pool, c)
}
func eligibility(ctx context.Context, pool *pgxpool.Pool, c *clock.Manual) error {
	v, token, err := identity()
	if err != nil {
		return err
	}
	shopper := clock.UUID()
	config := events.SaleConfigured{SaleID: clock.UUID(), ProductID: clock.UUID(), Version: 1, UnitPriceMinor: 2500, Currency: "INR", AllocatedUnits: 100, PerShopperUnitCap: 1, StartsAt: c.Now().Add(-time.Hour).Format(time.RFC3339), EndsAt: c.Now().Add(time.Hour).Format(time.RFC3339), Status: "ACTIVE", ReadyParticipants: []string{}}
	config.AdmissionLimits.ShopperPerSecond = 5
	config.AdmissionLimits.SalePerSecond = 100
	config.AdmissionLimits.MaxInFlight = 50
	store := admission.Store{Pool: pool, Clock: c}
	data, _ := json.Marshal(config)
	for _, kind := range []string{"SaleConfigured", "SaleActivated"} {
		e := events.Envelope{EventID: clock.UUID(), SchemaVersion: 1, Type: kind, OccurredAt: c.Now().Format(time.RFC3339Nano), Producer: "flash-sale", AggregateID: config.SaleID, AggregateVersion: 1, CorrelationID: clock.UUID(), Data: data}
		raw, e2 := events.Encode(e)
		if e2 != nil {
			return e2
		}
		if e2 = store.Consume(ctx, "sale.config.v1", config.SaleID, raw); e2 != nil {
			return e2
		}
	}
	downstream := httptest.NewServer(httpapi.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteError(w, r, "PURCHASE_LIMIT_EXCEEDED", "Scripted cap result", nil)
	})))
	defer downstream.Close()
	metrics, edgeMetrics := telemetry.New(), telemetry.New()
	handler := trusted("gateway", admission.Handler(admission.Config{Store: store, Verifier: v, Metrics: metrics, OrderURL: downstream.URL}))
	admitted := httptest.NewServer(handler)
	defer admitted.Close()
	edge := gateway.Handler(gateway.Config{Verifier: v, Metrics: edgeMetrics, AdmissionURL: admitted.URL})
	p := order.Purchase{SaleID: config.SaleID, ProductID: config.ProductID, Quantity: 1, ExpectedOfferVersion: 1, ExpectedUnitPriceMinor: 2500, Currency: "INR"}
	headers := map[string]string{"Authorization": "Bearer " + token(shopper, "SHOPPER"), "Idempotency-Key": "eligible", "Content-Type": "application/json"}
	call := func(p order.Purchase) *httptest.ResponseRecorder {
		b, _ := json.Marshal(p)
		return response(edge, "POST", "/v1/purchases", b, headers)
	}
	for range 2 {
		w := call(p)
		if err = expect(decoded(w), 409, "PURCHASE_LIMIT_EXCEEDED"); err != nil {
			return err
		}
	}
	unknown := p
	unknown.SaleID = clock.UUID()
	if err = expect(decoded(call(unknown)), 404, "NOT_FOUND"); err != nil {
		return err
	}
	unknown = p
	unknown.ProductID = clock.UUID()
	if err = expect(decoded(call(unknown)), 404, "NOT_FOUND"); err != nil {
		return err
	}
	b, _ := json.Marshal(p)
	w := response(edge, "POST", "/v1/purchases", b, map[string]string{"Content-Type": "application/json", "Idempotency-Key": "key"})
	if w.Code != 401 {
		return errors.New("unauthorized ingress counted")
	}
	w = response(edge, "POST", "/v1/purchases", []byte(`{}`), headers)
	if w.Code != 400 {
		return errors.New("malformed ingress counted")
	}
	// Known inactive, cancelled and unready submissions remain eligible.
	for _, state := range []string{"UPCOMING", "CANCELLED", "UNAVAILABLE"} {
		switch state {
		case "UPCOMING":
			_, err = pool.Exec(ctx, "UPDATE applied_sales SET configuration=jsonb_set(configuration,'{starts_at}',to_jsonb($2::text)),activated_version=NULL WHERE sale_id=$1", config.SaleID, c.Now().Add(time.Hour).Format(time.RFC3339))
		case "CANCELLED":
			_, err = pool.Exec(ctx, "UPDATE applied_sales SET cancelled=true WHERE sale_id=$1", config.SaleID)
		case "UNAVAILABLE":
			_, err = pool.Exec(ctx, "UPDATE applied_sales SET cancelled=false,configuration=$2,activated_version=NULL WHERE sale_id=$1", config.SaleID, data)
		}
		if err != nil {
			return err
		}
		w = call(p)
		code := "SALE_INACTIVE"
		if state == "UNAVAILABLE" {
			code = "SALE_NOT_READY"
		}
		if err = expect(decoded(w), 409, code); err != nil {
			return err
		}
	}
	var attempts, eventsCount int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM purchase_attempts").Scan(&attempts); err != nil {
		return err
	}
	if attempts != 5 {
		return fmt.Errorf("eligible attempts=%d want 5", attempts)
	}
	eventsCount, err = count(ctx, pool, "AttemptRecorded")
	if err != nil || eventsCount != 5 {
		return fmt.Errorf("attempt events=%d %v", eventsCount, err)
	}
	var downstreamRejects int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM purchase_attempts WHERE decision='ADMITTED' AND reason='NONE' AND response_status=409 AND response_error_code='PURCHASE_LIMIT_EXCEEDED'").Scan(&downstreamRejects); err != nil || downstreamRejects != 2 {
		return fmt.Errorf("downstream classification count=%d %v", downstreamRejects, err)
	}
	ex, em := excluded(metrics), excluded(edgeMetrics)
	if ex["unknown_target"] != 2 || len(ex) != 1 || em["unauthorized"] != 1 || em["malformed"] != 1 {
		return fmt.Errorf("exclusions Admission=%v Gateway=%v", ex, em)
	}
	// Trusted internal redelivery reuses the saved response/event, unlike explicit public retries.
	ah := map[string]string{"X-Shopper-ID": shopper, "X-Role": "SHOPPER", "X-Attempt-ID": clock.UUID(), "Idempotency-Key": "internal", "Content-Type": "application/json"}
	for range 2 {
		w = response(handler, "POST", "/v1/purchases", b, ah)
		if w.Code != 409 {
			return fmt.Errorf("internal replay %s", w.Body.String())
		}
	}
	eventsCount, err = count(ctx, pool, "AttemptRecorded")
	if err != nil || eventsCount != 6 {
		return fmt.Errorf("internal redelivery duplicated attempt: %d %v", eventsCount, err)
	}
	// Restore activation, exhaust the configured shopper quota, and verify 429 headers.
	_, err = pool.Exec(ctx, "UPDATE applied_sales SET activated_version=version WHERE sale_id=$1", config.SaleID)
	if err != nil {
		return err
	}
	for range 4 {
		w = call(p)
	}
	if w.Code != 429 || w.Header().Get("Retry-After") != "1" {
		return fmt.Errorf("429 contract: %d %s", w.Code, w.Body.String())
	}
	// Closed owner SQL fails closed, with no fabricated successful admission.
	dead, err := pgxpool.NewWithConfig(ctx, pool.Config())
	if err != nil {
		return err
	}
	dead.Close()
	failed := trusted("gateway", admission.Handler(admission.Config{Store: admission.Store{Pool: dead}, Verifier: v, Metrics: telemetry.New()}))
	w = response(failed, "POST", "/v1/purchases", b, ah)
	if err = expect(decoded(w), 503, "DEPENDENCY_UNAVAILABLE"); err != nil {
		return err
	}
	return nil
}

type observedClock struct {
	*clock.Manual
	observed chan struct{}
	once     atomic.Bool
}

func (c *observedClock) Now() time.Time {
	now := c.Manual.Now()
	if c.once.CompareAndSwap(false, true) {
		c.observed <- struct{}{}
	}
	return now
}
func quotaAfterLock(ctx context.Context, pool *pgxpool.Pool, c *clock.Manual) error {
	sale, shopper := clock.UUID(), clock.UUID()
	q := admission.QuotaStore{Pool: pool, Clock: c}
	if _, err := q.Acquire(ctx, clock.UUID(), sale, shopper, admission.Limits{ShopperPerSecond: 100, SalePerSecond: 100, MaxInFlight: 50}); err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, "SELECT scope_id FROM admission_quotas WHERE scope='SALE' AND scope_id=$1 FOR UPDATE", sale); err != nil {
		return err
	}
	observed := &observedClock{Manual: c, observed: make(chan struct{}, 1)}
	q.Clock = observed
	done := make(chan error, 1)
	go func() {
		_, e := q.Acquire(ctx, clock.UUID(), sale, shopper, admission.Limits{ShopperPerSecond: 100, SalePerSecond: 100, MaxInFlight: 50})
		done <- e
	}()
	select {
	case <-observed.observed:
	case <-ctx.Done():
		return ctx.Err()
	}
	c.Advance(time.Second)
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if err = <-done; err != nil {
		return err
	}
	var bucket time.Time
	if err = pool.QueryRow(ctx, "SELECT bucket_start FROM admission_quotas WHERE scope='SALE' AND scope_id=$1", sale).Scan(&bucket); err != nil {
		return err
	}
	if !bucket.Equal(c.Now().Truncate(time.Second)) {
		return errors.New("quota decision used time sampled before acquiring the shared lock")
	}
	return nil
}
