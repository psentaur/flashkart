package market

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PurchaseWork struct {
	Version    int       `json:"schema_version"`
	TraceID    string    `json:"trace_id"`
	OccurredAt time.Time `json:"occurred_at"`
	SaleID     string    `json:"sale_id"`
	AttemptID  string    `json:"attempt_id"`
	CustomerID string    `json:"customer_id"`
}

func (p PurchaseWork) valid() bool {
	return p.Version == 1 && !p.OccurredAt.IsZero() && validID(p.TraceID) &&
		validID(p.SaleID) && validID(p.AttemptID) && validID(p.CustomerID)
}

func purchaseEvent(p PurchaseWork, name, reservation string, payload map[string]any) map[string]any {
	value := map[string]any{
		"schema_version": 1, "event_id": name + ":" + p.AttemptID, "event_type": name,
		"occurred_at": p.OccurredAt, "trace_id": p.TraceID, "sale_id": p.SaleID,
		"attempt_id": p.AttemptID, "customer_id": p.CustomerID, "payload": payload,
	}
	if reservation != "" {
		value["reservation_id"] = reservation
	}
	return value
}

type Admission struct {
	DB                  *pgxpool.Pool
	Log                 *slog.Logger
	slots               chan struct{}
	mu                  sync.Mutex
	last                time.Time
	tokens, rate, burst float64
}

func NewAdmission(db *pgxpool.Pool, log *slog.Logger, rate, burst, concurrency int) *Admission {
	return &Admission{DB: db, Log: log, slots: make(chan struct{}, concurrency), rate: float64(rate),
		burst: float64(burst), tokens: float64(burst), last: time.Now()}
}

func (a *Admission) take() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	a.tokens = min(a.burst, a.tokens+now.Sub(a.last).Seconds()*a.rate)
	a.last = now
	if a.tokens < 1 {
		return false
	}
	a.tokens--
	return true
}

var errAttemptConflict = errors.New("attempt ID already belongs to another purchase intent")
var errThrottled = errors.New("admission is throttled")

func (a *Admission) Handler(token, customerID string, fallback http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/attempts", func(w http.ResponseWriter, r *http.Request) {
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if provided == r.Header.Get("Authorization") || !matchesToken(token, provided) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, r, 401, "UNAUTHENTICATED", "customer bearer token required")
			return
		}
		var input struct {
			Attempt string `json:"attempt_id"`
			Sale    string `json:"sale_id"`
		}
		if !decode(w, r, &input) {
			return
		}
		work := PurchaseWork{1, r.Header.Get("X-Correlation-ID"), time.Now().UTC(), input.Sale, input.Attempt, customerID}
		if !work.valid() || r.Header.Get("Idempotency-Key") != work.AttemptID {
			writeError(w, r, 400, "INVALID_ARGUMENT", "attempt ID must match Idempotency-Key and a sale ID is required")
			return
		}
		select {
		case a.slots <- struct{}{}:
			defer func() { <-a.slots }()
		default:
			w.Header().Set("Retry-After", "1")
			writeError(w, r, 429, "RATE_LIMITED", "admission concurrency limit reached")
			return
		}
		switch err := a.accept(r.Context(), work); {
		case errors.Is(err, errAttemptConflict):
			writeError(w, r, 409, "IDEMPOTENCY_CONFLICT", err.Error())
		case errors.Is(err, errThrottled):
			w.Header().Set("Retry-After", "1")
			writeError(w, r, 429, "RATE_LIMITED", err.Error())
		case err != nil:
			writeError(w, r, 503, "UNAVAILABLE", "acceptance could not be confirmed; retry the same attempt")
		default:
			send(w, 202, map[string]string{"attempt_id": work.AttemptID, "sale_id": work.SaleID, "state": "QUEUED"})
		}
	})
	return serveAPI(mux, fallback)
}

func lockAttempt(ctx context.Context, tx pgx.Tx, scope, id string) error {
	_, err := tx.Exec(ctx, "SELECT set_config('synchronous_commit','on',true), pg_advisory_xact_lock(hashtextextended($1,0))", scope+":"+id)
	return err
}

func (a *Admission) accept(ctx context.Context, work PurchaseWork) error {
	throttled := false
	err := pgx.BeginFunc(ctx, a.DB, func(tx pgx.Tx) error {
		if err := lockAttempt(ctx, tx, "admission", work.AttemptID); err != nil {
			return err
		}
		var sale, customer string
		err := tx.QueryRow(ctx, "SELECT sale_id,customer_id FROM admission.attempts WHERE attempt_id=$1", work.AttemptID).Scan(&sale, &customer)
		if err == nil {
			if sale != work.SaleID || customer != work.CustomerID {
				return errAttemptConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		throttled = !a.take()
		event := purchaseEvent(work, "attempt.accepted", "", map[string]any{})
		if throttled {
			event = purchaseEvent(work, "attempt.rejected", "", map[string]any{"reason": "THROTTLED"})
			identity, err := json.Marshal([]string{work.CustomerID, work.SaleID, work.AttemptID})
			if err != nil {
				return err
			}
			digest := sha256.Sum256(identity)
			event["event_id"] = "admission:throttled:" + hex.EncodeToString(digest[:])
		} else {
			body, err := json.Marshal(work)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "INSERT INTO admission.attempts VALUES ($1,$2,$3)", work.AttemptID, work.SaleID, work.CustomerID); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "INSERT INTO admission.purchase_outbox(attempt_id,body) VALUES ($1,$2::jsonb)", work.AttemptID, body); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, "INSERT INTO admission.events VALUES ($1,$2::jsonb) ON CONFLICT DO NOTHING", event["event_id"], event)
		return err
	})
	if err == nil && throttled {
		return errThrottled
	}
	return err
}

func (a *Admission) sendNext(ctx context.Context, queue PurchaseQueue) (bool, error) {
	var id, body string
	err := a.DB.QueryRow(ctx, `WITH claim AS (
		SELECT attempt_id FROM admission.purchase_outbox WHERE sent_at IS NULL AND next_attempt_at<=clock_timestamp()
		ORDER BY next_attempt_at,attempt_id LIMIT 1 FOR UPDATE SKIP LOCKED
	) UPDATE admission.purchase_outbox o SET next_attempt_at=clock_timestamp()+interval '15 seconds'
	FROM claim WHERE o.attempt_id=claim.attempt_id RETURNING o.attempt_id,o.body::text`).Scan(&id, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = queue.Send(ctx, body); err != nil {
		return false, err
	}
	_, err = a.DB.Exec(ctx, "UPDATE admission.purchase_outbox SET sent_at=clock_timestamp() WHERE attempt_id=$1 AND sent_at IS NULL", id)
	return true, err
}

func (a *Admission) Run(ctx context.Context, queue PurchaseQueue) {
	for ctx.Err() == nil {
		work, cancel := context.WithTimeout(ctx, 5*time.Second)
		sent, err := a.sendNext(work, queue)
		cancel()
		if err != nil && ctx.Err() == nil {
			a.Log.Warn("purchase outbox delivery will retry")
		}
		if !sent || err != nil {
			pause(ctx, 200*time.Millisecond)
		}
	}
}

func pause(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
