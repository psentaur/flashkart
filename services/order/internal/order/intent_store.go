package order

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/httpapi"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
	"time"
)

type Intent struct {
	OrderID, ShopperID, Key, AttemptID, CorrelationID, Owner, State string
	Purchase                                                        Purchase
	Offer                                                           *Offer
}

func PayloadHash(shopper string, p Purchase) string {
	p.SaleID = strings.ToLower(p.SaleID)
	p.ProductID = strings.ToLower(p.ProductID)
	b, _ := json.Marshal(struct {
		Shopper  string
		Purchase Purchase
	}{strings.ToLower(shopper), p})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s Store) Claim(ctx context.Context, shopper, key, attempt, correlation string, p Purchase) (Intent, *httpapi.Result, error) {
	i := Intent{OrderID: clock.UUID(), ShopperID: shopper, Key: key, AttemptID: attempt, CorrelationID: correlation, Owner: clock.UUID(), Purchase: p}
	var result *httpapi.Result
	err := s.Outbox().Transaction(ctx, func(tx pgx.Tx) error {
		now := s.Now()
		raw, _ := json.Marshal(p)
		_, err := tx.Exec(ctx, `INSERT INTO purchase_intents(shopper_id,request_key,payload_hash,purchase,order_id,original_attempt_id,correlation_id,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,'RECEIVED',$8,$8) ON CONFLICT DO NOTHING`, shopper, key, PayloadHash(shopper, p), raw, i.OrderID, attempt, correlation, now)
		if err != nil {
			return err
		}
		var hash string
		var response, offer []byte
		var until *time.Time
		err = tx.QueryRow(ctx, `SELECT order_id::text,payload_hash,original_attempt_id::text,correlation_id::text,state,response_snapshot,trusted_offer,lease_until FROM purchase_intents WHERE shopper_id=$1 AND request_key=$2 FOR UPDATE`, shopper, key).Scan(&i.OrderID, &hash, &i.AttemptID, &i.CorrelationID, &i.State, &response, &offer, &until)
		if err != nil {
			return err
		}
		if hash != PayloadHash(shopper, p) {
			r := errorFor(ctx, "IDEMPOTENCY_CONFLICT", nil)
			result = &r
			return nil
		}
		if i.State == "ACCEPTED" {
			var body []byte
			var o Order
			err := tx.QueryRow(ctx, "SELECT snapshot FROM orders WHERE order_id=$1 AND shopper_id=$2", i.OrderID, shopper).Scan(&body)
			if err != nil {
				return err
			}
			if err = json.Unmarshal(body, &o); err != nil {
				return err
			}
			r := orderResult(o)
			result = &r
			return nil
		}
		if i.State == "REJECTED" {
			var r httpapi.Result
			if err = json.Unmarshal(response, &r); err != nil {
				return err
			}
			result = &r
			return nil
		}
		if until != nil && now.Before(*until) {
			r := errorFor(ctx, "REQUEST_IN_PROGRESS", nil)
			result = &r
			return nil
		}
		if len(offer) > 0 {
			i.Offer = new(Offer)
			if err = json.Unmarshal(offer, i.Offer); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, "UPDATE purchase_intents SET state='RESERVING',lease_owner=$3,lease_until=$4,updated_at=$5 WHERE shopper_id=$1 AND request_key=$2", shopper, key, i.Owner, now.Add(5*time.Second), now)
		return err
	})
	return i, result, err
}

var ErrFenced = errors.New("purchase worker lease lost")

func (s Store) fenced(ctx context.Context, tx pgx.Tx, i Intent) error {
	var owner *string
	var until *time.Time
	var state string
	err := tx.QueryRow(ctx, "SELECT lease_owner::text,lease_until,state FROM purchase_intents WHERE order_id=$1 FOR UPDATE", i.OrderID).Scan(&owner, &until, &state)
	if err != nil {
		return err
	}
	if owner == nil || *owner != i.Owner || until == nil || !s.Now().Before(*until) || state != "RESERVING" {
		return ErrFenced
	}
	return nil
}
func (s Store) reject(ctx context.Context, i Intent, code string, offer json.RawMessage) (httpapi.Result, error) {
	r := errorFor(ctx, code, offer)
	b, _ := json.Marshal(r)
	err := s.Outbox().Transaction(ctx, func(tx pgx.Tx) error {
		if err := s.fenced(ctx, tx, i); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "UPDATE purchase_intents SET state='REJECTED',rejection_code=$2,response_snapshot=$3,lease_owner=NULL,lease_until=NULL,updated_at=$4 WHERE order_id=$1", i.OrderID, code, b, s.Now())
		return err
	})
	return r, err
}
func errorFor(ctx context.Context, code string, offer json.RawMessage) httpapi.Result {
	return httpapi.ErrorResult((&http.Request{}).WithContext(ctx), code, offer)
}
func orderResult(o Order) httpapi.Result {
	b, _ := json.Marshal(o)
	status := 200
	if o.State == "PENDING_PAYMENT" {
		status = 202
	}
	h := make(http.Header)
	h.Set("Location", "/v1/orders/"+o.OrderID)
	return httpapi.Result{Status: status, Body: b, Header: h}
}
