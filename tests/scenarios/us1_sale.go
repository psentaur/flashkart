package scenarios

import (
	"context"
	"encoding/json"
	"flash-sale/tests/us1"
	"fmt"
	"time"
)

func init() {
	for _, s := range []struct {
		name, description string
		check             func(context.Context) error
	}{
		{"sale-readiness", "Fixture readiness, version/cap digests, activation rollback, Admission and Kafka replay", us1.Readiness},
		{"sale-overlap", "Isolated SQL create/create, create/replace and replace/replace races, adjacency and cancelled-window reuse", us1.Overlap},
	} {
		Register(Scenario{Name: s.name, Description: s.description, Run: func(ctx context.Context) error {
			start := time.Now()
			if e := s.check(ctx); e != nil {
				return e
			}
			raw, e := json.Marshal(map[string]any{"scenario": s.name, "status": "PASS", "mode": "fixture", "real_owner_sql": true, "live_peer_acceptance": false, "duration_ms": time.Since(start).Milliseconds()})
			if e != nil {
				return e
			}
			fmt.Println(string(raw))
			return nil
		}})
	}
}
