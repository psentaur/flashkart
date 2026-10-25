// Package us1 runs isolated fixture checks against real owner SQL and Kafka.
// Scripted readiness is not acceptance evidence for the Inventory or Catalog owners.
package us1

import (
	"context"
	"encoding/json"
	"errors"
	"flash-sale/contracts"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/eventstore"
	"flash-sale/internal/platform/fault"
	"flash-sale/internal/platform/messaging"
	"flash-sale/internal/platform/value"
	admission "flash-sale/services/admission"
	flashsale "flash-sale/services/flash-sale"
	"fmt"
	"github.com/IBM/sarama"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"sync"
	"time"
)

type Fixture struct {
	Pool  *pgxpool.Pool
	Store flashsale.Store
	Clock *clock.Manual
	close func()
}

func database(ctx context.Context, owner string) (*pgxpool.Pool, func(), error) {
	prefix := strings.ToUpper(strings.ReplaceAll(owner, "-", "_"))
	url := os.Getenv(prefix + "_DATABASE_URL")
	if url == "" {
		return nil, nil, fmt.Errorf("%s_DATABASE_URL required; run with local configuration", prefix)
	}
	admin, e := pgxpool.New(ctx, url)
	if e != nil {
		return nil, nil, e
	}
	schema := "us1_" + strings.ReplaceAll(clock.UUID(), "-", "")
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		admin.Close()
		return nil, nil, e
	}
	cfg, e := pgxpool.ParseConfig(url)
	if e != nil {
		admin.Close()
		return nil, nil, e
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	cleanup := func() {
		if pool != nil {
			pool.Close()
		}
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(c, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	}
	if e != nil {
		cleanup()
		return nil, nil, e
	}
	if e = eventstore.Migrate(ctx, pool); e != nil {
		cleanup()
		return nil, nil, e
	}
	return pool, cleanup, nil
}
func New(ctx context.Context) (*Fixture, error) {
	p, close, e := database(ctx, "flash-sale")
	if e != nil {
		return nil, e
	}
	if e = flashsale.Migrate(ctx, p); e != nil {
		close()
		return nil, e
	}
	c := clock.NewManual(time.Now().UTC().Truncate(time.Second))
	return &Fixture{p, flashsale.Store{Pool: p, Clock: c}, c, close}, nil
}
func (f *Fixture) Close() { f.close() }
func (f *Fixture) Write(product string, offset time.Duration) flashsale.Write {
	return flashsale.Write{SaleID: clock.UUID(), ProductID: product, UnitPriceMinor: 2500, Currency: "INR", AllocatedUnits: 100, StartsAt: f.Clock.Now().Add(time.Hour + offset).Format(time.RFC3339), EndsAt: f.Clock.Now().Add(2*time.Hour + offset).Format(time.RFC3339), AdmissionLimits: flashsale.Limits{ShopperPerSecond: 5, SalePerSecond: 100, MaxInFlight: 50}}
}
func configured(ctx context.Context, f *Fixture, w flashsale.Write) (flashsale.Sale, error) {
	return f.Store.Put(ctx, w, clock.UUID(), clock.UUID(), true)
}
func ready(s flashsale.Sale, p, digest, status string) events.Envelope {
	data, _ := json.Marshal(events.SaleReady{SaleID: s.SaleID, ProductID: s.ProductID, Version: s.Version, Participant: p, AllocationDigest: digest, Status: status})
	return events.Envelope{EventID: clock.UUID(), SchemaVersion: 1, Type: "SaleReady", OccurredAt: s.StartsAt, Producer: p, AggregateID: s.SaleID, AggregateVersion: s.Version, CorrelationID: clock.UUID(), Data: data}
}
func apply(ctx context.Context, s flashsale.Store, e events.Envelope) error {
	b, err := events.Encode(e)
	if err != nil {
		return err
	}
	topic, key := events.Destination(e)
	return s.Consume(ctx, topic, key, b)
}
func count(ctx context.Context, p *pgxpool.Pool, kind string) (int, error) {
	var n int
	e := p.QueryRow(ctx, "SELECT count(*) FROM platform_outbox WHERE convert_from(envelope,'UTF8')::jsonb->>'type'=$1", kind).Scan(&n)
	return n, e
}

func Readiness(ctx context.Context) error {
	f, e := New(ctx)
	if e != nil {
		return e
	}
	defer f.Close()
	w := f.Write(clock.UUID(), 0)
	s, e := configured(ctx, f, w)
	if e != nil {
		return e
	}
	if s.PerShopperUnitCap != 1 {
		return errors.New("omitted create cap did not default to one")
	}
	// Canonical source vector, not a digest computed by a second copy of the algorithm.
	if e = verifyDigestVector(); e != nil {
		return e
	}
	old := s
	w.ExpectedVersion = s.Version
	cap := int64(4)
	w.PerShopperUnitCap = &cap
	s, e = f.Store.Put(ctx, w, clock.UUID(), clock.UUID(), false)
	if e != nil {
		return e
	}
	d, e := flashsale.Digest(s)
	if e != nil {
		return e
	}
	oldDigest, _ := flashsale.Digest(old)
	for _, p := range []string{"catalog", "admission", "inventory"} {
		if e = apply(ctx, f.Store, ready(old, p, oldDigest, "READY")); e != nil {
			return e
		}
	}
	if e = apply(ctx, f.Store, ready(s, "catalog", d, "READY")); e != nil {
		return e
	}
	ack := ready(s, "admission", d, "READY")
	if e = apply(ctx, f.Store, ack); e != nil {
		return e
	}
	if e = apply(ctx, f.Store, ack); e != nil {
		return e
	}
	if e = apply(ctx, f.Store, ready(s, "inventory", oldDigest, "READY")); e != nil {
		return e
	}
	// Before start all-ready still cannot activate; at start a mismatch remains unavailable.
	if e = f.Store.Advance(ctx); e != nil {
		return e
	}
	f.Clock.Advance(time.Hour)
	if e = f.Store.Advance(ctx); e != nil {
		return e
	}
	s, e = f.Store.Get(ctx, s.SaleID)
	if e != nil {
		return e
	}
	if s.Status != "UNAVAILABLE" || len(s.ReadyParticipants) != 2 {
		return fmt.Errorf("stale/missing readiness activated: %+v", s)
	}
	if e = apply(ctx, f.Store, ready(s, "inventory", d, "READY")); e != nil {
		return e
	}
	crash := fault.New()
	crash.Set("before_commit", errors.New("activation interruption"), nil)
	interrupted := f.Store
	interrupted.Fault = crash
	if interrupted.Advance(ctx) == nil {
		return errors.New("activation interruption ignored")
	}
	if n, e := count(ctx, f.Pool, "SaleActivated"); e != nil || n != 0 {
		return fmt.Errorf("activation survived rollback: %d %v", n, e)
	}
	if e = f.Store.Advance(ctx); e != nil {
		return e
	}
	if e = f.Store.Advance(ctx); e != nil {
		return e
	}
	s, e = f.Store.Get(ctx, s.SaleID)
	if e != nil {
		return e
	}
	if s.Status != "ACTIVE" {
		return fmt.Errorf("not active: %s", s.Status)
	}
	if n, e := count(ctx, f.Pool, "SaleActivated"); e != nil || n != 1 {
		return fmt.Errorf("activation repeated: %d %v", n, e)
	}
	w.ExpectedVersion = s.Version
	w.StartsAt = f.Clock.Now().Add(time.Hour).Format(time.RFC3339)
	w.EndsAt = f.Clock.Now().Add(2 * time.Hour).Format(time.RFC3339)
	if _, e = f.Store.Put(ctx, w, clock.UUID(), clock.UUID(), false); flashsale.Code(e) != "VERSION_CONFLICT" {
		return fmt.Errorf("start freeze bypassed: %v", e)
	}
	f.Clock.Advance(time.Hour)
	if e = f.Store.Advance(ctx); e != nil {
		return e
	}
	s, e = f.Store.Get(ctx, s.SaleID)
	if e != nil || s.Status != "ENDED" {
		return fmt.Errorf("end reopened sale: %s %v", s.Status, e)
	}
	// Original start cannot be extended by repeated pre-start rescheduling.
	freeze := f.Write(clock.UUID(), 0)
	original, e := configured(ctx, f, freeze)
	if e != nil {
		return e
	}
	freeze.ExpectedVersion = 1
	freeze.StartsAt = f.Clock.Now().Add(3 * time.Hour).Format(time.RFC3339)
	freeze.EndsAt = f.Clock.Now().Add(4 * time.Hour).Format(time.RFC3339)
	if _, e = f.Store.Put(ctx, freeze, clock.UUID(), clock.UUID(), false); e != nil {
		return e
	}
	f.Clock.Advance(time.Hour)
	freeze.ExpectedVersion = 2
	if _, e = f.Store.Put(ctx, freeze, clock.UUID(), clock.UUID(), false); flashsale.Code(e) != "VERSION_CONFLICT" {
		return fmt.Errorf("original freeze bypassed for %s: %v", original.SaleID, e)
	}
	return admissionAndKafka(ctx, f, s)
}
func admissionAndKafka(ctx context.Context, f *Fixture, _ flashsale.Sale) error {
	p, close, e := database(ctx, "admission")
	if e != nil {
		return e
	}
	defer close()
	if e = admission.Migrate(ctx, p); e != nil {
		return e
	}
	a := admission.Store{Pool: p, Clock: f.Clock}
	w := f.Write(clock.UUID(), 0)
	s, e := configured(ctx, f, w)
	if e != nil {
		return e
	}
	b, _ := json.Marshal(s)
	configured := events.Envelope{EventID: clock.UUID(), SchemaVersion: 1, Type: "SaleConfigured", OccurredAt: f.Clock.Now().Format(time.RFC3339), Producer: "flash-sale", AggregateID: s.SaleID, AggregateVersion: s.Version, CorrelationID: clock.UUID(), Data: b}
	activated := configured
	activated.EventID = clock.UUID()
	activated.Type = "SaleActivated"
	s.Status = "ACTIVE"
	s.ReadyParticipants = []string{"catalog", "admission", "inventory"}
	activated.Data, _ = json.Marshal(s)
	deliver := func(e events.Envelope) error {
		raw, err := events.Encode(e)
		if err != nil {
			return err
		}
		topic, key := events.Destination(e)
		return a.Consume(ctx, topic, key, raw)
	}
	if e = deliver(activated); e != nil {
		return e
	}
	if _, e = a.Get(ctx, s.SaleID); e == nil {
		return errors.New("early activation created canonical configuration")
	}
	if e = deliver(configured); e != nil {
		return e
	}
	if e = deliver(configured); e != nil {
		return e
	}
	projection, e := a.Get(ctx, s.SaleID)
	if e != nil || projection.Status != "UPCOMING" || projection.PerShopperUnitCap != 1 {
		return fmt.Errorf("admission projection: %+v %v", projection, e)
	}
	f.Clock.Advance(time.Hour)
	projection, e = a.Get(ctx, s.SaleID)
	if e != nil || projection.Status != "ACTIVE" {
		return fmt.Errorf("buffered activation not reconciled: %+v %v", projection, e)
	}
	// A newer config must invalidate prior activation and emit a fresh readiness digest.
	var current map[string]any
	_ = json.Unmarshal(configured.Data, &current)
	current["version"] = 2
	current["per_shopper_unit_cap"] = 3
	newer := configured
	newer.EventID = clock.UUID()
	newer.AggregateVersion = 2
	newer.Data, _ = json.Marshal(current)
	if e = deliver(newer); e != nil {
		return e
	}
	if e = deliver(activated); e != nil {
		return e
	}
	projection, e = a.Get(ctx, s.SaleID)
	if e != nil || projection.Status != "UNAVAILABLE" || projection.PerShopperUnitCap != 3 {
		return fmt.Errorf("obsolete activation applied: %+v %v", projection, e)
	}
	delete(current, "per_shopper_unit_cap")
	invalid := newer
	invalid.EventID = clock.UUID()
	invalid.AggregateVersion = 3
	current["version"] = 3
	invalid.Data, _ = json.Marshal(current)
	raw, _ := json.Marshal(invalid)
	if a.Consume(ctx, "sale.config.v1", s.SaleID, raw) == nil {
		return errors.New("missing cap accepted")
	}
	// Actual Kafka persists an activation outbox record; a crash after publish replays its identity.
	brokers := strings.Split(os.Getenv("KAFKA_BROKERS"), ",")
	producer, e := messaging.Producer(brokers, "us1-fixture")
	if e != nil {
		return e
	}
	defer producer.Close()
	cfg := messaging.Config("us1-fixture")
	admin, e := sarama.NewClusterAdmin(brokers, cfg)
	if e != nil {
		return e
	}
	defer admin.Close()
	topic := "us1-" + clock.UUID()
	if e = admin.CreateTopic(topic, &sarama.TopicDetail{NumPartitions: 1, ReplicationFactor: 1}, false); e != nil {
		return e
	}
	defer admin.DeleteTopic(topic)
	consumer, e := sarama.NewConsumer(brokers, cfg)
	if e != nil {
		return e
	}
	defer consumer.Close()
	partition, e := consumer.ConsumePartition(topic, 0, sarama.OffsetOldest)
	if e != nil {
		return e
	}
	defer partition.Close()
	// Publish twice simulates the durable receipt crash; use the real handler's inbox for replay.
	raw, e = events.Encode(configured)
	if e != nil {
		return e
	}
	for range 2 {
		if _, _, e = producer.SendMessage(&sarama.ProducerMessage{Topic: topic, Key: sarama.StringEncoder(s.SaleID), Value: sarama.ByteEncoder(raw)}); e != nil {
			return e
		}
	}
	for range 2 {
		select {
		case m := <-partition.Messages():
			if e = a.Consume(ctx, "sale.config.v1", string(m.Key), m.Value); e != nil {
				return e
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if n, e := count(ctx, p, "SaleReady"); e != nil || n != 2 {
		return fmt.Errorf("readiness outbox duplicated: %d %v", n, e)
	}
	return nil
}
func Overlap(ctx context.Context) error {
	f, e := New(ctx)
	if e != nil {
		return e
	}
	defer f.Close()
	race := func(writes []flashsale.Write, creates []bool) error {
		before, e := count(ctx, f.Pool, "SaleConfigured")
		if e != nil {
			return e
		}
		gate := make(chan struct{})
		results := make(chan error, len(writes))
		var wg sync.WaitGroup
		for i, w := range writes {
			wg.Add(1)
			go func(w flashsale.Write, create bool) {
				defer wg.Done()
				<-gate
				replica := flashsale.Store{Pool: f.Pool, Clock: f.Clock}
				_, e := replica.Put(ctx, w, clock.UUID(), clock.UUID(), create)
				results <- e
			}(w, creates[i])
		}
		close(gate)
		wg.Wait()
		close(results)
		success := 0
		for e := range results {
			if e == nil {
				success++
			} else if flashsale.Code(e) != "SALE_WINDOW_CONFLICT" {
				return fmt.Errorf("unexpected race error: %w", e)
			}
		}
		after, e := count(ctx, f.Pool, "SaleConfigured")
		if e != nil {
			return e
		}
		if success != 1 || after-before != 1 {
			return fmt.Errorf("conflicting commits/outbox: %d/%d", success, after-before)
		}
		return nil
	}
	product := clock.UUID()
	first := f.Write(product, 0)
	second := first
	second.SaleID = clock.UUID()
	if e = race([]flashsale.Write{first, second}, []bool{true, true}); e != nil {
		return e
	}
	// A distant existing sale races a create to the same free interval.
	product = clock.UUID()
	existing := f.Write(product, 3*time.Hour)
	if _, e = configured(ctx, f, existing); e != nil {
		return e
	}
	existing.ExpectedVersion = 1
	existing.StartsAt = f.Clock.Now().Add(time.Hour).Format(time.RFC3339)
	existing.EndsAt = f.Clock.Now().Add(2 * time.Hour).Format(time.RFC3339)
	other := f.Write(product, 0)
	if e = race([]flashsale.Write{existing, other}, []bool{false, true}); e != nil {
		return e
	}
	product = clock.UUID()
	left := f.Write(product, 3*time.Hour)
	right := f.Write(product, 6*time.Hour)
	for _, w := range []flashsale.Write{left, right} {
		if _, e = configured(ctx, f, w); e != nil {
			return e
		}
	}
	for _, w := range []*flashsale.Write{&left, &right} {
		w.ExpectedVersion = 1
		w.StartsAt = f.Clock.Now().Add(time.Hour).Format(time.RFC3339)
		w.EndsAt = f.Clock.Now().Add(2 * time.Hour).Format(time.RFC3339)
	}
	if e = race([]flashsale.Write{left, right}, []bool{false, false}); e != nil {
		return e
	}
	// Adjacency, distinct products and cancelled-window reuse.
	product = clock.UUID()
	one := f.Write(product, 0)
	s, e := configured(ctx, f, one)
	if e != nil {
		return e
	}
	adjacent := f.Write(product, time.Hour)
	if _, e = configured(ctx, f, adjacent); e != nil {
		return e
	}
	if _, e = configured(ctx, f, f.Write(clock.UUID(), 0)); e != nil {
		return e
	}
	if _, e = f.Store.Cancel(ctx, s.SaleID, s.Version, clock.UUID()); e != nil {
		return e
	}
	one.SaleID = clock.UUID()
	if _, e = configured(ctx, f, one); e != nil {
		return e
	}
	// Same creation identity replays only the original normalized content.
	replay, e := configured(ctx, f, one)
	if e != nil || replay.Version != 1 {
		return fmt.Errorf("creation replay failed: %v", e)
	}
	one.UnitPriceMinor++
	if _, e = configured(ctx, f, one); flashsale.Code(e) != "VERSION_CONFLICT" {
		return fmt.Errorf("changed create replay: %v", e)
	}
	one.UnitPriceMinor--
	one.ExpectedVersion = 1
	cap := int64(9)
	one.PerShopperUnitCap = &cap
	s, e = f.Store.Put(ctx, one, clock.UUID(), clock.UUID(), false)
	if e != nil {
		return e
	}
	one.ExpectedVersion = s.Version
	one.PerShopperUnitCap = nil
	s, e = f.Store.Put(ctx, one, clock.UUID(), clock.UUID(), false)
	if e != nil || s.PerShopperUnitCap != 1 {
		return fmt.Errorf("omitted replacement cap: %+v %v", s, e)
	}
	return nil
}

func verifyDigestVector() error {
	raw, e := contracts.Assets.ReadFile("examples.json")
	if e != nil {
		return e
	}
	var examples struct {
		Events []events.Envelope `json:"events"`
	}
	if e = json.Unmarshal(raw, &examples); e != nil {
		return e
	}
	var d string
	for _, en := range examples.Events {
		if en.Type == "SaleConfigured" {
			d, e = valueDigest(en.Data)
			if e != nil {
				return e
			}
			break
		}
	}
	for _, en := range examples.Events {
		if en.Type == "SaleReady" {
			var ack events.SaleReady
			_ = json.Unmarshal(en.Data, &ack)
			if d != ack.AllocationDigest {
				return fmt.Errorf("shared digest vector: %s != %s", d, ack.AllocationDigest)
			}
		}
	}
	return nil
}
func valueDigest(raw []byte) (string, error) { return value.ConfigurationDigest(raw) }
