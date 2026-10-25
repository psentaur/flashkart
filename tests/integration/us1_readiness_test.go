//go:build integration

package integration

import (
	"context"
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
