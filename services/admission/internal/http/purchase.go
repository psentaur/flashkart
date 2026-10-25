// Package admissionhttp validates eligible targets before recording any analytics identity.
package admissionhttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/eventstore"
	"flash-sale/internal/platform/httpapi"
	"flash-sale/internal/platform/telemetry"
	"flash-sale/internal/platform/value"
	"flash-sale/services/admission/internal/quota"
	"flash-sale/services/admission/internal/sale"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
	"time"
)

type Config struct {
	Store    sale.Store
	Verifier auth.Verifier
	OrderURL string
	Client   *http.Client
	Metrics  *telemetry.Metrics
}

func Handler(c Config) http.Handler {
	if c.Metrics == nil {
		c.Metrics = telemetry.New()
	}
	if c.Client == nil {
		c.Client = &http.Client{Timeout: 1500 * time.Millisecond}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/purchases", func(w http.ResponseWriter, r *http.Request) { purchase(w, r, c) })
	return httpapi.Middleware(mux)
}
func digest(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func purchase(w http.ResponseWriter, r *http.Request, c Config) {
	started := time.Now()
	received := quota.Store{Pool: c.Store.Pool, Clock: c.Store.Clock}.Now()
	principal, err := auth.Forwarded(r, "gateway")
	if err != nil || principal.Role != "SHOPPER" {
		c.Metrics.Exclude("unauthorized")
		httpapi.WriteError(w, r, "UNAUTHENTICATED", "Verified gateway shopper required", nil)
		return
	}
	p, err := httpapi.ReadPurchase(w, r)
	if err != nil {
		c.Metrics.Exclude("malformed")
		httpapi.WriteError(w, r, "INVALID_ARGUMENT", "Invalid purchase or request key", nil)
		return
	}
	projection, err := c.Store.Get(r.Context(), p.SaleID)
	if errors.Is(err, sale.ErrNotFound) || err == nil && projection.ProductID != p.ProductID {
		c.Metrics.Exclude("unknown_target")
		httpapi.WriteError(w, r, "NOT_FOUND", "Unknown sale/product", nil)
		return
	}
	if err != nil {
		httpapi.WriteError(w, r, "DEPENDENCY_UNAVAILABLE", "Target lookup unavailable", nil)
		return
	}
	attempt := r.Header.Get("X-Attempt-ID")
	if attempt == "" {
		attempt = clock.UUID()
	}
	if value.UUID(attempt) != nil {
		c.Metrics.Exclude("malformed")
		httpapi.WriteError(w, r, "INVALID_ARGUMENT", "Invalid attempt identity", nil)
		return
	}
	w.Header().Set("X-Attempt-ID", attempt)
	owner := clock.UUID()
	data, _ := json.Marshal(p)
	hash := digest(principal.Subject + "\n" + r.Header.Get("Idempotency-Key") + "\n" + string(data))
	var prior []byte
	var priorHash string
	err = c.Store.Pool.QueryRow(r.Context(), "SELECT payload_hash,response_snapshot FROM purchase_attempts WHERE attempt_id=$1", attempt).Scan(&priorHash, &prior)
	if err == nil {
		if hash != priorHash {
			httpapi.WriteError(w, r, "IDEMPOTENCY_CONFLICT", "Attempt identity mismatch", nil)
			return
		}
		var result httpapi.Result
		if json.Unmarshal(prior, &result) != nil {
			httpapi.WriteError(w, r, "DEPENDENCY_UNAVAILABLE", "Stored response unavailable", nil)
			return
		}
		result.Write(w)
		return
	}
	if err != pgx.ErrNoRows {
		httpapi.WriteError(w, r, "DEPENDENCY_UNAVAILABLE", "Attempts unavailable", nil)
		return
	}
	var tagOwner string
	err = c.Store.Pool.QueryRow(r.Context(), `INSERT INTO admission_requests(attempt_id,payload_hash,work_owner,lease_until) VALUES($1,$2,$3,$4) ON CONFLICT(attempt_id) DO UPDATE SET work_owner=EXCLUDED.work_owner,lease_until=EXCLUDED.lease_until WHERE admission_requests.payload_hash=EXCLUDED.payload_hash AND admission_requests.lease_until<=$5 RETURNING work_owner::text`, attempt, hash, owner, received.Add(2*time.Second), received).Scan(&tagOwner)
	if err == pgx.ErrNoRows {
		httpapi.WriteError(w, r, "REQUEST_IN_PROGRESS", "Attempt already processing", nil)
		return
	}
	if err != nil {
		httpapi.WriteError(w, r, "DEPENDENCY_UNAVAILABLE", "Attempt claim unavailable", nil)
		return
	}
	limiter := quota.Store{Pool: c.Store.Pool, Clock: c.Store.Clock}
	decision, reason := "REJECTED", "SALE_INACTIVE"
	result := httpapi.ErrorResult(r, reason, nil)
	if projection.Status == "UNAVAILABLE" {
		reason = "SALE_NOT_READY"
		result = httpapi.ErrorResult(r, reason, nil)
	}
	if projection.Status == "ACTIVE" {
		limits := projection.AdmissionLimits
		d, e := limiter.Acquire(r.Context(), attempt, p.SaleID, principal.Subject, quota.Limits{ShopperPerSecond: limits.ShopperPerSecond, SalePerSecond: limits.SalePerSecond, MaxInFlight: limits.MaxInFlight})
		if e != nil {
			reason = "DEPENDENCY_UNAVAILABLE"
			result = httpapi.ErrorResult(r, reason, nil)
		} else if !d.Admitted {
			reason = d.Reason
			result = httpapi.ErrorResult(r, reason, nil)
		} else {
			decision, reason = "ADMITTED", "NONE"
			body, _ := json.Marshal(map[string]any{"attempt_id": attempt, "shopper_id": principal.Subject, "purchase": p})
			ctx, cancel := httpapi.Budget(r.Context(), 1500*time.Millisecond)
			request, e := httpapi.ForwardRequest(ctx, "POST", strings.TrimRight(c.OrderURL, "/")+"/internal/orders", bytes.NewReader(body), r)
			if e == nil {
				request.Header.Set("X-Shopper-ID", principal.Subject)
				request.Header.Set("X-Role", "SHOPPER")
				request.Header.Set("Content-Type", "application/json")
				var response *http.Response
				response, e = c.Client.Do(request)
				if e == nil {
					result, e = httpapi.ReadResult(response)
				}
			}
			cancel()
			if e != nil {
				result = httpapi.ErrorResult(r, "DEPENDENCY_UNAVAILABLE", nil)
			}
		}
	}
	elapsed := time.Since(started).Milliseconds()
	errorCode := result.ErrorCode()
	var code *string
	if result.Status >= 400 {
		code = &errorCode
	}
	// A cancelled upstream cannot erase the final measured outcome or leak its lease.
	persist, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	snapshot, _ := json.Marshal(result)
	headers, _ := json.Marshal(result.Header)
	err = c.Store.Outbox().Transaction(persist, func(tx pgx.Tx) error {
		var activeOwner string
		if e := tx.QueryRow(persist, "SELECT work_owner::text FROM admission_requests WHERE attempt_id=$1 FOR UPDATE", attempt).Scan(&activeOwner); e != nil {
			return e
		}
		if activeOwner != owner {
			return errors.New("attempt worker fenced")
		}
		_, e := tx.Exec(persist, `INSERT INTO purchase_attempts(attempt_id,shopper_id,sale_id,product_id,received_at,decision,reason,retry_after_seconds,response_status,response_error_code,response_latency_ms,request_key_digest,payload_hash,response_snapshot,response_headers) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) ON CONFLICT DO NOTHING`, attempt, principal.Subject, p.SaleID, p.ProductID, received, decision, reason, resultRetry(result), result.Status, code, elapsed, digest(r.Header.Get("Idempotency-Key")), hash, snapshot, headers)
		if e != nil {
			return e
		}
		_, e = tx.Exec(persist, "UPDATE admission_leases SET released_at=COALESCE(released_at,$2) WHERE attempt_id=$1", attempt, limiter.Now())
		if e != nil {
			return e
		}
		payload, _ := json.Marshal(events.AttemptRecorded{AttemptID: attempt, ShopperID: principal.Subject, SaleID: p.SaleID, ProductID: p.ProductID, Decision: decision, Reason: reason, ResponseStatus: int64(result.Status), ResponseErrorCode: code, ResponseLatencyMS: elapsed})
		return eventstore.Enqueue(persist, tx, events.Envelope{EventID: attempt, SchemaVersion: 1, Type: "AttemptRecorded", OccurredAt: limiter.Now().Format(time.RFC3339Nano), Producer: "admission", AggregateID: attempt, AggregateVersion: 1, CorrelationID: httpapi.Correlation(r.Context()), Data: payload})
	})
	if err != nil {
		httpapi.WriteError(w, r, "DEPENDENCY_UNAVAILABLE", "Outcome recording unavailable; retry the same key", nil)
		return
	}
	result.Write(w)
}
func resultRetry(r httpapi.Result) *int {
	var e httpapi.Error
	_ = json.Unmarshal(r.Body, &e)
	if e.RetryAfterSeconds == 0 {
		return nil
	}
	return &e.RetryAfterSeconds
}
