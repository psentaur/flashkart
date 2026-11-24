package market

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"flashkart/internal/identity"
)

// Burst limits keep the demonstration bounded: it can show admission control, never run unattended.
const (
	MaxBurstShoppers = 400
	MaxBurstSeconds  = 20
)

// Burster lets an administrator start a short flash crowd from inside the cluster. A browser sends
// only a handful of requests at a time, so it cannot show admission control; this sends real purchase
// attempts to the Admission Controller from many concurrent shoppers.
type Burster struct {
	// Target is the Admission Controller's base URL, such as http://admission-controller:8080.
	Target        string
	CustomerToken string
	Client        *http.Client
	Log           *slog.Logger

	mu      sync.Mutex
	current *burst
}

type burst struct {
	ID        string
	SaleID    string
	Shoppers  int
	Seconds   int
	State     string
	StartedAt time.Time
	EndedAt   time.Time

	sent, admitted, throttled, failed atomic.Int64
}

// burstView is a copy taken under the Burster lock, so encoding it never races the running burst.
type burstView struct {
	ID        string    `json:"burst_id"`
	SaleID    string    `json:"sale_id"`
	Shoppers  int       `json:"shoppers"`
	Seconds   int       `json:"seconds"`
	State     string    `json:"state"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
	Sent      int64     `json:"sent"`
	Admitted  int64     `json:"admitted"`
	Throttled int64     `json:"throttled"`
	Failed    int64     `json:"failed"`
}

func (b *burst) view() burstView {
	return burstView{b.ID, b.SaleID, b.Shoppers, b.Seconds, b.State, b.StartedAt, b.EndedAt,
		b.sent.Load(), b.admitted.Load(), b.throttled.Load(), b.failed.Load()}
}

func (b *Burster) Handler(token string, fallback http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/admin/load-bursts", admin(token, b.start))
	mux.HandleFunc("GET /api/admin/load-bursts/{burst_id}", admin(token, b.get))
	return serveAPI(mux, fallback)
}

func (b *Burster) start(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SaleID   string `json:"sale_id"`
		Shoppers int    `json:"shoppers"`
		Seconds  int    `json:"seconds"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Shoppers == 0 {
		input.Shoppers = MaxBurstShoppers
	}
	if input.Seconds == 0 {
		input.Seconds = MaxBurstSeconds
	}
	if !validID(input.SaleID) || input.Shoppers < 1 || input.Shoppers > MaxBurstShoppers || input.Seconds < 1 || input.Seconds > MaxBurstSeconds {
		writeError(w, r, 400, "INVALID_ARGUMENT", "a sale ID, 1 to 400 shoppers and 1 to 20 seconds are required")
		return
	}
	customerToken := b.CustomerToken
	if customerToken == "" {
		if _, ok := identity.FromContext(r.Context()); ok {
			_, customerToken, _ = strings.Cut(r.Header.Get("Authorization"), " ")
		}
	}
	if b.Target == "" || customerToken == "" {
		writeError(w, r, 501, "BURST_UNAVAILABLE", "load bursts are not configured for this deployment")
		return
	}
	b.mu.Lock()
	if b.current != nil && b.current.State == "RUNNING" {
		running := b.current.view()
		b.mu.Unlock()
		writeError(w, r, 409, "BURST_RUNNING", "a load burst is already running: "+running.ID)
		return
	}
	run := &burst{ID: strings.ToLower(rand.Text()), SaleID: input.SaleID, Shoppers: input.Shoppers, Seconds: input.Seconds,
		State: "RUNNING", StartedAt: time.Now().UTC()}
	b.current = run
	started := run.view()
	b.mu.Unlock()
	// The burst outlives this request; it stops on its own after the requested seconds.
	go b.run(run, customerToken)
	send(w, 202, started)
}

func (b *Burster) get(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	run := b.current
	var view burstView
	if run != nil {
		view = run.view()
	}
	b.mu.Unlock()
	if run == nil || view.ID != r.PathValue("burst_id") {
		writeError(w, r, 404, "NOT_FOUND", "load burst not found")
		return
	}
	send(w, 200, view)
}

func (b *Burster) run(run *burst, customerToken string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(run.Seconds)*time.Second)
	defer cancel()
	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
			MaxIdleConns: MaxBurstShoppers, MaxIdleConnsPerHost: MaxBurstShoppers, IdleConnTimeout: 30 * time.Second}}
	}
	url := strings.TrimRight(b.Target, "/") + "/api/attempts"
	var shoppers sync.WaitGroup
	for range run.Shoppers {
		shoppers.Go(func() {
			for ctx.Err() == nil {
				attempt := "burst-" + run.ID + "-" + strings.ToLower(rand.Text())
				body, _ := json.Marshal(map[string]string{"attempt_id": attempt, "sale_id": run.SaleID})
				req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
				if err != nil {
					return
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+customerToken)
				req.Header.Set("Idempotency-Key", attempt)
				req.Header.Set("X-Correlation-ID", "burst-"+run.ID)
				resp, err := client.Do(req)
				if ctx.Err() != nil {
					return
				}
				run.sent.Add(1)
				if err != nil {
					run.failed.Add(1)
					continue
				}
				resp.Body.Close()
				switch resp.StatusCode {
				case http.StatusAccepted:
					run.admitted.Add(1)
				case http.StatusTooManyRequests:
					run.throttled.Add(1)
				default:
					run.failed.Add(1)
				}
			}
		})
	}
	shoppers.Wait()
	b.mu.Lock()
	run.State, run.EndedAt = "COMPLETED", time.Now().UTC()
	b.mu.Unlock()
	if b.Log != nil {
		b.mu.Lock()
		v := run.view()
		b.mu.Unlock()
		b.Log.Info("load burst finished", "burst_id", run.ID, "sent", v.Sent, "admitted", v.Admitted, "throttled", v.Throttled, "failed", v.Failed)
	}
}
