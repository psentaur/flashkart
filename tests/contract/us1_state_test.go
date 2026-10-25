//go:build integration

package contract

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flash-sale/gateway"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/value"
	flashsale "flash-sale/services/flash-sale"
	"flash-sale/tests/us1"
	"github.com/golang-jwt/jwt/v5"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestUS1SaleStateThroughGateway(t *testing.T) {
	ctx := context.Background()
	f, e := us1.New(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	now := f.Clock.Now()
	v := auth.Verifier{Key: &key.PublicKey, Issuer: "test", Audience: "flashkart", Now: f.Clock.Now}
	token, e := jwt.NewWithClaims(jwt.SigningMethodRS256, auth.Claims{Role: "OPERATOR", RegisteredClaims: jwt.RegisteredClaims{Subject: clock.UUID(), Issuer: "test", Audience: jwt.ClaimStrings{"flashkart"}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute))}}).SignedString(key)
	if e != nil {
		t.Fatal(e)
	}
	// Freeze time is advanced for business checks; the JWT test clock stays fixed.
	v.Now = func() time.Time { return now }
	upstream := httptest.NewServer(flashsale.Handler(f.Store, v))
	defer upstream.Close()
	h := gateway.Handler(gateway.Config{Verifier: v, SaleURL: upstream.URL})
	call := func(method, path string, input any, status int, schema string) *httptest.ResponseRecorder {
		t.Helper()
		var raw []byte
		if input != nil {
			raw, e = json.Marshal(input)
			if e != nil {
				t.Fatal(e)
			}
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		if schema != "" && value.Validate(schema, w.Body.Bytes()) != nil {
			t.Fatalf("invalid %s: %s", schema, w.Body.String())
		}
		return w
	}
	input := f.Write(clock.UUID(), 0)
	response := call("POST", "/v1/admin/sales", input, 201, "Sale")
	var s flashsale.Sale
	_ = json.Unmarshal(response.Body.Bytes(), &s)
	if s.PerShopperUnitCap != 1 || response.Header().Get("X-Sale-Freeze-At") == "" {
		t.Fatal("canonical default/freeze header missing")
	}
	path := "/v1/admin/sales/" + s.SaleID
	call("GET", path, nil, 200, "Sale")
	call("GET", "/v1/sales/"+s.SaleID+"/offer", nil, 200, "Offer")
	conflict := input
	conflict.SaleID = clock.UUID()
	response = call("POST", "/v1/admin/sales", conflict, 409, "Error")
	var failure struct {
		Code      string
		Retryable bool
	}
	_ = json.Unmarshal(response.Body.Bytes(), &failure)
	if failure.Code != "SALE_WINDOW_CONFLICT" || failure.Retryable || response.Header().Get("Retry-After") != "" {
		t.Fatal(response.Body.String())
	}
	for _, cap := range []int64{0, -1, value.MaxSafeInteger + 1} {
		bad := input
		bad.PerShopperUnitCap = &cap
		call("POST", "/v1/admin/sales", bad, 400, "Error")
	}
	input.ExpectedVersion = 1
	cap := int64(7)
	input.PerShopperUnitCap = &cap
	call("PUT", path, input, 200, "Sale")
	input.ExpectedVersion = 2
	input.PerShopperUnitCap = nil
	response = call("PUT", path, input, 200, "Sale")
	_ = json.Unmarshal(response.Body.Bytes(), &s)
	if s.PerShopperUnitCap != 1 || len(s.ReadyParticipants) != 0 {
		t.Fatal("replacement default/readiness")
	}
	call("PUT", "/v1/admin/sales/"+clock.UUID(), input, 400, "Error")
	call("PUT", path, input, 409, "Error")
	call("POST", path+"/cancel", map[string]any{"expected_version": 0}, 400, "Error")
	f.Clock.Advance(time.Hour)
	input.ExpectedVersion = 3
	input.PerShopperUnitCap = &cap
	call("PUT", path, input, 409, "Error")
	call("POST", path+"/cancel", map[string]any{"expected_version": 3}, 409, "Error")
	response = call("GET", "/v1/sales/"+s.SaleID+"/offer", nil, 200, "Offer")
	_ = json.Unmarshal(response.Body.Bytes(), &s.Offer)
	if s.Status != "UNAVAILABLE" {
		t.Fatal("unready at start", s.Status)
	}
	f.Clock.Advance(time.Hour)
	response = call("GET", "/v1/sales/"+s.SaleID+"/offer", nil, 200, "Offer")
	_ = json.Unmarshal(response.Body.Bytes(), &s.Offer)
	if s.Status != "ENDED" {
		t.Fatal("end boundary", s.Status)
	}
	// The private route accepts only an authenticated Order certificate identity.
	internal := flashsale.Handler(f.Store, v)
	for _, name := range []string{"order", "gateway"} {
		r := httptest.NewRequest("GET", "/internal/offers/"+s.SaleID, nil)
		uri, _ := url.Parse("spiffe://flashkart/service/" + name)
		cert := &x509.Certificate{URIs: []*url.URL{uri}}
		r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{cert}}}
		w := httptest.NewRecorder()
		internal.ServeHTTP(w, r)
		want := http.StatusForbidden
		if name == "order" {
			want = 200
		}
		if w.Code != want {
			t.Fatal(name, w.Code, w.Body.String())
		}
	}
}
