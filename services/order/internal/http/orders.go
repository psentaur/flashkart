// Package orderhttp registers only Admission submission and shopper status reads.
package orderhttp

import (
	"encoding/json"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/httpapi"
	"flash-sale/internal/platform/value"
	"flash-sale/services/order/internal/order"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"strings"
)

func Handler(s order.Store, v auth.Verifier) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/orders", func(w http.ResponseWriter, r *http.Request) {
		if _, err := auth.Service(r, "admission"); err != nil {
			httpapi.WriteError(w, r, "UNAUTHENTICATED", "Admission service required", nil)
			return
		}
		var body struct {
			AttemptID string         `json:"attempt_id"`
			ShopperID string         `json:"shopper_id"`
			Purchase  order.Purchase `json:"purchase"`
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		schema, se := value.Schema("internal.openapi.json", "/paths/~1internal~1orders/post/requestBody/content/application~1json/schema", false)
		doc, de := value.JSON(raw)
		if err != nil || se != nil || de != nil || schema.Validate(doc) != nil || json.Unmarshal(raw, &body) != nil || value.RequestKey(r.Header.Get("Idempotency-Key")) != nil || len(r.Header.Values("Idempotency-Key")) != 1 {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "Invalid admitted purchase", nil)
			return
		}
		p, err := auth.Forwarded(r, "admission")
		if err != nil || p.Role != "SHOPPER" || !strings.EqualFold(p.Subject, body.ShopperID) {
			httpapi.WriteError(w, r, "FORBIDDEN", "Shopper context mismatch", nil)
			return
		}
		body.Purchase.SaleID = strings.ToLower(body.Purchase.SaleID)
		body.Purchase.ProductID = strings.ToLower(body.Purchase.ProductID)
		result, err := s.Submit(r.Context(), strings.ToLower(body.ShopperID), r.Header.Get("Idempotency-Key"), strings.ToLower(body.AttemptID), httpapi.Correlation(r.Context()), body.Purchase)
		if err != nil {
			httpapi.WriteError(w, r, "DEPENDENCY_UNAVAILABLE", "Purchase outcome unknown; retry the same key", nil)
			return
		}
		if result.Header == nil {
			result.Header = make(http.Header)
		}
		result.Header.Set("X-Attempt-ID", body.AttemptID)
		result.Write(w)
	})
	mux.Handle("GET /v1/orders/{order_id}", httpapi.Require(v, "SHOPPER", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if value.UUID(r.PathValue("order_id")) != nil || len(r.URL.Query()) > 0 {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "Invalid order ID", nil)
			return
		}
		p, _ := auth.FromContext(r.Context())
		o, err := s.Get(r.Context(), r.PathValue("order_id"), p.Subject)
		if err == pgx.ErrNoRows {
			httpapi.WriteError(w, r, "NOT_FOUND", "Order not found", nil)
			return
		}
		if err != nil {
			httpapi.WriteError(w, r, "DEPENDENCY_UNAVAILABLE", "Order unavailable", nil)
			return
		}
		httpapi.WriteJSON(w, 200, o)
	})))
	return httpapi.Middleware(mux)
}
