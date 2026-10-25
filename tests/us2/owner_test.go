package us2

import (
	"context"
	"encoding/json"
	"flash-sale/internal/platform/clock"
	"flash-sale/tests/adapters"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOwnerVerifierRejectsInsufficientOrFixtureEvidence(t *testing.T) {
	shopper, sale, product, orderID := clock.UUID(), clock.UUID(), clock.UUID(), clock.UUID()
	evidence := map[string]any{"run_id": "run-us2", "sale_id": sale, "product_id": product, "sampled_at": time.Now().UTC().Format(time.RFC3339Nano), "allocated_units": 100, "available_units": 100, "held_units": 0, "sold_units": 0, "reservations": []any{}, "shopper_sale_ledgers": []any{map[string]any{"shopper_id": shopper, "sale_id": sale, "unexpired_held_units": 0, "confirmed_units": 0, "per_shopper_unit_cap": 1}}, "stable_reserve_results": []any{map[string]any{"order_id": orderID, "payload_hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "result_code": "PURCHASE_LIMIT_EXCEEDED"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(evidence) }))
	defer server.Close()
	adapter := adapters.Adapter{Name: "inventory", Mode: "real", URL: server.URL, HealthPath: "/health/ready", VerifyPath: "/verify", FaultPath: "/fault", Capabilities: []string{"stock-ledger", "shopper-sale-ledgers", "stable-reserve-results", "advance-clock", "pause-cleanup", "restart"}}
	expected := OwnerExpectation{RunID: "run-us2", SaleID: sale, ProductID: product, Allocated: 100, Cap: 1, ShopperIDs: []string{shopper}, OrderIDs: []string{orderID}}
	if err := VerifyOwner(context.Background(), adapter, server.Client(), expected); err != nil {
		t.Fatal(err)
	}
	adapter.Mode = "fixture"
	if VerifyOwner(context.Background(), adapter, server.Client(), expected) == nil {
		t.Fatal("fixture certified as live E1")
	}
	adapter.Mode = "real"
	for _, field := range []string{"run_id", "sampled_at", "reservations", "shopper_sale_ledgers", "stable_reserve_results"} {
		prior := evidence[field]
		delete(evidence, field)
		if VerifyOwner(context.Background(), adapter, server.Client(), expected) == nil {
			t.Fatal("accepted missing", field)
		}
		evidence[field] = prior
	}
	evidence["shopper_sale_ledgers"] = []any{}
	if VerifyOwner(context.Background(), adapter, server.Client(), expected) == nil {
		t.Fatal("accepted incomplete shopper ledger")
	}
}
