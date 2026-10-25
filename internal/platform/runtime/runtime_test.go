package runtime

import (
	"context"
	"errors"
	"flash-sale/internal/platform/telemetry"
	"net/http/httptest"
	"testing"
)

func TestReadinessChecksDependencyState(t *testing.T) {
	ready := false
	l := Lifecycle{Component: "order", Metrics: telemetry.New(), Checks: map[string]Check{"sql": func(context.Context) error {
		if !ready {
			return errors.New("offline")
		}
		return nil
	}}}
	h := l.Handler(nil)
	for _, want := range []int{503, 200} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/health/ready", nil))
		if w.Code != want {
			t.Fatal(w.Code)
		}
		ready = true
	}
}
