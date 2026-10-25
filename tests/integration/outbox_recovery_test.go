//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"flash-sale/contracts"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/eventstore"
	"flash-sale/internal/platform/fault"
	"github.com/jackc/pgx/v5"
	"testing"
	"time"
)

func TestInboxOutboxCrashRecovery(t *testing.T) {
	ctx := context.Background()
	p := database(t)
	if e := eventstore.Migrate(ctx, p); e != nil {
		t.Fatal(e)
	}
	_, e := p.Exec(ctx, "CREATE TABLE IF NOT EXISTS foundation_effects(id uuid PRIMARY KEY)")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := contracts.Assets.ReadFile("examples.json")
	var ex struct {
		Events []events.Envelope `json:"events"`
	}
	_ = json.Unmarshal(raw, &ex)
	en := ex.Events[0]
	en.EventID = clock.UUID()
	en.AggregateID = clock.UUID()
	var data events.SaleConfigured
	_ = json.Unmarshal(en.Data, &data)
	data.SaleID = en.AggregateID
	en.Data, _ = json.Marshal(data)
	raw, _ = events.Encode(en)
	consumer := "test-" + clock.UUID()
	f := fault.New()
	f.Set("before_commit", errors.New("process crash"), nil)
	s := eventstore.Store{Pool: p, Fault: f}
	apply := func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, "INSERT INTO foundation_effects VALUES($1)", en.EventID); e != nil {
			return e
		}
		return eventstore.Enqueue(ctx, tx, en)
	}
	if _, e = s.Consume(ctx, consumer, en.EventID, raw, apply); e == nil {
		t.Fatal("crash not injected")
	}
	var count int
	_ = p.QueryRow(ctx, "SELECT count(*) FROM foundation_effects WHERE id=$1", en.EventID).Scan(&count)
	if count != 0 {
		t.Fatal("business effect survived rollback")
	}
	s = eventstore.Store{Pool: p}
	if applied, e := s.Consume(ctx, consumer, en.EventID, raw, apply); e != nil || !applied {
		t.Fatal(applied, e)
	}
	if applied, e := s.Consume(ctx, consumer, en.EventID, raw, apply); e != nil || applied {
		t.Fatal("duplicate effect", e)
	}
	if _, e = s.Consume(ctx, consumer, en.EventID, []byte("changed"), apply); e == nil {
		t.Fatal("identity mutation")
	}
	published := map[string]int{}
	publish := func(_ context.Context, d, k string, b []byte) error {
		if _, e := events.Decode(d, k, b); e != nil {
			return e
		}
		published[d]++
		return nil
	}
	f = fault.New()
	f.Set("after_publish", errors.New("crash after broker acknowledgement"), nil)
	s.Fault = f
	if _, e = s.DispatchOne(ctx, time.Now().Add(time.Minute), publish); e == nil {
		t.Fatal("missing post-publish crash")
	}
	s.Fault = nil
	for range 2 {
		if sent, e := s.DispatchOne(ctx, time.Now().Add(time.Minute), publish); e != nil || !sent {
			t.Fatal(sent, e)
		}
	}
	if len(published) != 2 {
		t.Fatal("missing independent receipt", published)
	}
	_ = p.QueryRow(ctx, "SELECT count(*) FROM platform_delivery WHERE event_id=$1 AND published_at IS NOT NULL", en.EventID).Scan(&count)
	if count != 2 {
		t.Fatal("receipts", count)
	}
	if n, e := s.Prune(ctx, time.Now()); e != nil || n != 0 {
		t.Fatal("premature prune", n, e)
	}
	_, _ = p.Exec(ctx, "DELETE FROM platform_delivery WHERE event_id=$1", en.EventID)
	_, _ = p.Exec(ctx, "DELETE FROM platform_outbox WHERE event_id=$1", en.EventID)
	_, _ = p.Exec(ctx, "DELETE FROM platform_inbox WHERE consumer_name=$1", consumer)
	_, _ = p.Exec(ctx, "DELETE FROM foundation_effects WHERE id=$1", en.EventID)
}
