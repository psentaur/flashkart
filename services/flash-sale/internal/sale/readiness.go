package sale

import (
	"context"
	"encoding/json"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/eventstore"
	"github.com/jackc/pgx/v5"
	"time"
)

func (s Store) Handle(ctx context.Context, tx pgx.Tx, e events.Envelope) error {
	if e.Type != "SaleReady" {
		return &events.QuarantineError{Reason: "Flash Sale readiness accepts SaleReady only"}
	}
	var ack events.SaleReady
	if err := json.Unmarshal(e.Data, &ack); err != nil {
		return err
	}
	r, err := load(ctx, tx, ack.SaleID, true)
	if Code(err) == "NOT_FOUND" {
		return nil
	}
	if err != nil {
		return err
	}
	if r.sale.Version != ack.Version || r.sale.ProductID != ack.ProductID || r.cancelled != nil {
		return nil
	}
	// Keep mismatches inspectable; only matching digests enter the ready set.
	_, err = tx.Exec(ctx, `INSERT INTO sale_readiness(sale_id,version,participant,allocation_digest,status,error_code,applied_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(sale_id,version,participant) DO UPDATE SET allocation_digest=EXCLUDED.allocation_digest,status=EXCLUDED.status,error_code=EXCLUDED.error_code,applied_at=EXCLUDED.applied_at WHERE sale_readiness.applied_at<=EXCLUDED.applied_at`, ack.SaleID, ack.Version, ack.Participant, ack.AllocationDigest, ack.Status, ack.ErrorCode, e.OccurredAt)
	return err
}
func (s Store) Consume(ctx context.Context, topic, key string, raw []byte) error {
	e, err := events.Decode(topic, key, raw)
	if err != nil {
		return err
	}
	_, err = s.events().Consume(ctx, "flash-sale-readiness-v1", e.EventID, raw, func(tx pgx.Tx) error { return s.Handle(ctx, tx, e) })
	return err
}

// Advance is repeat safe across replicas. Row locks fence activation and the
// original-start freeze. A restart after end may freeze, but never activates.
func (s Store) Advance(ctx context.Context) error {
	return s.transaction(ctx, func(tx pgx.Tx) error {
		now := s.now()
		rows, e := tx.Query(ctx, `SELECT sale_id::text FROM sales WHERE cancelled_at IS NULL AND (frozen_at IS NULL OR (activated_at IS NULL AND ends_at>$1)) AND (original_starts_at<=$1 OR starts_at<=$1) ORDER BY sale_id`, now)
		if e != nil {
			return e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, id := range ids {
			r, e := load(ctx, tx, id, true)
			if e != nil {
				return e
			}
			if r.cancelled != nil {
				continue
			}
			now = s.now()
			start, _ := time.Parse(time.RFC3339Nano, r.sale.StartsAt)
			end, _ := time.Parse(time.RFC3339Nano, r.sale.EndsAt)
			if !now.Before(r.original) || !now.Before(start) {
				if _, e = tx.Exec(ctx, "UPDATE sales SET frozen_at=COALESCE(frozen_at,$2) WHERE sale_id=$1", id, now); e != nil {
					return e
				}
			}
			v, e := s.view(ctx, tx, r)
			if e != nil {
				return e
			}
			if r.activated != nil || now.Before(start) || !now.Before(end) || len(v.ReadyParticipants) != 3 {
				continue
			}
			v.Status = "ACTIVE"
			if _, e = tx.Exec(ctx, "UPDATE sales SET activated_at=$2 WHERE sale_id=$1", id, now); e != nil {
				return e
			}
			if e = s.emit(ctx, tx, "SaleActivated", v, ""); e != nil {
				return e
			}
		}
		return nil
	})
}

// Outbox exposes the shared receipt store to the lifecycle worker.
func (s Store) Outbox() eventstore.Store { return s.events() }
