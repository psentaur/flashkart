// Package adapters describes owner-supplied service boundaries, never peer database access.
package adapters

import (
	"context"
	"encoding/json"
	"flash-sale/internal/platform/value"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type Unavailable struct{ Dependency, Reason string }

func (e *Unavailable) Error() string { return e.Dependency + " unavailable: " + e.Reason }

type Adapter struct {
	Name          string   `json:"name"`
	Mode          string   `json:"mode"`
	URL           string   `json:"url"`
	CredentialEnv string   `json:"credential_env"`
	Start         []string `json:"start"`
	HealthPath    string   `json:"health_path"`
	FaultPath     string   `json:"fault_path"`
	VerifyPath    string   `json:"verify_path"`
	Capabilities  []string `json:"capabilities"`
}

func (a Adapter) Validate() error {
	u, e := url.Parse(a.URL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return &Unavailable{a.Name, "invalid adapter URL"}
	}
	if a.Mode != "real" && a.Mode != "fixture" {
		return &Unavailable{a.Name, "mode must be real or fixture"}
	}
	for _, p := range []string{a.HealthPath, a.FaultPath, a.VerifyPath} {
		if !strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
			return &Unavailable{a.Name, "explicit adapter paths required"}
		}
	}
	return nil
}
func (a Adapter) RequireReal() error {
	if e := a.Validate(); e != nil {
		return e
	}
	if a.Mode != "real" {
		return &Unavailable{a.Name, "fixture evidence cannot satisfy E1/E2 acceptance"}
	}
	if a.Name == "inventory" {
		for _, required := range []string{"stock-ledger", "shopper-sale-ledgers", "stable-reserve-results", "advance-clock", "pause-cleanup", "restart"} {
			found := false
			for _, c := range a.Capabilities {
				if c == required {
					found = true
				}
			}
			if !found {
				return &Unavailable{a.Name, "required capability absent: " + required}
			}
		}
	}
	return nil
}
func (a Adapter) Request(ctx context.Context, client *http.Client, method, path string) (*http.Response, error) {
	if e := a.Validate(); e != nil {
		return nil, e
	}
	if path != a.HealthPath && path != a.VerifyPath {
		return nil, fmt.Errorf("read-only adapter path required")
	}
	r, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.URL, "/")+path, nil)
	if e != nil {
		return nil, e
	}
	if method != "GET" {
		return nil, fmt.Errorf("read-only adapter method required")
	}
	if a.CredentialEnv != "" {
		token := os.Getenv(a.CredentialEnv)
		if token == "" {
			return nil, &Unavailable{a.Name, "credential input absent"}
		}
		r.Header.Set("Authorization", "Bearer "+token)
	}
	resp, e := client.Do(r)
	if e != nil {
		return nil, &Unavailable{a.Name, "connection failed"}
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, &Unavailable{a.Name, "adapter did not return a successful verification"}
	}
	return resp, nil
}

type Allowance struct {
	ShopperID     string `json:"shopper_id"`
	SaleID        string `json:"sale_id"`
	UnexpiredHeld int64  `json:"unexpired_held_units"`
	Confirmed     int64  `json:"confirmed_units"`
	Cap           int64  `json:"per_shopper_unit_cap"`
}
type Snapshot struct {
	Allocated      int64           `json:"allocated_units"`
	Available      int64           `json:"available_units"`
	Held           int64           `json:"held_units"`
	Sold           int64           `json:"sold_units"`
	Allowances     []Allowance     `json:"shopper_sale_ledgers"`
	ReserveResults json.RawMessage `json:"stable_reserve_results"`
}

func (s Snapshot) Verify() error {
	for _, n := range []int64{s.Allocated, s.Available, s.Held, s.Sold} {
		if n < 0 || n > value.MaxSafeInteger {
			return fmt.Errorf("invalid stock quantity")
		}
	}
	if s.Available > s.Allocated || s.Held > s.Allocated-s.Available || s.Sold != s.Allocated-s.Available-s.Held {
		return fmt.Errorf("stock invariant failed")
	}
	seen := map[string]bool{}
	for _, r := range s.Allowances {
		k := r.SaleID + ":" + r.ShopperID
		if seen[k] || value.UUID(r.SaleID) != nil || value.UUID(r.ShopperID) != nil || r.Cap < 1 || r.Cap > value.MaxSafeInteger || r.UnexpiredHeld < 0 || r.Confirmed < 0 || r.UnexpiredHeld > r.Cap || r.Confirmed > r.Cap-r.UnexpiredHeld {
			return fmt.Errorf("shopper allowance invariant failed")
		}
		seen[k] = true
	}
	if s.Allowances == nil || len(s.ReserveResults) == 0 {
		return fmt.Errorf("owner stock and shopper ledgers/reserve outcomes required")
	}
	return nil
}
