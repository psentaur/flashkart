package us2

import (
	"context"
	"encoding/json"
	"errors"
	"flash-sale/contracts/gen/inventory/v1"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/fault"
	"flash-sale/internal/platform/httpapi"
	"flash-sale/internal/platform/value"
	order "flash-sale/services/order"
	"fmt"
	"sync"
	"time"
)

func Purchase(ctx context.Context) error {
	f, err := New(ctx)
	if err != nil {
		return err
	}
	defer f.Close()
	shopper, key := clock.UUID(), "concurrent-key"
	id, err := f.Script(ctx, shopper, key, f.Purchase, inventoryv1.ResultCode_APPLIED)
	if err != nil {
		return err
	}
	gate := make(chan struct{})
	entered := make(chan struct{})
	f.Peer.Fault = hook(func(ctx context.Context, name string) error {
		if name != "reserve" {
			return nil
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-gate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	// Start one worker, then explicitly observe the protected Reserve before racing 49 copies.
	entered = make(chan struct{}, 1)
	results := make(chan error, 50)
	go func() {
		r, e := f.Submit(ctx, shopper, key, f.Purchase)
		if e == nil {
			e = expect(r, 202, "")
		}
		results <- e
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		return ctx.Err()
	}
	var copies sync.WaitGroup
	for range 49 {
		copies.Add(1)
		go func() {
			defer copies.Done()
			r, e := f.Submit(ctx, shopper, key, f.Purchase)
			if e == nil && r.Status != 202 {
				e = expect(r, 409, "REQUEST_IN_PROGRESS")
			}
			results <- e
		}()
	}
	copies.Wait()
	close(gate)
	for range 50 {
		if e := <-results; e != nil {
			return e
		}
	}
	if f.Peer.calls.Load() != 1 {
		return fmt.Errorf("same-key Reserve calls=%d", f.Peer.calls.Load())
	}
	for _, kind := range []string{"PaymentRequested", "OrderPending"} {
		if n, e := count(ctx, f.Pool, kind); e != nil || n != 1 {
			return fmt.Errorf("%s count %d: %v", kind, n, e)
		}
	}
	changed := f.Purchase
	changed.Quantity = 2
	r, err := f.Submit(ctx, shopper, key, changed)
	if err != nil {
		return err
	}
	if err = expect(r, 409, "IDEMPOTENCY_CONFLICT"); err != nil {
		return err
	}
	// Successful Confirm is already committed at Inventory; simulate the lost RPC response.
	f.Peer.Fault = fault.None{}
	if err = f.Peer.SetTerminal(id, inventoryv1.ReservationState_CONFIRMED); err != nil {
		return err
	}
	fi := fault.New()
	fi.Set("confirm", errors.New("lost successful Confirm response"), nil)
	f.Peer.Fault = fi
	var command events.Envelope
	var raw []byte
	if err = f.Pool.QueryRow(ctx, "SELECT envelope FROM platform_outbox WHERE convert_from(envelope,'UTF8')::jsonb->>'type'='PaymentRequested'").Scan(&raw); err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &command); err != nil {
		return err
	}
	var data map[string]any
	_ = json.Unmarshal(command.Data, &data)
	data["state"] = "SUCCEEDED"
	data["version"] = 1
	b, _ := json.Marshal(data)
	event := events.Envelope{EventID: clock.UUID(), SchemaVersion: 1, Type: "PaymentSucceeded", OccurredAt: f.Clock.Now().Format(time.RFC3339Nano), Producer: "payment", AggregateID: command.AggregateID, AggregateVersion: 1, CorrelationID: command.CorrelationID, Data: b}
	b, err = events.Encode(event)
	if err != nil {
		return err
	}
	topic, k := events.Destination(event)
	for range 2 {
		if err = f.Store.Consume(ctx, topic, k, b); err != nil {
			return err
		}
	}
	event.EventID = clock.UUID()
	b, _ = events.Encode(event)
	if err = f.Store.Consume(ctx, topic, k, b); err != nil {
		return err
	}
	r, err = f.Submit(ctx, shopper, key, f.Purchase)
	if err != nil {
		return err
	}
	if err = expect(r, 200, ""); err != nil {
		return err
	}
	if value.Validate("Order", r.Body) != nil {
		return errors.New("terminal replay violates Order contract")
	}
	var o order.Order
	_ = json.Unmarshal(r.Body, &o)
	if o.OrderID != id || o.State != "CONFIRMED" {
		return fmt.Errorf("terminal replay %+v", o)
	}
	// Dataset-lifetime keys retain their terminal result across 1000 explicit replays.
	for range 1000 {
		replay, e := f.Submit(ctx, shopper, key, f.Purchase)
		if e != nil {
			return e
		}
		if e = expect(replay, 200, ""); e != nil {
			return e
		}
	}
	if n, e := count(ctx, f.Pool, "OrderConfirmed"); e != nil || n != 1 {
		return fmt.Errorf("confirmation count %d: %v", n, e)
	}
	if _, err = f.Store.Get(ctx, id, clock.UUID()); err == nil {
		return errors.New("another shopper could read order")
	}
	return offerAndUnknown(ctx, f)
}
func offerAndUnknown(ctx context.Context, f *Fixture) error {
	shopper := clock.UUID()
	p := f.Purchase
	p.ExpectedUnitPriceMinor++
	r, err := f.Submit(ctx, shopper, "changed-offer", p)
	if err != nil {
		return err
	}
	if err = expect(r, 409, "OFFER_CHANGED"); err != nil {
		return err
	}
	var errorBody httpapi.Error
	_ = json.Unmarshal(r.Body, &errorBody)
	if value.Validate("Offer", errorBody.CurrentOffer) != nil {
		return errors.New("missing canonical current offer")
	}
	f.Offer.PerShopperUnitCap = 0
	_, err = f.Submit(ctx, shopper, "invalid-cap", f.Purchase)
	if err == nil {
		return errors.New("canonical cap zero accepted")
	}
	f.Offer.PerShopperUnitCap = 1
	id, err := f.Script(ctx, shopper, "orphan", f.Purchase, inventoryv1.ResultCode_APPLIED)
	if err != nil {
		return err
	}
	f.Peer.LoseResponse.Store(true)
	_, err = f.Submit(ctx, shopper, "orphan", f.Purchase)
	if err == nil {
		return errors.New("timeout fabricated acceptance")
	}
	f.Peer.Fault = fault.None{}
	f.Clock.Advance(6 * time.Second)
	// Simulate a new Order instance reusing the same owned database and proposed ID.
	restarted := f.Store
	r, err = restarted.Submit(ctx, shopper, "orphan", clock.UUID(), clock.UUID(), f.Purchase)
	if err != nil {
		return err
	}
	if err = expect(r, 202, ""); err != nil {
		return err
	}
	var o order.Order
	_ = json.Unmarshal(r.Body, &o)
	if o.OrderID != id {
		return errors.New("orphan recovery changed proposed ID")
	}
	// Interrupt the acceptance transaction after a successful Reserve, then replay the hold.
	_, err = f.Script(ctx, shopper, "commit-crash", f.Purchase, inventoryv1.ResultCode_APPLIED)
	if err != nil {
		return err
	}
	commits := 0
	interrupted := f.Store
	interrupted.Fault = hook(func(context.Context, string) error {
		commits++
		if commits == 3 {
			return errors.New("acceptance interrupted")
		}
		return nil
	})
	_, err = interrupted.Submit(ctx, shopper, "commit-crash", clock.UUID(), clock.UUID(), f.Purchase)
	if err == nil {
		return errors.New("acceptance interruption ignored")
	}
	f.Clock.Advance(6 * time.Second)
	r, err = f.Submit(ctx, shopper, "commit-crash", f.Purchase)
	if err != nil {
		return err
	}
	if err = expect(r, 202, ""); err != nil {
		return err
	}

	return fencedWorker(ctx, f, shopper)
}
func ShopperCap(ctx context.Context) error {
	f, err := New(ctx)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, quantity := range []int64{1, 2} {
		f.Offer.PerShopperUnitCap = quantity
		shopper := clock.UUID()
		p := f.Purchase
		p.Quantity = quantity
		accepted, err := f.Script(ctx, shopper, "accepted", p, inventoryv1.ResultCode_APPLIED)
		if err != nil {
			return err
		}
		_, err = f.Script(ctx, shopper, "rejected", p, inventoryv1.ResultCode_PURCHASE_LIMIT_EXCEEDED)
		if err != nil {
			return err
		}
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, key := range []string{"accepted", "rejected"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s := f.Store
				r, e := s.Submit(ctx, shopper, key, clock.UUID(), clock.UUID(), p)
				if e == nil {
					if key == "accepted" {
						e = expect(r, 202, "")
					} else {
						e = expect(r, 409, "PURCHASE_LIMIT_EXCEEDED")
						if r.Header.Get("Retry-After") != "" {
							e = errors.New("cap Retry-After")
						}
					}
				}
				errs <- e
			}()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				return e
			}
		}
		if err = f.Peer.SetTerminal(accepted, inventoryv1.ReservationState_RELEASED); err != nil {
			return err
		}
		// A scripted allowance release never changes the rejected original key.
		r, e := f.Submit(ctx, shopper, "rejected", p)
		if e != nil {
			return e
		}
		if e = expect(r, 409, "PURCHASE_LIMIT_EXCEEDED"); e != nil {
			return e
		}
		_, err = f.Script(ctx, shopper, "later-new-key", p, inventoryv1.ResultCode_APPLIED)
		if err != nil {
			return err
		}
		r, e = f.Submit(ctx, shopper, "later-new-key", p)
		if e != nil {
			return e
		}
		if e = expect(r, 202, ""); e != nil {
			return e
		}
	}
	if n, e := count(ctx, f.Pool, "PaymentRequested"); e != nil || n != 4 {
		return fmt.Errorf("rejection created payment: %d %v", n, e)
	}
	// The 100-unit fixture uses 100 separate shopper identities at cap=1.
	f.Offer.PerShopperUnitCap = 1
	for range 100 {
		shopper := clock.UUID()
		if _, err = f.Script(ctx, shopper, "100-unit-fixture", f.Purchase, inventoryv1.ResultCode_APPLIED); err != nil {
			return err
		}
		r, e := f.Submit(ctx, shopper, "100-unit-fixture", f.Purchase)
		if e != nil {
			return e
		}
		if e = expect(r, 202, ""); e != nil {
			return e
		}
	}
	return nil
}

func fencedWorker(ctx context.Context, f *Fixture, shopper string) error {
	id, err := f.Script(ctx, shopper, "stale-worker", f.Purchase, inventoryv1.ResultCode_APPLIED)
	if err != nil {
		return err
	}
	entered, gate := make(chan struct{}, 1), make(chan struct{})
	f.Peer.Fault = hook(func(ctx context.Context, name string) error {
		if name != "reserve" {
			return nil
		}
		entered <- struct{}{}
		select {
		case <-gate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	result := make(chan error, 1)
	go func() { _, e := f.Submit(ctx, shopper, "stale-worker", f.Purchase); result <- e }()
	select {
	case <-entered:
	case <-ctx.Done():
		close(gate)
		return ctx.Err()
	}
	f.Clock.Advance(6 * time.Second)
	_, _, err = f.Store.Claim(ctx, shopper, "stale-worker", clock.UUID(), clock.UUID(), f.Purchase)
	close(gate)
	if err != nil {
		return err
	}
	if err = <-result; err == nil {
		return errors.New("stale worker committed acceptance")
	}
	var n int
	if err = f.Pool.QueryRow(ctx, "SELECT count(*) FROM orders WHERE order_id=$1", id).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return errors.New("fenced acceptance created an order")
	}
	f.Peer.Fault = fault.None{}
	return nil
}
