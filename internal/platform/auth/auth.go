// Package auth validates shopper JWTs and mTLS service subjects independently.
package auth

import (
	"context"
	"crypto/rsa"
	"flash-sale/internal/platform/value"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"net/http"
	"os"
	"strings"
	"time"
)

type Claims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}
type Principal struct {
	Subject string
	Role    string
	Service string
}
type contextKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, p)
}
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(contextKey{}).(Principal)
	return p, ok
}

type Verifier struct {
	Key              *rsa.PublicKey
	Issuer, Audience string
	Now              func() time.Time
}

func (v Verifier) Verify(raw string) (Principal, error) {
	if v.Key == nil || v.Key.N.BitLen() < 2048 || v.Issuer == "" || v.Audience == "" {
		return Principal{}, fmt.Errorf("verifier configuration invalid")
	}
	now := v.Now
	if now == nil {
		now = time.Now
	}
	c := new(Claims)
	_, e := jwt.ParseWithClaims(raw, c, func(*jwt.Token) (any, error) { return v.Key, nil }, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(v.Issuer), jwt.WithAudience(v.Audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(now))
	if e != nil || value.UUID(c.Subject) != nil || (c.Role != "SHOPPER" && c.Role != "OPERATOR") || c.IssuedAt == nil || c.ExpiresAt == nil {
		return Principal{}, fmt.Errorf("invalid identity")
	}
	if c.ExpiresAt.Sub(c.IssuedAt.Time) != 15*time.Minute {
		return Principal{}, fmt.Errorf("invalid token lifetime")
	}
	return Principal{Subject: strings.ToLower(c.Subject), Role: c.Role}, nil
}
func (v Verifier) Request(r *http.Request) (Principal, error) {
	parts := strings.Split(r.Header.Get("Authorization"), " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		return Principal{}, fmt.Errorf("bearer token required")
	}
	return v.Verify(parts[1])
}
func Service(r *http.Request, allowed ...string) (string, error) {
	if r.TLS != nil {
		for _, chain := range r.TLS.VerifiedChains {
			if len(chain) == 0 {
				continue
			}
			for _, uri := range chain[0].URIs {
				for _, name := range allowed {
					if uri.String() == "spiffe://flashkart/service/"+name {
						return name, nil
					}
				}
			}
		}
	}
	return "", fmt.Errorf("authenticated service required")
}
func Forwarded(r *http.Request, allowed ...string) (Principal, error) {
	service, e := Service(r, allowed...)
	if e != nil {
		return Principal{}, e
	}
	p := Principal{r.Header.Get("X-Shopper-ID"), r.Header.Get("X-Role"), service}
	if value.UUID(p.Subject) != nil || (p.Role != "SHOPPER" && p.Role != "OPERATOR") {
		return Principal{}, fmt.Errorf("invalid forwarded identity")
	}
	return p, nil
}
func StripForwarded(h http.Header) {
	for _, key := range []string{"X-Shopper-ID", "X-Role", "X-Service-ID", "X-Attempt-ID"} {
		h.Del(key)
	}
}

// Load reads a public verification key; services never receive the signing key.
func Load(path, issuer, audience string) (Verifier, error) {
	raw, e := os.ReadFile(path)
	if e != nil {
		return Verifier{}, fmt.Errorf("JWT public key unavailable; run local seed")
	}
	key, e := jwt.ParseRSAPublicKeyFromPEM(raw)
	if e != nil {
		return Verifier{}, fmt.Errorf("invalid JWT public key")
	}
	return Verifier{Key: key, Issuer: issuer, Audience: audience}, nil
}
