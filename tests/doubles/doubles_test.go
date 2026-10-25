package doubles

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"flash-sale/contracts"
	"flash-sale/contracts/gen/inventory/v1"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/fault"
	"github.com/golang-jwt/jwt/v5"
	"net/http/httptest"
	"testing"
	"time"
)

func TestScriptedReserveReplaysAndTerminalState(t *testing.T) {
	i := NewInventory()
	req := &inventoryv1.ReserveRequest{OrderId: "order", Quantity: 1}
	i.Script(req, &inventoryv1.OperationResult{Code: inventoryv1.ResultCode_PURCHASE_LIMIT_EXCEEDED})
	for range 2 {
		v, e := i.Reserve(context.Background(), req)
		if e != nil || v.Code != inventoryv1.ResultCode_PURCHASE_LIMIT_EXCEEDED || v.Reservation != nil {
			t.Fatal(v, e)
		}
	}
	req.Quantity = 2
	v, _ := i.Reserve(context.Background(), req)
	if v.Code != inventoryv1.ResultCode_PAYLOAD_MISMATCH {
		t.Fatal("mismatch")
	}
	req.Quantity = 1
	i.Script(req, &inventoryv1.OperationResult{Code: inventoryv1.ResultCode_APPLIED, Reservation: &inventoryv1.Reservation{OrderId: "order", State: inventoryv1.ReservationState_HELD}})
	if e := i.SetTerminal("order", inventoryv1.ReservationState_EXPIRED); e != nil {
		t.Fatal(e)
	}
	v, _ = i.Reserve(context.Background(), req)
	if v.Reservation.State != inventoryv1.ReservationState_EXPIRED {
		t.Fatal("stale held replay")
	}
}

func TestScriptedPaymentStableResult(t *testing.T) {
	raw, _ := contracts.Assets.ReadFile("examples.json")
	var f struct {
		Events []events.Envelope `json:"events"`
	}
	_ = json.Unmarshal(raw, &f)
	var command, result events.Envelope
	for _, e := range f.Events {
		if e.Type == "PaymentRequested" {
			command = e
		}
		if e.Type == "PaymentSucceeded" {
			result = e
		}
	}
	p := NewPayment()
	if e := p.Script(command, result); e != nil {
		t.Fatal(e)
	}
	a, e := p.Handle(command)
	if e != nil {
		t.Fatal(e)
	}
	b, e := p.Handle(command)
	if e != nil || a.EventID != b.EventID {
		t.Fatal("unstable result")
	}
}

func TestNoticesFilterVerifiedSubjectAndRejectForgedHeaders(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	verifier := auth.Verifier{Key: &key.PublicKey, Issuer: "test", Audience: "fixture"}
	r := NewREST(verifier)
	shopper := "44444444-4444-4444-8444-444444444444"
	other := "55555555-5555-4555-8555-555555555555"
	notice := json.RawMessage(`{"notice_id":"11111111-1111-4111-8111-111111111111","shopper_id":"44444444-4444-4444-8444-444444444444","order_id":"22222222-2222-4222-8222-222222222222","outcome":"CONFIRMED","message":"confirmed","delivery_state":"DELIVERED","created_at":"2026-10-25T00:00:00Z"}`)
	if e = r.Notice(shopper, notice); e != nil {
		t.Fatal(e)
	}
	for _, subject := range []string{shopper, other, ""} {
		q := httptest.NewRequest("GET", "/v1/notifications", nil)
		q.Header.Set("X-Shopper-ID", shopper)
		if subject != "" {
			c := auth.Claims{Role: "SHOPPER", RegisteredClaims: jwt.RegisteredClaims{Subject: subject, Issuer: "test", Audience: jwt.ClaimStrings{"fixture"}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute))}}
			token, e := jwt.NewWithClaims(jwt.SigningMethodRS256, c).SignedString(key)
			if e != nil {
				t.Fatal(e)
			}
			q.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		if subject == "" {
			if w.Code != 401 {
				t.Fatal("forged header authenticated")
			}
			continue
		}
		var response struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &response)
		want := 0
		if subject == shopper {
			want = 1
		}
		if w.Code != 200 || len(response.Items) != want {
			t.Fatal("cross-subject notice leakage", w.Code, len(response.Items))
		}
	}
}

func TestExplicitClockAndPausedCleanup(t *testing.T) {
	c := clock.NewManual(time.Unix(0, 0))
	i := NewInventory()
	i.Clock = c
	req := &inventoryv1.ReserveRequest{OrderId: "fixture"}
	i.Script(req, &inventoryv1.OperationResult{Code: inventoryv1.ResultCode_APPLIED, Reservation: &inventoryv1.Reservation{OrderId: "fixture", State: inventoryv1.ReservationState_HELD}})
	due := c.Now().Add(time.Second)
	if i.Cleanup(context.Background(), "fixture", inventoryv1.ReservationState_EXPIRED, due) == nil {
		t.Fatal("early cleanup")
	}
	c.Advance(time.Second)
	f := fault.New()
	f.Set("cleanup", errors.New("paused"), nil)
	i.Fault = f
	if i.Cleanup(context.Background(), "fixture", inventoryv1.ReservationState_EXPIRED, due) == nil {
		t.Fatal("cleanup pause ignored")
	}
	f.Set("cleanup", nil, nil)
	if e := i.Cleanup(context.Background(), "fixture", inventoryv1.ReservationState_EXPIRED, due); e != nil {
		t.Fatal(e)
	}
	result, e := i.Reserve(context.Background(), req)
	if e != nil || result.Reservation.State != inventoryv1.ReservationState_EXPIRED {
		t.Fatal("terminal replay", e)
	}
}
