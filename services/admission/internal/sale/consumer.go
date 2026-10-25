package sale

import (
	"context"
	"encoding/json"
	"errors"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/eventstore"
	"flash-sale/internal/platform/value"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

func (s Store) Consume(ctx context.Context, topic, key string, raw []byte) error {
	en, e := events.Decode(topic, key, raw)
	if e != nil {
		return e
	}
	_, e = s.Outbox().Consume(ctx, "admission-config-v1", en.EventID, raw, func(tx pgx.Tx) error { return s.Handle(ctx, tx, en) })
	return e
}
func (s Store) Handle(ctx context.Context, tx pgx.Tx, en events.Envelope) error {
	if en.Type != "SaleConfigured" && en.Type != "SaleActivated" && en.Type != "SaleCancelled" {
		return &events.QuarantineError{Reason: "unexpected Admission configuration kind"}
	}
	id := strings.ToLower(en.AggregateID)
	if _, e := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", id); e != nil {
		return e
	}
	var current int64
	var cancelled bool
	var digest *string
	e := tx.QueryRow(ctx, "SELECT version,cancelled,allocation_digest FROM applied_sales WHERE sale_id=$1 FOR UPDATE", id).Scan(&current, &cancelled, &digest)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if en.AggregateVersion < current {
		return nil
	}
	if en.Type == "SaleCancelled" {
		var v events.SaleCancelled
		if e = json.Unmarshal(en.Data, &v); e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO applied_sales(sale_id,product_id,version,cancelled) VALUES($1,$2,$3,true) ON CONFLICT(sale_id) DO UPDATE SET version=EXCLUDED.version,cancelled=true,activated_version=NULL`, id, v.ProductID, v.Version)
		return e
	}
	var config events.SaleConfigured
	if e = json.Unmarshal(en.Data, &config); e != nil {
		return e
	}
	d, e := value.ConfigurationDigest(en.Data)
	if e != nil {
		return e
	}
	if config.PerShopperUnitCap < 1 || config.PerShopperUnitCap > value.MaxSafeInteger {
		return &events.QuarantineError{Reason: "invalid canonical cap"}
	}
	start, e := time.Parse(time.RFC3339Nano, config.StartsAt)
	if e != nil {
		return e
	}
	end, e := time.Parse(time.RFC3339Nano, config.EndsAt)
	if e != nil || !start.Before(end) {
		return &events.QuarantineError{Reason: "invalid sale window"}
	}
	if cancelled && current >= config.Version {
		return nil
	}
	if en.Type == "SaleActivated" {
		if current == config.Version && digest != nil {
			if *digest != d {
				return &events.QuarantineError{Reason: "activation digest differs from applied configuration"}
			}
			_, e = tx.Exec(ctx, "UPDATE applied_sales SET activated_version=$2 WHERE sale_id=$1 AND NOT cancelled", id, config.Version)
			return e
		}
		var prior string
		e = tx.QueryRow(ctx, "SELECT allocation_digest FROM early_sale_activation WHERE sale_id=$1 AND version=$2", id, config.Version).Scan(&prior)
		if e == nil && prior != d {
			return &events.QuarantineError{Reason: "contradictory early activation"}
		}
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		_, e = tx.Exec(ctx, "INSERT INTO early_sale_activation(sale_id,version,allocation_digest,configuration) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", id, config.Version, d, en.Data)
		return e
	}
	if current == config.Version && digest != nil {
		if *digest != d {
			return &events.QuarantineError{Reason: "same configuration version changed terms"}
		}
		return nil
	}
	var early *int64
	var earlyDigest string
	e = tx.QueryRow(ctx, "SELECT allocation_digest FROM early_sale_activation WHERE sale_id=$1 AND version=$2", id, config.Version).Scan(&earlyDigest)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if e == nil && earlyDigest == d {
		early = &config.Version
	}
	_, e = tx.Exec(ctx, `INSERT INTO applied_sales(sale_id,product_id,version,configuration,allocation_digest,per_shopper_unit_cap,starts_at,ends_at,original_starts_at,activated_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$7,$9) ON CONFLICT(sale_id) DO UPDATE SET product_id=EXCLUDED.product_id,version=EXCLUDED.version,configuration=EXCLUDED.configuration,allocation_digest=EXCLUDED.allocation_digest,per_shopper_unit_cap=EXCLUDED.per_shopper_unit_cap,starts_at=EXCLUDED.starts_at,ends_at=EXCLUDED.ends_at,activated_version=EXCLUDED.activated_version,cancelled=false`, id, config.ProductID, config.Version, en.Data, d, config.PerShopperUnitCap, start, end, early)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "DELETE FROM early_sale_activation WHERE sale_id=$1 AND version<=$2", id, config.Version); e != nil {
		return e
	}
	data, _ := json.Marshal(events.SaleReady{SaleID: config.SaleID, ProductID: config.ProductID, Version: config.Version, Participant: "admission", AllocationDigest: d, Status: "READY"})
	return eventstore.Enqueue(ctx, tx, events.Envelope{EventID: clock.UUID(), SchemaVersion: 1, Type: "SaleReady", OccurredAt: s.now().Format(time.RFC3339Nano), Producer: "admission", AggregateID: config.SaleID, AggregateVersion: config.Version, CorrelationID: en.CorrelationID, Data: data})
}
