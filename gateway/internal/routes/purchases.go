package routes

import (
	"bytes"
	"encoding/json"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/httpapi"
	"flash-sale/internal/platform/value"
	"net/http"
	"strings"
)

func purchases(mux *http.ServeMux, c Config) {
	shopper := func(next http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, err := c.Verifier.Request(r)
			if err != nil || p.Role != "SHOPPER" {
				if r.Method == "POST" {
					c.Metrics.Exclude("unauthorized")
				}
				code := "UNAUTHENTICATED"
				if err == nil {
					code = "FORBIDDEN"
				}
				httpapi.WriteError(w, r, code, "Shopper identity required", nil)
				return
			}
			next(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
		})
	}
	mux.Handle("POST /v1/purchases", shopper(func(w http.ResponseWriter, r *http.Request) {
		p, err := httpapi.ReadPurchase(w, r)
		if err != nil {
			c.Metrics.Exclude("malformed")
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "Invalid purchase or request key", nil)
			return
		}
		raw, _ := json.Marshal(p)
		r.Body = ioBody(raw)
		proxyPurchase(w, r, c, c.AdmissionURL, "/v1/purchases", true)
	}))
	mux.Handle("GET /v1/orders/{order_id}", shopper(func(w http.ResponseWriter, r *http.Request) {
		if value.UUID(r.PathValue("order_id")) != nil || len(r.URL.Query()) > 0 {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "Invalid order ID", nil)
			return
		}
		proxyPurchase(w, r, c, c.OrderURL, "/v1/orders/"+strings.ToLower(r.PathValue("order_id")), false)
	}))
}
func proxyPurchase(w http.ResponseWriter, r *http.Request, c Config, base, path string, submit bool) {
	req, err := httpapi.ForwardRequest(r.Context(), r.Method, strings.TrimRight(base, "/")+path, r.Body, r)
	if err != nil {
		httpapi.WriteError(w, r, "DEPENDENCY_UNAVAILABLE", "Purchase service unavailable", nil)
		return
	}
	p, _ := auth.FromContext(r.Context())
	req.Header.Set("X-Shopper-ID", p.Subject)
	req.Header.Set("X-Role", "SHOPPER")
	if submit {
		req.Header.Set("X-Attempt-ID", clock.UUID())
	}
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		httpapi.WriteError(w, r, "DEPENDENCY_UNAVAILABLE", "Outcome unknown; retry with the same key", nil)
		return
	}
	result, err := httpapi.ReadResult(resp)
	if err != nil {
		httpapi.WriteError(w, r, "DEPENDENCY_UNAVAILABLE", "Invalid purchase response", nil)
		return
	}
	if result.Status < 300 {
		var o struct {
			ShopperID string `json:"shopper_id"`
		}
		_ = json.Unmarshal(result.Body, &o)
		if o.ShopperID != p.Subject {
			httpapi.WriteError(w, r, "DEPENDENCY_UNAVAILABLE", "Invalid owner response", nil)
			return
		}
	}
	if result.ErrorCode() == "PURCHASE_LIMIT_EXCEEDED" {
		result.Header.Del("Retry-After")
	}
	result.Write(w)
}

// The body is already schema checked and bounded by ReadPurchase.
func ioBody(raw []byte) *bodyReader { return &bodyReader{bytes.NewReader(raw)} }

type bodyReader struct{ *bytes.Reader }

func (*bodyReader) Close() error { return nil }
