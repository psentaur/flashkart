// Package sale owns authoritative sale configuration and readiness, never stock.
package sale

import (
	"context"
	"encoding/json"
	"errors"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/eventstore"
	"flash-sale/internal/platform/fault"
	"flash-sale/internal/platform/postgres"
	"flash-sale/internal/platform/value"
	"flash-sale/services/flash-sale/migrations"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"time"
)

type Limits struct {
	ShopperPerSecond int64 `json:"shopper_per_second"`
	SalePerSecond    int64 `json:"sale_per_second"`
	MaxInFlight      int64 `json:"max_in_flight"`
}
type Write struct {
	SaleID            string `json:"sale_id"`
	ProductID         string `json:"product_id"`
	ExpectedVersion   int64  `json:"expected_version"`
	UnitPriceMinor    int64  `json:"unit_price_minor"`
	Currency          string `json:"currency"`
	AllocatedUnits    int64  `json:"allocated_units"`
	PerShopperUnitCap *int64 `json:"per_shopper_unit_cap,omitempty"`
	StartsAt          string `json:"starts_at"`
	EndsAt            string `json:"ends_at"`
	AdmissionLimits   Limits `json:"admission_limits"`
}
type Offer struct {
	SaleID            string `json:"sale_id"`
	ProductID         string `json:"product_id"`
	Version           int64  `json:"version"`
	UnitPriceMinor    int64  `json:"unit_price_minor"`
	Currency          string `json:"currency"`
	StartsAt          string `json:"starts_at"`
	EndsAt            string `json:"ends_at"`
	Status            string `json:"status"`
	PerShopperUnitCap int64  `json:"per_shopper_unit_cap"`
}
type Sale struct {
	Offer
	AllocatedUnits    int64    `json:"allocated_units"`
	AdmissionLimits   Limits   `json:"admission_limits"`
	ReadyParticipants []string `json:"ready_participants"`
}
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Message }
func Code(e error) string {
	var x *Error
	if errors.As(e, &x) {
		return x.Code
	}
	return "DEPENDENCY_UNAVAILABLE"
}
func reject(code, message string) error { return &Error{code, message} }
func databaseError(e error) error {
	var x *pgconn.PgError
	if errors.As(e, &x) {
		switch x.Code {
		case "23P01":
			return reject("SALE_WINDOW_CONFLICT", "Another non-cancelled sale for this product overlaps the requested window.")
		case "23505":
			return reject("VERSION_CONFLICT", "Sale identity or version already exists.")
		}
	}
	return e
}
func Migrate(ctx context.Context, p *pgxpool.Pool) error {
	return postgres.Migrate(ctx, p, []postgres.Migration{{Name: "001_sales", SQL: migrations.Sales}})
}

type Store struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	Fault fault.Injector
}

func (s Store) now() time.Time {
	if s.Clock != nil {
		return s.Clock.Now().UTC()
	}
	return time.Now().UTC()
}
func (s Store) events() eventstore.Store { return eventstore.Store{Pool: s.Pool, Fault: s.Fault} }
func (s Store) transaction(ctx context.Context, fn func(pgx.Tx) error) error {
	if s.Pool == nil {
		return fmt.Errorf("sale database unavailable")
	}
	return databaseError(s.events().Transaction(ctx, fn))
}
func normalize(w Write) (Sale, error) {
	b, e := json.Marshal(w)
	if e != nil || value.Validate("SaleWrite", b) != nil {
		return Sale{}, reject("INVALID_ARGUMENT", "Invalid sale configuration.")
	}
	start, _ := time.Parse(time.RFC3339Nano, w.StartsAt)
	end, _ := time.Parse(time.RFC3339Nano, w.EndsAt)
	if !start.Before(end) {
		return Sale{}, reject("INVALID_ARGUMENT", "Sale start must be before end.")
	}
	cap := int64(1)
	if w.PerShopperUnitCap != nil {
		cap = *w.PerShopperUnitCap
	}
	return Sale{Offer: Offer{SaleID: strings.ToLower(w.SaleID), ProductID: strings.ToLower(w.ProductID), Version: w.ExpectedVersion + 1, UnitPriceMinor: w.UnitPriceMinor, Currency: w.Currency, StartsAt: start.UTC().Format(time.RFC3339Nano), EndsAt: end.UTC().Format(time.RFC3339Nano), Status: "UPCOMING", PerShopperUnitCap: cap}, AllocatedUnits: w.AllocatedUnits, AdmissionLimits: w.AdmissionLimits, ReadyParticipants: []string{}}, nil
}
func Digest(s Sale) (string, error) {
	b, e := json.Marshal(s)
	if e != nil {
		return "", e
	}
	return value.ConfigurationDigest(b)
}

type row struct {
	sale                         Sale
	original                     time.Time
	cancelled, frozen, activated *time.Time
	creation                     string
}

func load(ctx context.Context, tx pgx.Tx, id string, lock bool) (row, error) {
	var r row
	var b []byte
	q := "SELECT document,original_starts_at,cancelled_at,frozen_at,activated_at,creation_digest FROM sales WHERE sale_id=$1"
	if lock {
		q += " FOR UPDATE"
	}
	e := tx.QueryRow(ctx, q, id).Scan(&b, &r.original, &r.cancelled, &r.frozen, &r.activated, &r.creation)
	if errors.Is(e, pgx.ErrNoRows) {
		return r, reject("NOT_FOUND", "Sale not found.")
	}
	if e != nil {
		return r, e
	}
	e = json.Unmarshal(b, &r.sale)
	return r, e
}
func (s Store) view(ctx context.Context, tx pgx.Tx, r row) (Sale, error) {
	v := r.sale
	v.ReadyParticipants = []string{}
	d, e := Digest(v)
	if e != nil {
		return v, e
	}
	rows, e := tx.Query(ctx, "SELECT participant FROM sale_readiness WHERE sale_id=$1 AND version=$2 AND allocation_digest=$3 AND status='READY' ORDER BY participant", v.SaleID, v.Version, d)
	if e != nil {
		return v, e
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if e = rows.Scan(&p); e != nil {
			return v, e
		}
		v.ReadyParticipants = append(v.ReadyParticipants, p)
	}
	if e = rows.Err(); e != nil {
		return v, e
	}
	start, _ := time.Parse(time.RFC3339Nano, v.StartsAt)
	end, _ := time.Parse(time.RFC3339Nano, v.EndsAt)
	now := s.now()
	switch {
	case r.cancelled != nil:
		v.Status = "CANCELLED"
	case !now.Before(end):
		v.Status = "ENDED"
	case now.Before(start):
		v.Status = "UPCOMING"
	case len(v.ReadyParticipants) == 3 && r.activated != nil:
		v.Status = "ACTIVE"
	default:
		v.Status = "UNAVAILABLE"
	}
	return v, nil
}
func (s Store) Get(ctx context.Context, id string) (Sale, error) {
	if value.UUID(id) != nil {
		return Sale{}, reject("INVALID_ARGUMENT", "Invalid sale ID.")
	}
	var v Sale
	e := s.transaction(ctx, func(tx pgx.Tx) error {
		r, e := load(ctx, tx, id, false)
		if e != nil {
			return e
		}
		v, e = s.view(ctx, tx, r)
		return e
	})
	return v, e
}

// FreezeAt also informs the operator UI after a pre-start reschedule. The original
// boundary is kept even if a later version moves the visible window forward.
func (s Store) FreezeAt(ctx context.Context, id string) (time.Time, error) {
	if value.UUID(id) != nil {
		return time.Time{}, reject("INVALID_ARGUMENT", "Invalid sale ID.")
	}
	var at time.Time
	e := s.Pool.QueryRow(ctx, "SELECT LEAST(original_starts_at,starts_at) FROM sales WHERE sale_id=$1", id).Scan(&at)
	if errors.Is(e, pgx.ErrNoRows) {
		return at, reject("NOT_FOUND", "Sale not found.")
	}
	return at, e
}
