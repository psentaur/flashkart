package contract

import (
	"context"
	"flash-sale/tests/us2"
	"testing"
	"time"
)

func TestUS2PurchaseTransportContract(t *testing.T) {
	if err := us2.Transport(); err != nil {
		t.Fatal(err)
	}
}

func TestUS2InventoryClientContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := us2.InventoryContract(ctx); err != nil {
		t.Fatal(err)
	}
}
