//go:build integration

package integration

import (
	"context"
	"flash-sale/tests/us2"
	"testing"
	"time"
)

func TestUS2ConcurrentKeysAndPaymentSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := us2.Purchase(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestUS2ScriptedShopperCapsAndRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := us2.ShopperCap(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestUS2PublicJourneyAndKafkaSettlement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := us2.Journey(ctx); err != nil {
		t.Fatal(err)
	}
}
