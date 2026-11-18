package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixture(t *testing.T) (*Auth, func(map[string]any) string) {
	t.Helper()
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	key := jose.JSONWebKey{Key: &private.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig"}
	body, _ := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{key}})
	a := &Auth{Mode: "clerk", Issuer: "https://unit.clerk.accounts.dev", OwnerID: "user_owner", Origins: []string{"http://localhost:5173"},
		Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://unit.clerk.accounts.dev/.well-known/jwks.json" {
				t.Fatalf("untrusted JWKS URL: %s", r.URL)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
		})}}
	sign := func(overrides map[string]any) string {
		claims := map[string]any{"iss": a.Issuer, "sub": "user_customer", "sid": "sess_test", "azp": a.Origins[0], "exp": time.Now().Add(time.Minute).Unix(), "nbf": time.Now().Add(-time.Second).Unix(), "iat": time.Now().Unix()}
		for k, v := range overrides {
			if v == nil {
				delete(claims, k)
			} else {
				claims[k] = v
			}
		}
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: private, KeyID: "test"}}, (&jose.SignerOptions{}).WithType("JWT"))
		if err != nil {
			t.Fatal(err)
		}
		token, err := jwt.Signed(signer).Claims(claims).Serialize()
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	return a, sign
}

func TestClerkSessions(t *testing.T) {
	a, sign := fixture(t)
	for _, test := range []struct {
		name   string
		claims map[string]any
		role   string
		valid  bool
	}{
		{"customer", nil, "customer", true},
		{"admin", map[string]any{"role": "admin"}, "admin", true},
		{"owner", map[string]any{"sub": "user_owner"}, "owner", true},
		{"owner metadata cannot grant ownership", map[string]any{"role": "owner"}, "customer", true},
		{"unsafe metadata ignored", map[string]any{"unsafeMetadata": map[string]string{"role": "admin"}}, "customer", true},
		{"wrong issuer", map[string]any{"iss": "https://other.clerk.accounts.dev"}, "", false},
		{"wrong origin", map[string]any{"azp": "https://attacker.example"}, "", false},
		{"missing origin", map[string]any{"azp": nil}, "", false},
		{"expired", map[string]any{"exp": time.Now().Add(-time.Minute).Unix()}, "", false},
		{"not yet valid", map[string]any{"nbf": time.Now().Add(time.Minute).Unix()}, "", false},
		{"no expiry", map[string]any{"exp": nil}, "", false},
		{"not a session", map[string]any{"sid": nil}, "", false},
		{"pending session", map[string]any{"sts": "pending"}, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, err := a.Verify(context.Background(), sign(test.claims))
			if (err == nil) != test.valid || test.valid && p.Role != test.role {
				t.Fatalf("principal=%+v err=%v", p, err)
			}
		})
	}
	token := sign(nil)
	parts := strings.Split(token, ".")
	parts[1] = parts[1][:len(parts[1])-2] + "aa"
	if _, err := a.Verify(context.Background(), strings.Join(parts, ".")); err == nil {
		t.Fatal("accepted tampered token")
	}
	if _, err := a.Verify(context.Background(), "not-a-jwt"); err == nil {
		t.Fatal("accepted malformed token")
	}
}

func TestDemoCannotRunInAWS(t *testing.T) {
	t.Setenv("FLASHKART_AUTH_MODE", "demo")
	if _, err := New("aws"); err == nil {
		t.Fatal("demo mode allowed in AWS")
	}
}

func TestConcurrentColdSigningKeyLookup(t *testing.T) {
	a, sign := fixture(t)
	token := sign(nil)
	transport := a.Client.Transport
	var fetches atomic.Int32
	a.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		fetches.Add(1)
		time.Sleep(20 * time.Millisecond)
		return transport.RoundTrip(r)
	})
	var group sync.WaitGroup
	start := make(chan struct{})
	for range 20 {
		group.Go(func() {
			<-start
			if _, err := a.Verify(context.Background(), token); err != nil {
				t.Errorf("concurrent session rejected: %v", err)
			}
		})
	}
	close(start)
	group.Wait()
	if fetches.Load() != 1 {
		t.Fatalf("JWKS fetched %d times", fetches.Load())
	}
}

func TestCustomerIdentityIsRequestScoped(t *testing.T) {
	a, sign := fixture(t)
	for _, id := range []string{"user_a", "user_b"} {
		r, _ := http.NewRequest("GET", "/api/test", nil)
		r.Header.Set("Authorization", "Bearer "+sign(map[string]any{"sub": id}))
		h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := CustomerID(r, "shared-demo-customer"); got != id {
				t.Fatalf("customer=%s, want %s", got, id)
			}
		}))
		h.ServeHTTP(discardResponse{}, r)
	}
}

type discardResponse struct{}

func (discardResponse) Header() http.Header         { return http.Header{} }
func (discardResponse) Write(p []byte) (int, error) { return len(p), nil }
func (discardResponse) WriteHeader(int)             {}
