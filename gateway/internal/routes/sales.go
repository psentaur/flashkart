// Package routes registers an explicit public allowlist; it has no business database.
package routes

import (
	"bytes"
	"encoding/json"
	"flash-sale/gateway/internal/catalog"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/httpapi"
	"flash-sale/internal/platform/telemetry"
	"flash-sale/internal/platform/value"
	"io"
	"net/http"
	"strings"
)

type Config struct {
	Verifier               auth.Verifier
	SaleURL, CatalogURL    string
	AdmissionURL, OrderURL string
	Metrics                *telemetry.Metrics
	Client                 *http.Client
	Frontend               http.Handler
}

func Handler(c Config) http.Handler {
	if c.Metrics == nil {
		c.Metrics = telemetry.New()
	}
	mux := http.NewServeMux()
	purchases(mux, c)
	cat := catalog.Client{BaseURL: c.CatalogURL, HTTP: c.Client}
	mux.Handle("GET /v1/products", cat)
	mux.Handle("GET /v1/products/{product_id}", cat)
	saleProxy := func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		contract := path
		if id := r.PathValue("sale_id"); id != "" {
			if value.UUID(id) != nil {
				httpapi.WriteError(w, r, "INVALID_ARGUMENT", "Invalid sale ID.", nil)
				return
			}
			path = strings.Replace(path, id, strings.ToLower(id), 1)
			contract = strings.Replace(path, strings.ToLower(id), "{sale_id}", 1)
		}
		if len(r.URL.Query()) > 0 {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "Unexpected query parameters.", nil)
			return
		}
		if r.Method == "PUT" || r.Method == "POST" && r.URL.Path == "/v1/admin/sales" {
			raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
			var body struct {
				SaleID string `json:"sale_id"`
			}
			if e != nil || value.Validate("SaleWrite", raw) != nil || json.Unmarshal(raw, &body) != nil || r.Method == "PUT" && !strings.EqualFold(body.SaleID, r.PathValue("sale_id")) {
				httpapi.WriteError(w, r, "INVALID_ARGUMENT", "Invalid sale configuration or mismatched ID.", nil)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.Header.Set("Content-Type", "application/json")
		}
		catalog.Proxy(w, r, c.Client, c.SaleURL, path, contract)
	}
	mux.HandleFunc("GET /v1/sales/{sale_id}/offer", saleProxy)
	for _, route := range []string{"POST /v1/admin/sales", "PUT /v1/admin/sales/{sale_id}", "GET /v1/admin/sales/{sale_id}", "POST /v1/admin/sales/{sale_id}/cancel"} {
		mux.Handle(route, httpapi.Require(c.Verifier, "OPERATOR", http.HandlerFunc(saleProxy)))
	}
	if c.Frontend != nil {
		mux.Handle("/", c.Frontend)
	}
	return httpapi.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { auth.StripForwarded(r.Header); mux.ServeHTTP(w, r) }))
}
