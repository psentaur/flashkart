package contract

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/value"
	flashsale "flash-sale/services/flash-sale"
	"github.com/golang-jwt/jwt/v5"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUS1SaleRouteAuthorizationAndValidation(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	verifier := auth.Verifier{Key: &key.PublicKey, Issuer: "test", Audience: "flashkart", Now: func() time.Time { return now }}
	handler := flashsale.Handler(flashsale.Store{}, verifier)
	token := func(role string) string {
		s, e := jwt.NewWithClaims(jwt.SigningMethodRS256, auth.Claims{Role: role, RegisteredClaims: jwt.RegisteredClaims{Subject: clock.UUID(), Issuer: "test", Audience: jwt.ClaimStrings{"flashkart"}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute))}}).SignedString(key)
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	for _, tc := range []struct {
		name, method, path, body, token string
		status                          int
		code                            string
	}{
		{"unauthenticated", "POST", "/v1/admin/sales", "{}", "", 401, "UNAUTHENTICATED"},
		{"shopper denied", "POST", "/v1/admin/sales", "{}", token("SHOPPER"), 403, "FORBIDDEN"},
		{"malformed", "POST", "/v1/admin/sales", "{}", token("OPERATOR"), 400, "INVALID_ARGUMENT"},
		{"invalid path", "GET", "/v1/sales/nope/offer", "", "", 400, "INVALID_ARGUMENT"},
		{"internal public denial", "GET", "/internal/offers/" + clock.UUID(), "", "", 401, "UNAUTHENTICATED"},
		{"forged service header", "GET", "/internal/offers/" + clock.UUID(), "", "", 401, "UNAUTHENTICATED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			r.Header.Set("X-Service-ID", "order")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			var e struct{ Code string }
			_ = json.Unmarshal(w.Body.Bytes(), &e)
			if e.Code != tc.code || value.Validate("Error", w.Body.Bytes()) != nil {
				t.Fatal(w.Body.String())
			}
		})
	}
}

func TestUS1CanonicalCapContract(t *testing.T) {
	for _, n := range []int64{0, -1, value.MaxSafeInteger + 1} {
		raw := []byte(`{"sale_id":"11111111-1111-4111-8111-111111111111","product_id":"22222222-2222-4222-8222-222222222222","version":1,"unit_price_minor":2500,"currency":"INR","starts_at":"2026-10-25T10:00:00Z","ends_at":"2026-10-25T10:10:00Z","status":"UPCOMING"}`)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		m["per_shopper_unit_cap"] = n
		b, _ := json.Marshal(m)
		if value.Validate("Offer", b) == nil {
			t.Fatalf("invalid cap accepted: %d", n)
		}
	}
}
