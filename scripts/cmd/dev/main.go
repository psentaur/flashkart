// Command dev runs only Mukthish's foundation lifecycle and explicit fixture mode.
package main

import (
	"context"
	"flash-sale/internal/platform/eventstore"
	"flash-sale/internal/platform/postgres"
	"flash-sale/scripts/internal/fixtures"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func run(ctx context.Context, args ...string) error {
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
func migrate(ctx context.Context) error {
	for _, owner := range []string{"admission", "flash-sale", "order"} {
		prefix := strings.ToUpper(strings.ReplaceAll(owner, "-", "_"))
		p, e := postgres.Open(ctx, owner, os.Getenv(prefix+"_DATABASE_URL"))
		if e != nil {
			return e
		}
		e = eventstore.Migrate(ctx, p)
		p.Close()
		if e != nil {
			return e
		}
		fmt.Println(owner + ": foundation schema current")
	}
	return nil
}
func wait(ctx context.Context) error {
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready := true
		for _, key := range []string{"ADMISSION_URL", "FLASH_SALE_URL", "ORDER_URL", "ANALYTICS_URL", "GATEWAY_URL", "CATALOG_URL", "NOTIFICATION_URL"} {
			base := os.Getenv(key)
			if base == "" {
				return fmt.Errorf("%s required", key)
			}
			r, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/health/ready", nil)
			if e != nil {
				return fmt.Errorf("invalid %s", key)
			}
			response, e := client.Do(r)
			if e != nil {
				ready = false
				continue
			}
			if response.StatusCode != 200 {
				ready = false
			}
			response.Body.Close()
		}
		if ready {
			fmt.Println("Foundation and fixture dependencies ready; business stories remain unavailable")
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("foundation readiness deadline exceeded")
		case <-ticker.C:
		}
	}
}
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: dev bootstrap|dev-up|dev-down|migrate|seed|wait-ready")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	var e error
	switch os.Args[1] {
	case "bootstrap":
		e = run(ctx, "python3", "scripts/project.py", "bootstrap")
	case "migrate":
		e = migrate(ctx)
	case "seed":
		e = fixtures.Seed()
		if e == nil {
			fmt.Println("Seeded one operator, 100 shoppers and local service certificates; no business sale seeded")
		}
	case "dev-up":
		if os.Getenv("DEV_PEER_MODE") != "fixture" {
			e = fmt.Errorf("DEV_PEER_MODE=fixture required; real adapters remain external and need E4 approval")
			break
		}
		if e = run(ctx, "python3", "scripts/dev.py", "up"); e != nil {
			break
		}
		if e = migrate(ctx); e != nil {
			break
		}
		if e = fixtures.Seed(); e != nil {
			break
		}
		if e = run(ctx, "python3", "scripts/dev.py", "launch"); e == nil {
			e = wait(ctx)
		}
	case "dev-down":
		if e = run(ctx, "python3", "scripts/dev.py", "stop"); e == nil {
			e = run(ctx, "python3", "scripts/dev.py", "down")
		}
	case "wait-ready":
		e = wait(ctx)
	default:
		e = fmt.Errorf("unknown foundation command")
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
