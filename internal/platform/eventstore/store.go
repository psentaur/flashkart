// Package eventstore keeps inbox, business changes, and outgoing intents in one owner transaction.
package eventstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/fault"
	"flash-sale/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

//go:embed schema.sql
var Schema string

//go:embed repair.sql
var repairSchema string

func Migrate(ctx context.Context, p *pgxpool.Pool) error {
	return postgres.Migrate(ctx, p, []postgres.Migration{{Name: "000_platform_events", SQL: Schema}, {Name: "000_platform_events_002_repair", SQL: repairSchema}})
}

type Store struct {
	Pool  *pgxpool.Pool
	Fault fault.Injector
}

func (s Store) hit(ctx context.Context, name string) error {
	if s.Fault == nil {
		return nil
	}
	return s.Fault.Hit(ctx, name)
}
func (s Store) Transaction(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if e = fn(tx); e != nil {
		return e
	}
	if e = s.hit(ctx, "before_commit"); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// Consume detects event-ID reuse with changed bytes instead of silently dropping it.
func (s Store) Consume(ctx context.Context, consumer, id string, raw []byte, fn func(pgx.Tx) error) (bool, error) {
	applied := false
	sum := sha256.Sum256(raw)
	e := s.Transaction(ctx, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, "INSERT INTO platform_inbox(consumer_name,event_id,payload_hash) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", consumer, id, sum[:])
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			var prior []byte
			if e = tx.QueryRow(ctx, "SELECT payload_hash FROM platform_inbox WHERE consumer_name=$1 AND event_id=$2", consumer, id).Scan(&prior); e != nil {
				return e
			}
			if !bytes.Equal(prior, sum[:]) {
				return &events.QuarantineError{Reason: "event identity reused with different payload"}
			}
			return nil
		}
		if e = fn(tx); e != nil {
			return e
		}
		applied = true
		return nil
	})
	return applied && e == nil, e
}

// Enqueue stores independent receipts. Domain records always keep an analytics copy.
func Enqueue(ctx context.Context, tx pgx.Tx, e events.Envelope) error {
	raw, err := events.Encode(e)
	if err != nil {
		return err
	}
	topic, key := events.Destination(e)
	var prior []byte
	err = tx.QueryRow(ctx, `INSERT INTO platform_outbox(event_id,aggregate_id,aggregate_version,envelope) VALUES($1,$2,$3,$4) ON CONFLICT(event_id) DO UPDATE SET event_id=EXCLUDED.event_id RETURNING envelope`, e.EventID, e.AggregateID, e.AggregateVersion, raw).Scan(&prior)
	if err != nil {
		return err
	}
	if !bytes.Equal(prior, raw) {
		return &events.QuarantineError{Reason: "outbox identity reused"}
	}
	dest := map[string]string{topic: key}
	if topic != "analytics.activity.v1" && e.Producer != "analytics" {
		_, ak := events.DestinationFor(e, "analytics.activity.v1")
		dest["analytics.activity.v1"] = ak
	}
	for d, k := range dest {
		if _, err = tx.Exec(ctx, "INSERT INTO platform_delivery(event_id,destination,record_key) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", e.EventID, d, k); err != nil {
			return err
		}
	}
	return nil
}

type Publish func(context.Context, string, string, []byte) error

// DispatchOne holds only a delivery row lock. A crash after publish repeats the same event.
func (s Store) DispatchOne(ctx context.Context, now time.Time, publish Publish) (bool, error) {
	sent := false
	var deliveryFailure error
	e := s.Transaction(ctx, func(tx pgx.Tx) error {
		var id, dest, key string
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT d.event_id::text,d.destination,d.record_key,o.envelope FROM platform_delivery d JOIN platform_outbox o USING(event_id) WHERE d.published_at IS NULL AND d.next_attempt_at<=$1 AND NOT EXISTS(SELECT 1 FROM platform_delivery prior JOIN platform_outbox po USING(event_id) WHERE prior.destination=d.destination AND prior.published_at IS NULL AND po.aggregate_id=o.aggregate_id AND po.aggregate_version<o.aggregate_version) ORDER BY o.created_at,d.destination FOR UPDATE OF d SKIP LOCKED LIMIT 1`, now).Scan(&id, &dest, &key, &raw)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		if err = publish(ctx, dest, key, raw); err != nil {
			deliveryFailure = &DeliveryError{Destination: dest, Cause: err}
			_, update := tx.Exec(ctx, "UPDATE platform_delivery SET attempts=attempts+1,next_attempt_at=$3 WHERE event_id=$1 AND destination=$2", id, dest, now.Add(time.Second))
			return update
		}
		if err = s.hit(ctx, "after_publish"); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "UPDATE platform_delivery SET attempts=attempts+1,published_at=$3 WHERE event_id=$1 AND destination=$2", id, dest, now)
		sent = err == nil
		return err
	})
	if e != nil {
		return false, e
	}
	return sent, deliveryFailure
}

// Prune never removes a missing destination receipt or records inside replay retention.
func (s Store) Prune(ctx context.Context, now time.Time) (int64, error) {
	tag, e := s.Pool.Exec(ctx, `DELETE FROM platform_outbox o WHERE created_at<$1 AND NOT EXISTS(SELECT 1 FROM platform_delivery d WHERE d.event_id=o.event_id AND d.published_at IS NULL) AND EXISTS(SELECT 1 FROM platform_delivery d WHERE d.event_id=o.event_id)`, now.Add(-8*24*time.Hour))
	return tag.RowsAffected(), e
}

// DeliveryError reports the publication failure after its retry receipt commits.
type DeliveryError struct {
	Destination string
	Cause       error
}

func (e *DeliveryError) Error() string { return "outbox delivery failed: " + e.Destination }
func (e *DeliveryError) Unwrap() error { return e.Cause }
