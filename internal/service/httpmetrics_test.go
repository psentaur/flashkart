package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPMetricsCountsClassesAndSkipsProbes(t *testing.T) {
	var m httpMetrics
	handler := m.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/missing":
			w.WriteHeader(http.StatusNotFound)
		case "/api/broken":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			w.Write([]byte("ok"))
		}
	}))
	for _, path := range []string{"/api/products", "/api/products", "/api/missing", "/api/broken", "/healthz", "/readyz", "/metrics"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	var out strings.Builder
	m.write(&out)
	text := out.String()
	for _, want := range []string{
		`flashkart_http_requests_total{code="2xx"} 2`,
		`flashkart_http_requests_total{code="4xx"} 1`,
		`flashkart_http_requests_total{code="5xx"} 1`,
		`flashkart_http_request_duration_seconds_bucket{le="+Inf"} 4`,
		`flashkart_http_request_duration_seconds_count 4`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("metrics missing %q:\n%s", want, text)
		}
	}
}

func TestHTTPMetricsHistogramIsCumulative(t *testing.T) {
	var m httpMetrics
	m.observe(http.StatusOK, 3*time.Millisecond)
	m.observe(http.StatusOK, 300*time.Millisecond)
	m.observe(http.StatusOK, 10*time.Second)

	var out strings.Builder
	m.write(&out)
	text := out.String()
	for _, want := range []string{
		`flashkart_http_request_duration_seconds_bucket{le="0.005"} 1`,
		`flashkart_http_request_duration_seconds_bucket{le="0.5"} 2`,
		`flashkart_http_request_duration_seconds_bucket{le="5"} 2`,
		`flashkart_http_request_duration_seconds_bucket{le="+Inf"} 3`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("histogram missing %q:\n%s", want, text)
		}
	}
}
