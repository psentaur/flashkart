package service

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// latencyBuckets are the upper bounds, in seconds, of the request duration histogram.
var latencyBuckets = [...]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

// statusClasses are the response classes counted separately so dashboards can derive error rates.
var statusClasses = [...]string{"1xx", "2xx", "3xx", "4xx", "5xx"}

// httpMetrics records request counts by status class and a latency histogram.
// Probe and scrape endpoints are excluded so the series describe user-facing traffic.
type httpMetrics struct {
	byClass  [len(statusClasses)]atomic.Uint64
	buckets  [len(latencyBuckets)]atomic.Uint64
	count    atomic.Uint64
	sumMicro atomic.Uint64
}

func (m *httpMetrics) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics", "/healthz", "/readyz":
			next.ServeHTTP(w, r)
			return
		}
		recorder := &statusRecorder{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(recorder, r)
		m.observe(recorder.status(), time.Since(start))
	})
}

func (m *httpMetrics) observe(status int, elapsed time.Duration) {
	class := status/100 - 1
	if class < 0 || class >= len(statusClasses) {
		class = len(statusClasses) - 1
	}
	m.byClass[class].Add(1)
	seconds := elapsed.Seconds()
	for i, bound := range latencyBuckets {
		if seconds <= bound {
			m.buckets[i].Add(1)
		}
	}
	m.count.Add(1)
	m.sumMicro.Add(uint64(elapsed.Microseconds()))
}

func (m *httpMetrics) write(w io.Writer) {
	fmt.Fprintln(w, "# TYPE flashkart_http_requests_total counter")
	for i, class := range statusClasses {
		fmt.Fprintf(w, "flashkart_http_requests_total{code=%q} %d\n", class, m.byClass[i].Load())
	}
	fmt.Fprintln(w, "# TYPE flashkart_http_request_duration_seconds histogram")
	for i, bound := range latencyBuckets {
		fmt.Fprintf(w, "flashkart_http_request_duration_seconds_bucket{le=%q} %d\n",
			strconv.FormatFloat(bound, 'g', -1, 64), m.buckets[i].Load())
	}
	count := m.count.Load()
	fmt.Fprintf(w, "flashkart_http_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", count)
	fmt.Fprintf(w, "flashkart_http_request_duration_seconds_sum %g\n", float64(m.sumMicro.Load())/1e6)
	fmt.Fprintf(w, "flashkart_http_request_duration_seconds_count %d\n", count)
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.code == 0 {
		r.code = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *statusRecorder) status() int {
	if r.code == 0 {
		return http.StatusOK
	}
	return r.code
}
