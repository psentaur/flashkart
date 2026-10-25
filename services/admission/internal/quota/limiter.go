package quota

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"time"
)

func (s Store) Acquire(ctx context.Context, attempt, sale, shopper string, limits Limits) (Decision, error) {
	if limits.ShopperPerSecond < 1 || limits.SalePerSecond < 1 || limits.MaxInFlight < 1 {
		return Decision{}, fmt.Errorf("invalid admission limits")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Decision{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	// Always lock SALE before SHOPPER_SALE. The SALE row also protects lease counts.
	now := s.Now()
	bucket := now.Truncate(time.Second)
	counts := make([]int64, 2)
	starts := make([]time.Time, 2)
	keys := []struct {
		scope, id string
		limit     int64
	}{{"SALE", sale, limits.SalePerSecond}, {"SHOPPER_SALE", sale + ":" + shopper, limits.ShopperPerSecond}}
	for n, key := range keys {
		_, err = tx.Exec(ctx, `INSERT INTO admission_quotas(scope,scope_id,bucket_start,quota_limit) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, key.scope, key.id, bucket, key.limit)
		if err != nil {
			return Decision{}, err
		}
		err = tx.QueryRow(ctx, "SELECT bucket_start,count FROM admission_quotas WHERE scope=$1 AND scope_id=$2 FOR UPDATE", key.scope, key.id).Scan(&starts[n], &counts[n])
		if err != nil {
			return Decision{}, err
		}
	}
	// The shared database clock is sampled after both row locks, avoiding stale
	// buckets/lease lifetimes under contention and independent replica clocks.
	now = s.Now()
	if s.Clock == nil {
		if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
			return Decision{}, err
		}
	}
	bucket = now.Truncate(time.Second)
	for n, key := range keys {
		if !starts[n].Equal(bucket) {
			counts[n] = 0
		}
		_, err = tx.Exec(ctx, "UPDATE admission_quotas SET bucket_start=$3,count=$4,quota_limit=$5 WHERE scope=$1 AND scope_id=$2", key.scope, key.id, bucket, counts[n], key.limit)
		if err != nil {
			return Decision{}, err
		}
	}
	var until time.Time
	var released *time.Time
	var priorSale, priorShopper string
	err = tx.QueryRow(ctx, "SELECT sale_id::text,shopper_id::text,expires_at,released_at FROM admission_leases WHERE attempt_id=$1", attempt).Scan(&priorSale, &priorShopper, &until, &released)
	if err == nil {
		if priorSale != sale || priorShopper != shopper {
			return Decision{}, fmt.Errorf("attempt identity conflict")
		}
		if released != nil || !now.Before(until) {
			return Decision{Reason: "OVERLOADED", RetryAfterSeconds: 1}, tx.Commit(ctx)
		}
		return Decision{Admitted: true, Reason: "NONE"}, tx.Commit(ctx)
	}
	if err != pgx.ErrNoRows {
		return Decision{}, err
	}
	d := Decision{Admitted: true, Reason: "NONE"}
	var live int64
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM admission_leases WHERE sale_id=$1 AND released_at IS NULL AND expires_at>$2", sale, now).Scan(&live); err != nil {
		return Decision{}, err
	}
	switch {
	case counts[0] >= limits.SalePerSecond || counts[1] >= limits.ShopperPerSecond:
		d = Decision{Reason: "RATE_LIMITED", RetryAfterSeconds: 1}
	case live >= limits.MaxInFlight:
		d = Decision{Reason: "OVERLOADED", RetryAfterSeconds: 1}
	}
	if d.Admitted {
		_, err = tx.Exec(ctx, `UPDATE admission_quotas SET count=count+1 WHERE (scope='SALE' AND scope_id=$1) OR (scope='SHOPPER_SALE' AND scope_id=$2)`, sale, sale+":"+shopper)
		if err != nil {
			return Decision{}, err
		}
		_, err = tx.Exec(ctx, "INSERT INTO admission_leases(attempt_id,sale_id,shopper_id,expires_at) VALUES($1,$2,$3,$4)", attempt, sale, shopper, now.Add(LeaseLifetime))
		if err != nil {
			return Decision{}, err
		}
	}
	return d, tx.Commit(ctx)
}
