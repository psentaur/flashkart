// Package salehttp exposes only the reviewed sale routes.
package salehttp

import (
	"encoding/json"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/httpapi"
	"flash-sale/internal/platform/value"
	"flash-sale/services/flash-sale/internal/sale"
	"io"
	"net/http"
	"strings"
	"time"
)

func Handler(store sale.Store, v auth.Verifier) http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, r *http.Request, result any, err error, status int) {
		if err != nil {
			code := sale.Code(err)
			message := "Sale service unavailable."
			if code != "DEPENDENCY_UNAVAILABLE" {
				message = err.Error()
			}
			httpapi.WriteError(w, r, code, message, nil)
			return
		}
		if configured, ok := result.(sale.Sale); ok {
			at, e := store.FreezeAt(r.Context(), configured.SaleID)
			if e != nil {
				httpapi.WriteError(w, r, "DEPENDENCY_UNAVAILABLE", "Sale service unavailable.", nil)
				return
			}
			w.Header().Set("X-Sale-Freeze-At", at.UTC().Format(time.RFC3339Nano))
		}
		httpapi.WriteJSON(w, status, result)
	}
	write := func(create bool) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var input sale.Write
			if httpapi.Decode(w, r, "SaleWrite", &input) != nil {
				httpapi.WriteError(w, r, "INVALID_ARGUMENT", "Invalid sale configuration.", nil)
				return
			}
			if !create && (value.UUID(r.PathValue("sale_id")) != nil || !strings.EqualFold(r.PathValue("sale_id"), input.SaleID)) {
				httpapi.WriteError(w, r, "INVALID_ARGUMENT", "Path and body sale IDs must match.", nil)
				return
			}
			p, _ := auth.FromContext(r.Context())
			result, e := store.Put(r.Context(), input, p.Subject, httpapi.Correlation(r.Context()), create)
			status := 200
			if create {
				status = 201
			}
			reply(w, r, result, e, status)
		})
	}
	mux.Handle("GET /v1/admin/sales/{sale_id}", httpapi.Require(v, "OPERATOR", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, e := store.Get(r.Context(), r.PathValue("sale_id"))
		reply(w, r, result, e, 200)
	})))
	mux.Handle("POST /v1/admin/sales", httpapi.Require(v, "OPERATOR", write(true)))
	mux.Handle("PUT /v1/admin/sales/{sale_id}", httpapi.Require(v, "OPERATOR", write(false)))
	mux.Handle("POST /v1/admin/sales/{sale_id}/cancel", httpapi.Require(v, "OPERATOR", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		var input struct {
			ExpectedVersion int64 `json:"expected_version"`
		}
		if e == nil {
			schema, err := value.Schema("public.openapi.json", "/paths/~1v1~1admin~1sales~1{sale_id}~1cancel/post/requestBody/content/application~1json/schema", false)
			doc, parse := value.JSON(raw)
			if err != nil || parse != nil || schema.Validate(doc) != nil {
				e = io.ErrUnexpectedEOF
			}
		}
		if e != nil || json.Unmarshal(raw, &input) != nil {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "Valid expected version required.", nil)
			return
		}
		result, e := store.Cancel(r.Context(), r.PathValue("sale_id"), input.ExpectedVersion, httpapi.Correlation(r.Context()))
		reply(w, r, result, e, 200)
	})))
	offer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, e := store.Get(r.Context(), r.PathValue("sale_id"))
		reply(w, r, result.Offer, e, 200)
	})
	mux.Handle("GET /v1/sales/{sale_id}/offer", offer)
	mux.Handle("GET /internal/offers/{sale_id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, e := auth.Service(r, "order"); e != nil {
			code := "UNAUTHENTICATED"
			if _, e = auth.Service(r, "gateway", "admission", "analytics", "flash-sale"); e == nil {
				code = "FORBIDDEN"
			}
			httpapi.WriteError(w, r, code, "Order service identity required.", nil)
			return
		}
		offer.ServeHTTP(w, r)
	}))
	return httpapi.Middleware(mux)
}
