//go:build integration

package integration

import (
	"context"
	"errors"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/eventstore"
	"flash-sale/internal/platform/fault"
	"flash-sale/internal/platform/messaging"
	"flash-sale/tests/us1"
	"github.com/IBM/sarama"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
	"time"
)

func TestUS1NullablePoisonAndRetryPool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f, e := us1.New(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	cfg := f.Pool.Config()
	cfg.MaxConns = 1
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	retry := messaging.RetryStore{Pool: pool, Clock: f.Clock}
	for i, v := range []struct{ key, value []byte }{{nil, nil}, {[]byte{}, []byte{}}} {
		m := &sarama.ConsumerMessage{Topic: "sale.config.v1", Partition: 0, Offset: int64(i), Key: v.key, Value: v.value, Timestamp: f.Clock.Now()}
		if e = retry.Schedule(ctx, "probe", "", "INVALID_EVENT", m, true, false); e != nil {
			t.Fatal(e)
		}
		var keyNull, valueNull bool
		if e = pool.QueryRow(ctx, "SELECT record_key IS NULL,payload IS NULL FROM platform_failed_work WHERE original_offset=$1", i).Scan(&keyNull, &valueNull); e != nil {
			t.Fatal(e)
		}
		if keyNull != (v.key == nil) || valueNull != (v.value == nil) {
			t.Fatal("null/empty distinction lost")
		}
	}
	m := &sarama.ConsumerMessage{Topic: "sale.ready.v1", Offset: 2, Key: []byte("key"), Value: []byte("payload")}
	id := clock.UUID()
	if e = retry.Schedule(ctx, "retry-pool", id, "TRANSIENT", m, false, false); e != nil {
		t.Fatal(e)
	}
	f.Clock.Advance(time.Second)
	store := eventstore.Store{Pool: pool}
	ran, e := retry.RunOne(ctx, "retry-pool", func(ctx context.Context, work messaging.FailedWork) error {
		_, e := store.Consume(ctx, "retry-test", id, work.Payload, func(tx pgx.Tx) error { _, e := tx.Exec(ctx, "SELECT 1"); return e })
		return e
	}, nil)
	if e != nil || !ran {
		t.Fatalf("one-connection retry starved: %v %v", ran, e)
	}
}
func TestUS1DeliveryFailureIsVisible(t *testing.T) {
	ctx := context.Background()
	f, e := us1.New(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if _, e = f.Store.Put(ctx, f.Write(clock.UUID(), 0), clock.UUID(), clock.UUID(), true); e != nil {
		t.Fatal(e)
	}
	sentinel := errors.New("broker unavailable")
	sent, e := f.Store.Outbox().DispatchOne(ctx, f.Clock.Now().Add(time.Second), func(context.Context, string, string, []byte) error { return sentinel })
	if sent || !errors.Is(e, sentinel) {
		t.Fatalf("hidden delivery failure: %v %v", sent, e)
	}
	var attempts int
	var published bool
	if e = f.Pool.QueryRow(ctx, "SELECT attempts,published_at IS NOT NULL FROM platform_delivery ORDER BY attempts DESC LIMIT 1").Scan(&attempts, &published); e != nil {
		t.Fatal(e)
	}
	if attempts != 1 || published {
		t.Fatal("failure schedule rolled back or acknowledged")
	}
}

func TestUS1RetryClaimRecoveryAndExclusion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f, e := us1.New(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	retry := messaging.RetryStore{Pool: f.Pool, Clock: f.Clock}
	m := &sarama.ConsumerMessage{Topic: "sale.ready.v1", Key: []byte("key"), Value: []byte("payload")}
	id := clock.UUID()
	if e = retry.Schedule(ctx, "claims", id, "RETRY", m, false, false); e != nil {
		t.Fatal(e)
	}
	f.Clock.Advance(time.Second)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, e := retry.RunOne(ctx, "claims", func(ctx context.Context, _ messaging.FailedWork) error {
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}, nil)
		done <- e
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	ran, e := retry.RunOne(ctx, "claims", func(context.Context, messaging.FailedWork) error { t.Error("concurrent claim ran"); return nil }, nil)
	close(release)
	if e != nil || ran {
		t.Fatal(ran, e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	// Business commits, then the worker disappears before its claim result persists.
	m.Offset = 1
	if e = retry.Schedule(ctx, "recovery", id, "RETRY", m, false, false); e != nil {
		t.Fatal(e)
	}
	f.Clock.Advance(time.Second)
	point := fault.New()
	point.Set("after_retry_handle", errors.New("worker interrupted"), nil)
	retry.Fault = point
	business := eventstore.Store{Pool: f.Pool}
	effects := 0
	apply := func(ctx context.Context, w messaging.FailedWork) error {
		_, e := business.Consume(ctx, "claim-effect", id, w.Payload, func(pgx.Tx) error { effects++; return nil })
		return e
	}
	if _, e = retry.RunOne(ctx, "recovery", apply, nil); e == nil {
		t.Fatal("interruption absent")
	}
	retry.Fault = nil
	f.Clock.Advance(31 * time.Second)
	if ran, e = retry.RunOne(ctx, "recovery", apply, nil); e != nil || !ran || effects != 1 {
		t.Fatal("committed effect repeated", ran, e, effects)
	}
}

func TestUS1RetrySaturatedPoolAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f, e := us1.New(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	cfg := f.Pool.Config()
	cfg.MaxConns = 2
	p, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	retry := messaging.RetryStore{Pool: p, Clock: f.Clock}
	business := eventstore.Store{Pool: p}
	for i := 0; i < 2; i++ {
		m := &sarama.ConsumerMessage{Topic: "sale.ready.v1", Offset: int64(i), Key: []byte("k"), Value: []byte("data")}
		if e = retry.Schedule(ctx, "saturated", clock.UUID(), "RETRY", m, false, false); e != nil {
			t.Fatal(e)
		}
	}
	f.Clock.Advance(time.Second)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, e := retry.RunOne(ctx, "saturated", func(ctx context.Context, w messaging.FailedWork) error {
				_, e := business.Consume(ctx, "saturated-effects", w.EventID, w.Payload, func(tx pgx.Tx) error {
					entered <- struct{}{}
					select {
					case <-release:
						_, e := tx.Exec(ctx, "SELECT 1")
						return e
					case <-ctx.Done():
						return ctx.Err()
					}
				})
				return e
			}, nil)
			done <- e
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			close(release)
			t.Fatal("pool starvation", ctx.Err())
		}
	}
	close(release)
	for range 2 {
		if e = <-done; e != nil {
			t.Fatal(e)
		}
	}
	m := &sarama.ConsumerMessage{Topic: "sale.ready.v1", Offset: 2, Key: []byte("k"), Value: []byte("data")}
	id := clock.UUID()
	if e = retry.Schedule(ctx, "cancelled", id, "RETRY", m, false, false); e != nil {
		t.Fatal(e)
	}
	f.Clock.Advance(time.Second)
	cancelled, stop := context.WithCancel(ctx)
	effects := 0
	apply := func(ctx context.Context, w messaging.FailedWork) error {
		_, e := business.Consume(ctx, "cancelled-effect", id, w.Payload, func(pgx.Tx) error { effects++; return nil })
		return e
	}
	if _, e = retry.RunOne(cancelled, "cancelled", func(ctx context.Context, w messaging.FailedWork) error { e := apply(ctx, w); stop(); return e }, nil); !errors.Is(e, context.Canceled) {
		t.Fatal("cancellation not retained", e)
	}
	f.Clock.Advance(31 * time.Second)
	if ran, e := retry.RunOne(ctx, "cancelled", apply, nil); e != nil || !ran || effects != 1 {
		t.Fatal("cancel recovery repeated business effect", ran, e, effects)
	}
}
