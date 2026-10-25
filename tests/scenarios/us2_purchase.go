package scenarios

import (
	"context"
	"encoding/json"
	"flash-sale/tests/us2"
	"fmt"
	"time"
)

func init() {
	for _, s := range []struct {
		name, description string
		run               func(context.Context) error
	}{
		{"purchase-success", "Public purchase, owned SQL, scripted Inventory gRPC and Payment through real Kafka", us2.Journey},
		{"shopper-cap", "Scripted cap=1/quantity=2, distinct-key replies and 100 separate shopper identities; E1 invariants unverified", us2.ShopperCap},
		{"duplicate-replay", "50 concurrent matching requests, stable accepted/rejected intent and interrupted acceptance", us2.Purchase},
	} {
		Register(Scenario{Name: s.name, Description: s.description, Run: func(ctx context.Context) error {
			start := time.Now()
			if err := s.run(ctx); err != nil {
				return err
			}
			b, _ := json.Marshal(map[string]any{"scenario": s.name, "status": "PASS", "mode": "fixture", "real_owner_sql": true, "live_peer_acceptance": false, "e1_stock_allowance_verification": "UNVERIFIED: owner adapter required", "duration_ms": time.Since(start).Milliseconds()})
			fmt.Println(string(b))
			return nil
		}})
	}
}
