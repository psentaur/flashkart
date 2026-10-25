package contract

import (
	"encoding/json"
	"flash-sale/gateway"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/httpapi"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUS1CatalogProxySemantics(t *testing.T) {
	product := clock.UUID()
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"conflict", 409, `{"code":"METADATA_CONFLICT","message":"Resolve versions","correlation_id":"11111111-1111-4111-8111-111111111111","retryable":false}`, 409},
		{"quorum", 503, `{"code":"QUORUM_UNAVAILABLE","message":"Two replicas unavailable","correlation_id":"11111111-1111-4111-8111-111111111111","retryable":true}`, 503},
		{"available", 200, `{"product_id":"` + product + `","value":{"name":"Limited product","description":"Fixture","image_refs":[]},"context":{"node-a":1},"offers":[]}`, 200},
		{"invalid upstream", 200, `{"value":"not a product"}`, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Shopper-ID") != "" || r.Header.Get("Authorization") != "" {
					t.Error("unapproved identity forwarded")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer up.Close()
			h := gateway.Handler(gateway.Config{CatalogURL: up.URL})
			r := httptest.NewRequest("GET", "/v1/products/"+product, nil)
			r.Header.Set("X-Shopper-ID", clock.UUID())
			r.Header.Set("Authorization", "Bearer public-catalog-must-not-receive-this")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if tc.status != 200 {
				var e httpapi.Error
				_ = json.Unmarshal(w.Body.Bytes(), &e)
				if e.Code == "NOT_FOUND" {
					t.Fatal("conflict/unavailable became absent")
				}
			}
		})
	}
	h := gateway.Handler(gateway.Config{})
	for _, path := range []string{"/internal/offers/" + product, "/v1/inventory", "/v1/payment"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
	for _, path := range []string{"/v1/products?limit=0", "/v1/products?limit=101", "/v1/products?limit=2&limit=3", "/v1/products?cursor=", "/v1/products/nope"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
}
