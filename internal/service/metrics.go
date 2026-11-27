package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var apiDurationBounds = [...]time.Duration{
	5 * time.Millisecond, 10 * time.Millisecond, 25 * time.Millisecond,
	50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond,
	500 * time.Millisecond, time.Second, 2500 * time.Millisecond, 5 * time.Second,
}

type apiMeasurements struct {
	statuses [6]uint64
	buckets  [len(apiDurationBounds)]uint64
	count    uint64
	total    time.Duration
}

type apiMetrics struct {
	mu           sync.Mutex
	measurements apiMeasurements
}

type apiResponse struct {
	http.ResponseWriter
	status int
}

func (w *apiResponse) WriteHeader(status int) {
	if w.status == 0 && (status == http.StatusSwitchingProtocols || status >= 200) {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *apiResponse) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

func (w *apiResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (m *apiMetrics) serve(next http.Handler, w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		next.ServeHTTP(w, r)
		return
	}
	start := time.Now()
	response := &apiResponse{ResponseWriter: w}
	next.ServeHTTP(response, r)
	status := response.status
	if status == 0 {
		status = http.StatusOK
	}
	m.record(status, time.Since(start))
}

func (m *apiMetrics) record(status int, elapsed time.Duration) {
	class := status / 100
	if class < 1 || class >= len(m.measurements.statuses) {
		class = 0
	}
	bucket := sort.Search(len(apiDurationBounds), func(i int) bool { return elapsed <= apiDurationBounds[i] })
	m.mu.Lock()
	defer m.mu.Unlock()
	m.measurements.statuses[class]++
	m.measurements.count++
	m.measurements.total += elapsed
	if bucket < len(apiDurationBounds) {
		m.measurements.buckets[bucket]++
	}
}

func (m *apiMetrics) writePrometheus(w io.Writer) {
	m.mu.Lock()
	measurements := m.measurements
	m.mu.Unlock()
	fmt.Fprintln(w, "# TYPE flashkart_http_api_responses_total counter")
	for class, label := range [...]string{"other", "1xx", "2xx", "3xx", "4xx", "5xx"} {
		fmt.Fprintf(w, "flashkart_http_api_responses_total{status_class=%q} %d\n", label, measurements.statuses[class])
	}

	fmt.Fprintln(w, "# TYPE flashkart_http_api_duration_seconds histogram")
	var cumulative uint64
	for i, bound := range apiDurationBounds {
		cumulative += measurements.buckets[i]
		fmt.Fprintf(w, "flashkart_http_api_duration_seconds_bucket{le=%q} %d\n",
			strconv.FormatFloat(bound.Seconds(), 'f', -1, 64), cumulative)
	}
	fmt.Fprintf(w, "flashkart_http_api_duration_seconds_bucket{le=\"+Inf\"} %d\n", measurements.count)
	fmt.Fprintf(w, "flashkart_http_api_duration_seconds_sum %g\n", measurements.total.Seconds())
	fmt.Fprintf(w, "flashkart_http_api_duration_seconds_count %d\n", measurements.count)
}

func workflowMetrics(db *pgxpool.Pool, service string, out io.Writer, ctx context.Context) {
	var query string
	switch service {
	case "admission-controller":
		query = "SELECT count(*) FROM admission.purchase_outbox WHERE sent_at IS NULL"
	case "inventory-reservation":
		query = "SELECT count(*) FROM inventory.order_work WHERE delivered_at IS NULL"
	case "order-service":
		query = "SELECT count(*) FROM orders.purchases WHERE state='PENDING'"
	case "flash-sale-manager":
		query = "SELECT count(*) FROM flash_sale.drafts WHERE state='PENDING'"
	}
	if query != "" {
		var pending int64
		if db.QueryRow(ctx, query).Scan(&pending) == nil {
			fmt.Fprintf(out, "# TYPE flashkart_workflow_pending gauge\nflashkart_workflow_pending{service=%q} %d\n", service, pending)
		}
	}
	if service != "inventory-reservation" {
		return
	}
	rows, err := db.Query(ctx, "SELECT sale_id,available_stock FROM inventory.sales WHERE active")
	if err != nil {
		return
	}
	defer rows.Close()
	fmt.Fprintln(out, "# TYPE flashkart_inventory_available_stock gauge")
	for rows.Next() {
		var sale string
		var stock int64
		if rows.Scan(&sale, &stock) == nil {
			fmt.Fprintf(out, "flashkart_inventory_available_stock{sale_id=%q} %d\n", sale, stock)
		}
	}
}
