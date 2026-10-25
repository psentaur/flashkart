package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"github.com/golang-jwt/jwt/v5"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestJWTAndForgedHeaders(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	now := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	v := Verifier{Key: &key.PublicKey, Issuer: "test", Audience: "flashkart", Now: func() time.Time { return now }}
	claims := Claims{Role: "SHOPPER", RegisteredClaims: jwt.RegisteredClaims{Subject: "11111111-1111-4111-8111-111111111111", Issuer: "test", Audience: jwt.ClaimStrings{"flashkart"}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute))}}
	sign := func(c Claims) string {
		s, e := jwt.NewWithClaims(jwt.SigningMethodRS256, c).SignedString(key)
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	token := sign(claims)
	if _, e := v.Verify(token); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Claims){func(c *Claims) { c.Role = "ADMIN" }, func(c *Claims) { c.Issuer = "bad" }, func(c *Claims) { c.Audience = nil }, func(c *Claims) { c.ExpiresAt = nil }, func(c *Claims) { c.IssuedAt = nil }, func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(now) }, func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(now.Add(time.Hour)) }} {
		c := claims
		mutate(&c)
		if _, e := v.Verify(sign(c)); e == nil {
			t.Fatal("invalid claims accepted")
		}
	}
	bad, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("secret"))
	if _, e := v.Verify(bad); e == nil {
		t.Fatal("wrong algorithm")
	}
	r := httptest.NewRequest("GET", "http://localhost", nil)
	r.Header.Set("X-Shopper-ID", claims.Subject)
	r.Header.Set("X-Role", "OPERATOR")
	if _, e := Forwarded(r, "admission"); e == nil {
		t.Fatal("public forged context accepted")
	}
	uri, _ := url.Parse("spiffe://flashkart/service/admission")
	r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{URIs: []*url.URL{uri}}}}}
	if p, e := Forwarded(r, "admission"); e != nil || p.Subject != claims.Subject {
		t.Fatal(p, e)
	}
	if _, e := Forwarded(r, "gateway"); e == nil {
		t.Fatal("unauthorized service")
	}
}
