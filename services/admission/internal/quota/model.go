// Package quota owns Admission's shared counters, leases and eligible attempts.
package quota

import (
	"context"
	"flash-sale/internal/platform/clock"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type Limits struct{ ShopperPerSecond, SalePerSecond, MaxInFlight int64 }

var DefaultLimits = Limits{5, 100, 50}

const LeaseLifetime = 2 * time.Second

type Store struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
}

func (s Store) Now() time.Time {
	if s.Clock != nil {
		return s.Clock.Now().UTC()
	}
	return time.Now().UTC()
}

type Decision struct {
	Admitted          bool
	Reason            string
	RetryAfterSeconds int
}

func (s Store) Release(ctx context.Context, attempt string) error {
	_, err := s.Pool.Exec(ctx, "UPDATE admission_leases SET released_at=COALESCE(released_at,$2) WHERE attempt_id=$1", attempt, s.Now())
	return err
}
