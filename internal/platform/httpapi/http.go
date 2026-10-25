// Package httpapi supplies transport primitives; domain routes register explicitly.
package httpapi

import (
	"context"
	"encoding/json"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/value"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

type correlationKey struct{}

func Correlation(ctx context.Context) string { s, _ := ctx.Value(correlationKey{}).(string); return s }
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := Correlation(r.Context())
		if id == "" {
			id = r.Header.Get("X-Correlation-ID")
		}
		if value.UUID(id) != nil {
			id = clock.UUID()
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if _, e := auth.Service(r, "gateway", "admission", "order", "flash-sale"); e == nil {
			if d, e := time.Parse(time.RFC3339Nano, r.Header.Get("X-Request-Deadline")); e == nil {
				var end context.CancelFunc
				ctx, end = context.WithDeadline(ctx, d)
				defer end()
			}
		}
		r = r.WithContext(context.WithValue(ctx, correlationKey{}, id))
		w.Header().Set("X-Correlation-ID", id)
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

type Error struct {
	Code              string          `json:"code"`
	Message           string          `json:"message"`
	CorrelationID     string          `json:"correlation_id"`
	Retryable         bool            `json:"retryable"`
	RetryAfterSeconds int             `json:"retry_after_seconds,omitempty"`
	CurrentOffer      json.RawMessage `json:"current_offer,omitempty"`
}
type rule struct {
	status int
	retry  bool
}

var rules = map[string]rule{
	"INVALID_ARGUMENT": {400, false}, "UNAUTHENTICATED": {401, false}, "FORBIDDEN": {403, false}, "NOT_FOUND": {404, false},
	"SALE_INACTIVE": {409, false}, "SALE_NOT_READY": {409, true}, "OFFER_CHANGED": {409, false}, "SOLD_OUT": {409, false}, "INSUFFICIENT_STOCK": {409, false}, "RATE_LIMITED": {429, true}, "OVERLOADED": {429, true}, "IDEMPOTENCY_CONFLICT": {409, false}, "REQUEST_IN_PROGRESS": {409, true}, "METADATA_CONFLICT": {409, false}, "QUORUM_UNAVAILABLE": {503, true}, "DEPENDENCY_UNAVAILABLE": {503, true}, "VERSION_CONFLICT": {409, false}, "SALE_WINDOW_CONFLICT": {409, false}, "PURCHASE_LIMIT_EXCEEDED": {409, false},
}

func WriteError(w http.ResponseWriter, r *http.Request, code, message string, offer json.RawMessage) {
	rule, ok := rules[code]
	if !ok {
		code = "DEPENDENCY_UNAVAILABLE"
		rule = rules[code]
		message = "service unavailable"
	}
	e := Error{Code: code, Message: message, CorrelationID: Correlation(r.Context()), Retryable: rule.retry, CurrentOffer: offer}
	if e.CorrelationID == "" {
		e.CorrelationID = clock.UUID()
	}
	if code == "REQUEST_IN_PROGRESS" || rule.status == 429 {
		e.RetryAfterSeconds = 1
		w.Header().Set("Retry-After", strconv.Itoa(e.RetryAfterSeconds))
	} else {
		w.Header().Del("Retry-After")
	}
	WriteJSON(w, rule.status, e)
}
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func Decode(w http.ResponseWriter, r *http.Request, schema string, out any) error {
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if e != nil {
		return fmt.Errorf("body exceeds limit")
	}
	if e = value.Validate(schema, raw); e != nil {
		return fmt.Errorf("invalid %s", schema)
	}
	return json.Unmarshal(raw, out)
}

// Budget shortens a context only when its remaining deadline is longer.
func Budget(ctx context.Context, max time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, max)
}
func ForwardRequest(ctx context.Context, method, url string, body io.Reader, source *http.Request) (*http.Request, error) {
	r, e := http.NewRequestWithContext(ctx, method, url, body)
	if e != nil {
		return nil, e
	}
	for _, h := range []string{"Authorization", "Idempotency-Key", "Content-Type"} {
		r.Header.Set(h, source.Header.Get(h))
	}
	r.Header.Set("X-Correlation-ID", Correlation(ctx))
	if d, ok := ctx.Deadline(); ok {
		r.Header.Set("X-Request-Deadline", d.UTC().Format(time.RFC3339Nano))
	}
	return r, nil
}
func Require(v auth.Verifier, role string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, e := v.Request(r)
		if e != nil {
			WriteError(w, r, "UNAUTHENTICATED", "valid identity required", nil)
			return
		}
		if p.Role != role {
			WriteError(w, r, "FORBIDDEN", "role denied", nil)
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
	})
}
func Register(mux *http.ServeMux, method, path string, h http.Handler) {
	mux.Handle(method+" "+path, h)
}
