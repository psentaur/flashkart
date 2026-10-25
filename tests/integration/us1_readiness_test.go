//go:build integration

package integration

import (
	"context"
	"flash-sale/internal/platform/clock"
	"flash-sale/tests/us1"
	"testing"
	"time"
)

func TestUS1ReadinessAndActivationRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := us1.Readiness(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestUS1ConcurrentSchedules(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := us1.Overlap(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestUS1EndedSalesFreezeOnceWithoutRepeatedRowLocks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f, err := us1.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := f.Write(clock.UUID(), 0)
	if _, err = f.Store.Put(ctx, w, clock.UUID(), clock.UUID(), true); err != nil {
		t.Fatal(err)
	}
	f.Clock.Advance(3 * time.Hour)
	if err = f.Store.Advance(ctx); err != nil {
		t.Fatal(err)
	}
	var frozen, activated bool
	if err = f.Pool.QueryRow(ctx, "SELECT frozen_at IS NOT NULL,activated_at IS NOT NULL FROM sales WHERE sale_id=$1", w.SaleID).Scan(&frozen, &activated); err != nil {
		t.Fatal(err)
	}
	if !frozen || activated {
		t.Fatal("ended sale was not frozen without activation")
	}
	locked, err := f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locked.Rollback(context.Background()) }()
	if _, err = locked.Exec(ctx, "SELECT sale_id FROM sales WHERE sale_id=$1 FOR UPDATE", w.SaleID); err != nil {
		t.Fatal(err)
	}
	scan, cancelScan := context.WithTimeout(ctx, time.Second)
	defer cancelScan()
	if err = f.Store.Advance(scan); err != nil {
		t.Fatalf("ended, frozen sale was selected again: %v", err)
	}
}
