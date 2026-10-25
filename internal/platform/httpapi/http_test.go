package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCapErrorAndCorrelation(t *testing.T) {
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, "PURCHASE_LIMIT_EXCEEDED", "cap reached", nil)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/purchases", nil))
	var e Error
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if w.Code != 409 || e.Retryable || w.Header().Get("Retry-After") != "" || e.CorrelationID == "" {
		t.Fatal(w.Code, e)
	}
}
func TestRemainingBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	next, done := Budget(ctx, 2*time.Second)
	defer done()
	a, _ := ctx.Deadline()
	b, _ := next.Deadline()
	if !a.Equal(b) {
		t.Fatal("deadline stacked")
	}
}
