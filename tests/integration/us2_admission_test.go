//go:build integration

package integration

import (
	"context"
	"flash-sale/tests/us2"
	"testing"
	"time"
)

func TestUS2SharedQuotasAndEligibility(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := us2.Admission(ctx); err != nil {
		t.Fatal(err)
	}
}
