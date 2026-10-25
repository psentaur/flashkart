package sale

import (
	"context"
	"encoding/json"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/eventstore"
	"flash-sale/internal/platform/value"
	"github.com/jackc/pgx/v5"
	"time"
)

func (s Store) emit(ctx context.Context, tx pgx.Tx, kind string, v Sale, correlation string) error {
	var data any = v
	if kind == "SaleCancelled" {
		data = events.SaleCancelled{SaleID: v.SaleID, ProductID: v.ProductID, Version: v.Version}
	}
	b, e := json.Marshal(data)
	if e != nil {
		return e
	}
	if value.UUID(correlation) != nil {
		correlation = clock.UUID()
	}
	return eventstore.Enqueue(ctx, tx, events.Envelope{EventID: clock.UUID(), SchemaVersion: 1, Type: kind, OccurredAt: s.now().Format(time.RFC3339Nano), Producer: "flash-sale", AggregateID: v.SaleID, AggregateVersion: v.Version, CorrelationID: correlation, Data: b})
}
func (s Store) Put(ctx context.Context, w Write, operator, correlation string, create bool) (Sale, error) {
	v, e := normalize(w)
	if e != nil {
		return v, e
	}
	if value.UUID(operator) != nil {
		return v, reject("INVALID_ARGUMENT", "Invalid operator identity.")
	}
	if (create && w.ExpectedVersion != 0) || (!create && w.ExpectedVersion < 1) {
		return v, reject("VERSION_CONFLICT", "Expected version does not match operation.")
	}
	var out Sale
	e = s.transaction(ctx, func(tx pgx.Tx) error {
		// Serializes matching creates as well as replacements of a missing ID. The
		// exclusion constraint remains the arbiter for different sale identities.
		if _, e := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", v.SaleID); e != nil {
			return e
		}
		// GiST can detect a deadlock when simultaneous exclusion checks inspect
		// each other's uncommitted rows. Serialize this product before row writes;
		// the database exclusion remains the final invariant, including other writers.
		if _, e := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,1))", v.ProductID); e != nil {
			return e
		}
		prior, err := load(ctx, tx, v.SaleID, true)
		if create {
			d, e := Digest(v)
			if e != nil {
				return e
			}
			if err == nil {
				if prior.creation != d {
					return reject("VERSION_CONFLICT", "Sale ID was created with different terms.")
				}
				out, e = s.view(ctx, tx, prior)
				return e
			}
			if Code(err) != "NOT_FOUND" {
				return err
			}
			start, _ := time.Parse(time.RFC3339Nano, v.StartsAt)
			if !s.now().Before(start) {
				return reject("VERSION_CONFLICT", "Sale must be configured before its start.")
			}
			b, _ := json.Marshal(v)
			_, e = tx.Exec(ctx, `INSERT INTO sales(sale_id,product_id,version,document,creation_digest,original_starts_at,starts_at,ends_at,allocated_units,unit_price_minor,per_shopper_unit_cap,configured_by,configured_at) VALUES($1,$2,$3,$4,$5,$6,$6,$7,$8,$9,$10,$11,$12)`, v.SaleID, v.ProductID, v.Version, b, d, start, v.EndsAt, v.AllocatedUnits, v.UnitPriceMinor, v.PerShopperUnitCap, operator, s.now())
			if e != nil {
				return e
			}
		} else {
			if err != nil {
				return err
			}
			start, _ := time.Parse(time.RFC3339Nano, prior.sale.StartsAt)
			if prior.sale.Version != w.ExpectedVersion || prior.cancelled != nil || prior.frozen != nil || !s.now().Before(prior.original) || !s.now().Before(start) {
				return reject("VERSION_CONFLICT", "Sale version changed or configuration is frozen.")
			}
			if prior.sale.ProductID != v.ProductID {
				return reject("INVALID_ARGUMENT", "A sale's product cannot change.")
			}
			nextStart, _ := time.Parse(time.RFC3339Nano, v.StartsAt)
			if !s.now().Before(nextStart) {
				return reject("VERSION_CONFLICT", "Replacement must precede its start.")
			}
			b, _ := json.Marshal(v)
			_, e = tx.Exec(ctx, `UPDATE sales SET version=$2,document=$3,starts_at=$4,ends_at=$5,allocated_units=$6,unit_price_minor=$7,per_shopper_unit_cap=$8,configured_by=$9,configured_at=$10,activated_at=NULL WHERE sale_id=$1`, v.SaleID, v.Version, b, v.StartsAt, v.EndsAt, v.AllocatedUnits, v.UnitPriceMinor, v.PerShopperUnitCap, operator, s.now())
			if e != nil {
				return e
			}
		}
		if e = s.emit(ctx, tx, "SaleConfigured", v, correlation); e != nil {
			return e
		}
		out = v
		return nil
	})
	return out, e
}
func (s Store) Cancel(ctx context.Context, id string, expected int64, correlation string) (Sale, error) {
	if value.UUID(id) != nil || expected < 1 {
		return Sale{}, reject("INVALID_ARGUMENT", "Valid sale ID and expected version required.")
	}
	var out Sale
	e := s.transaction(ctx, func(tx pgx.Tx) error {
		r, e := load(ctx, tx, id, true)
		if e != nil {
			return e
		}
		start, _ := time.Parse(time.RFC3339Nano, r.sale.StartsAt)
		if r.sale.Version != expected || r.cancelled != nil || r.frozen != nil || !s.now().Before(r.original) || !s.now().Before(start) {
			return reject("VERSION_CONFLICT", "Sale version changed or cancellation is frozen.")
		}
		r.sale.Version++
		r.sale.Status = "CANCELLED"
		r.sale.ReadyParticipants = []string{}
		now := s.now()
		r.cancelled = &now
		b, _ := json.Marshal(r.sale)
		if _, e = tx.Exec(ctx, "UPDATE sales SET version=$2,document=$3,cancelled_at=$4 WHERE sale_id=$1", id, r.sale.Version, b, now); e != nil {
			return e
		}
		if e = s.emit(ctx, tx, "SaleCancelled", r.sale, correlation); e != nil {
			return e
		}
		out = r.sale
		return nil
	})
	return out, e
}
