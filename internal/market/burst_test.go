package market

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadBurst(t *testing.T) {
	var seen atomic.Int64
	var bad atomic.Int64
	admission := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Attempt string `json:"attempt_id"`
			Sale    string `json:"sale_id"`
		}
		if r.URL.Path != "/api/attempts" || r.Header.Get("Authorization") != "Bearer customer-token" ||
			json.NewDecoder(r.Body).Decode(&body) != nil || body.Sale != "sale-1" ||
			r.Header.Get("Idempotency-Key") != body.Attempt || !strings.HasPrefix(body.Attempt, "burst-") {
			bad.Add(1)
			w.WriteHeader(400)
			return
		}
		// Admit the first few attempts, then report overload like the Admission Controller.
		if seen.Add(1) <= 5 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer admission.Close()

	burster := &Burster{Target: admission.URL, CustomerToken: "customer-token"}
	handler := burster.Handler("admin-token", http.NotFoundHandler())
	call := func(method, path, token string, input any) (int, burstView) {
		var body bytes.Buffer
		if input != nil {
			json.NewEncoder(&body).Encode(input)
		}
		r := httptest.NewRequest(method, path, &body)
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var view burstView
		json.Unmarshal(w.Body.Bytes(), &view)
		return w.Code, view
	}

	start := map[string]any{"sale_id": "sale-1", "shoppers": 8, "seconds": 1}
	if code, _ := call("POST", "/api/admin/load-bursts", "", start); code != 401 {
		t.Fatalf("starting without a token returned %d", code)
	}
	if code, _ := call("POST", "/api/admin/load-bursts", "customer-token", start); code != 403 {
		t.Fatalf("starting with the customer token returned %d", code)
	}
	for _, invalid := range []map[string]any{{"sale_id": "", "shoppers": 8, "seconds": 1},
		{"sale_id": "sale-1", "shoppers": MaxBurstShoppers + 1, "seconds": 1}, {"sale_id": "sale-1", "shoppers": 8, "seconds": MaxBurstSeconds + 1}} {
		if code, _ := call("POST", "/api/admin/load-bursts", "admin-token", invalid); code != 400 {
			t.Fatalf("invalid burst %v returned %d", invalid, code)
		}
	}
	code, started := call("POST", "/api/admin/load-bursts", "admin-token", start)
	if code != 202 || started.State != "RUNNING" || started.Shoppers != 8 || started.SaleID != "sale-1" {
		t.Fatalf("start returned %d %+v", code, started)
	}
	if code, _ := call("POST", "/api/admin/load-bursts", "admin-token", start); code != 409 {
		t.Fatalf("a second concurrent burst returned %d", code)
	}
	if code, _ := call("GET", "/api/admin/load-bursts/unknown", "admin-token", nil); code != 404 {
		t.Fatalf("an unknown burst returned %d", code)
	}

	var done burstView
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, done = call("GET", "/api/admin/load-bursts/"+started.ID, "admin-token", nil); done.State == "COMPLETED" {
			break
		}
	}
	if done.State != "COMPLETED" || done.EndedAt.IsZero() {
		t.Fatalf("burst did not finish: %+v", done)
	}
	if bad.Load() != 0 || done.Failed != 0 || done.Admitted != 5 || done.Throttled < 1 ||
		done.Sent != done.Admitted+done.Throttled || done.Sent > seen.Load() || seen.Load()-done.Sent > int64(done.Shoppers) {
		// Requests still in flight at the deadline reach the server but are not counted: at most one per shopper.
		t.Fatalf("unexpected burst counts %+v (server saw %d, %d malformed)", done, seen.Load(), bad.Load())
	}

	// After it finishes, another burst may start; without configuration none can.
	if code, _ := call("POST", "/api/admin/load-bursts", "admin-token", map[string]any{"sale_id": "sale-1", "shoppers": 1, "seconds": 1}); code != 202 {
		t.Fatalf("a burst after completion returned %d", code)
	}
	unconfigured := (&Burster{}).Handler("admin-token", http.NotFoundHandler())
	r := httptest.NewRequest("POST", "/api/admin/load-bursts", strings.NewReader(`{"sale_id":"sale-1"}`))
	r.Header.Set("Authorization", "Bearer admin-token")
	w := httptest.NewRecorder()
	unconfigured.ServeHTTP(w, r)
	if w.Code != 501 {
		t.Fatalf("an unconfigured burst returned %d", w.Code)
	}
}
