// Package sale owns Admission's applied configuration, never stock or allowance.
package sale

import (
	"context"
	"encoding/json"
	"errors"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/eventstore"
	"flash-sale/internal/platform/postgres"
	"flash-sale/services/admission/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type Store struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
}

func (s Store) now() time.Time {
	if s.Clock != nil {
		return s.Clock.Now().UTC()
	}
	return time.Now().UTC()
}
func Migrate(ctx context.Context, p *pgxpool.Pool) error {
	return postgres.Migrate(ctx, p, []postgres.Migration{{Name: "001_sales", SQL: migrations.Sales}, {Name: "002_quotas", SQL: migrations.Quotas}})
}

var ErrNotFound = errors.New("known configuration not found")

type Projection struct {
	events.SaleConfigured
	Cancelled        bool
	ActivatedVersion *int64
}

func (s Store) Get(ctx context.Context, id string) (Projection, error) {
	var p Projection
	var raw []byte
	var version int64
	e := s.Pool.QueryRow(ctx, "SELECT configuration,version,cancelled,activated_version FROM applied_sales WHERE sale_id=$1", id).Scan(&raw, &version, &p.Cancelled, &p.ActivatedVersion)
	if errors.Is(e, pgx.ErrNoRows) || e == nil && raw == nil {
		return p, ErrNotFound
	}
	if e != nil {
		return p, e
	}
	if e = json.Unmarshal(raw, &p.SaleConfigured); e != nil {
		return p, e
	}
	p.Version = version
	start, _ := time.Parse(time.RFC3339Nano, p.StartsAt)
	end, _ := time.Parse(time.RFC3339Nano, p.EndsAt)
	now := s.now()
	switch {
	case p.Cancelled:
		p.Status = "CANCELLED"
	case !now.Before(end):
		p.Status = "ENDED"
	case now.Before(start):
		p.Status = "UPCOMING"
	case p.ActivatedVersion != nil && *p.ActivatedVersion == p.Version:
		p.Status = "ACTIVE"
	default:
		p.Status = "UNAVAILABLE"
	}
	return p, nil
}
func (s Store) Outbox() eventstore.Store { return eventstore.Store{Pool: s.Pool} }
