// Package telemetry registers bounded labels in a private Prometheus registry.
package telemetry

import (
	"flash-sale/internal/platform/httpapi"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"log/slog"
	"net/http"
	"time"
)

type Metrics struct {
	Registry *prometheus.Registry
	Requests *prometheus.CounterVec
	Latency  *prometheus.HistogramVec
	Excluded *prometheus.CounterVec
}

func New() *Metrics {
	r := prometheus.NewRegistry()
	m := &Metrics{r, prometheus.NewCounterVec(prometheus.CounterOpts{Name: "flashkart_http_requests_total", Help: "Requests by registered route and status class"}, []string{"route", "status"}), prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "flashkart_http_seconds", Help: "Initial response latency", Buckets: prometheus.DefBuckets}, []string{"route"}), prometheus.NewCounterVec(prometheus.CounterOpts{Name: "purchase_excluded_submissions_total", Help: "Traffic excluded from sale analytics"}, []string{"reason"})}
	r.MustRegister(m.Requests, m.Latency, m.Excluded)
	return m
}
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}
func (m *Metrics) Exclude(reason string) {
	switch reason {
	case "malformed", "unauthorized", "unknown_target":
		m.Excluded.WithLabelValues(reason).Inc()
	default:
		m.Excluded.WithLabelValues("other").Inc()
	}
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (w *recorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *recorder) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(p)
}

// Observe records a registered route; route is supplied by registration, never taken from user paths or identities.
func (m *Metrics) Observe(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &recorder{ResponseWriter: w}
		next.ServeHTTP(rw, r)
		status := rw.status
		if status == 0 {
			status = 200
		}
		class := "5xx"
		switch {
		case status < 300:
			class = "2xx"
		case status < 400:
			class = "3xx"
		case status < 500:
			class = "4xx"
		}
		m.Requests.WithLabelValues(route, class).Inc()
		m.Latency.WithLabelValues(route).Observe(time.Since(start).Seconds())
		slog.InfoContext(r.Context(), "request complete", "route", route, "status", status, "correlation_id", httpapi.Correlation(r.Context()), "duration_ms", time.Since(start).Milliseconds())
	})
}
