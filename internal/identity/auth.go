// Package identity verifies Clerk sessions and provides request-scoped identities.
package identity

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/jwt"
)

type Principal struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}
type principalKey struct{}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok && p.UserID != ""
}

func CustomerID(r *http.Request, fallback string) string {
	if p, ok := FromContext(r.Context()); ok {
		return p.UserID
	}
	return fallback
}

func Error(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if status == 401 {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}

type Auth struct {
	Mode, Issuer, OwnerID                   string
	Origins                                 []string
	DemoAdmin, DemoCustomer, DemoCustomerID string
	Client                                  *http.Client
	mu                                      sync.Mutex
	refreshing chan struct{}
	keys                                    map[string]*clerk.JSONWebKey
	loaded, attempted                       time.Time
}

func New(environment string) (*Auth, error) {
	mode := os.Getenv("FLASHKART_AUTH_MODE")
	if mode == "" {
		mode = "clerk"
	}
	a := &Auth{Mode: mode, Issuer: strings.TrimSuffix(os.Getenv("CLERK_ISSUER_URL"), "/"),
		OwnerID: os.Getenv("FLASHKART_OWNER_USER_ID"), Client: &http.Client{Timeout: 5 * time.Second}}
	if mode == "demo" {
		if environment != "local" {
			return nil, errors.New("demo authentication is allowed only in local environments")
		}
		a.DemoAdmin, a.DemoCustomer, a.DemoCustomerID = os.Getenv("FLASHKART_ADMIN_TOKEN"), os.Getenv("FLASHKART_CUSTOMER_TOKEN"), os.Getenv("FLASHKART_CUSTOMER_ID")
		if a.DemoAdmin == "" || a.DemoCustomer == "" || a.DemoCustomerID == "" || a.DemoAdmin == a.DemoCustomer {
			return nil, errors.New("demo mode requires distinct admin/customer tokens and a customer ID")
		}
		return a, nil
	}
	if mode != "clerk" {
		return nil, errors.New("FLASHKART_AUTH_MODE must be clerk or demo")
	}
	u, err := url.Parse(a.Issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, errors.New("CLERK_ISSUER_URL must be the HTTPS Clerk Frontend API origin")
	}
	for _, origin := range strings.Split(os.Getenv("FLASHKART_ALLOWED_ORIGINS"), ",") {
		origin = strings.TrimSpace(origin)
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" ||
			(u.Scheme != "https" && !(environment == "local" && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
			return nil, errors.New("FLASHKART_ALLOWED_ORIGINS requires exact frontend origins (HTTPS outside local development)")
		}
		a.Origins = append(a.Origins, origin)
	}
	return a, nil
}

func (a *Auth) Role(id, role string) string {
	if a.OwnerID != "" && id == a.OwnerID {
		return "owner"
	}
	if role == "admin" {
		return "admin"
	}
	return "customer"
}

func (a *Auth) key(ctx context.Context, id string) (*clerk.JSONWebKey, error) {
	if id == "" {
		return nil, errors.New("missing signing key ID")
	}
	a.mu.Lock()
	if key := a.keys[id]; key != nil && time.Since(a.loaded) < time.Hour {
		a.mu.Unlock()
		return key, nil
	}
	if a.refreshing != nil {
		pending := a.refreshing
		a.mu.Unlock()
		select {
		case <-pending:
			return a.key(ctx, id)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	// Unknown key IDs cannot cause an unbounded stream of JWKS requests.
	if time.Since(a.attempted) < 10*time.Second {
		a.mu.Unlock()
		return nil, errors.New("unknown signing key")
	}
	a.attempted = time.Now()
	pending := make(chan struct{})
	a.refreshing = pending
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		close(pending)
		a.refreshing = nil
		a.mu.Unlock()
	}()
	req, err := http.NewRequestWithContext(ctx, "GET", a.Issuer+"/.well-known/jwks.json", nil)
	if err != nil {
		return nil, err
	}
	res, err := a.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errors.New("signing keys unavailable")
	}
	var set clerk.JSONWebKeySet
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&set); err != nil {
		return nil, err
	}
	keys := make(map[string]*clerk.JSONWebKey)
	for _, key := range set.Keys {
		if key != nil && key.Algorithm == "RS256" && key.Use == "sig" {
			keys[key.KeyID] = key
		}
	}
	a.mu.Lock()
	a.keys = keys
	a.loaded = time.Now()
	a.mu.Unlock()
	if key := keys[id]; key != nil {
		return key, nil
	}
	return nil, errors.New("unknown signing key")
}

func same(a, b string) bool { return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func (a *Auth) Verify(ctx context.Context, token string) (Principal, error) {
	if a.Mode == "demo" {
		if same(a.DemoAdmin, token) {
			return Principal{"demo-admin", "admin"}, nil
		}
		if same(a.DemoCustomer, token) {
			return Principal{a.DemoCustomerID, "customer"}, nil
		}
		return Principal{}, errors.New("invalid demo token")
	}
	if len(token) > 16<<10 {
		return Principal{}, errors.New("invalid session")
	}
	unsafe, err := jwt.Decode(ctx, &jwt.DecodeParams{Token: token})
	if err != nil {
		return Principal{}, err
	}
	key, err := a.key(ctx, unsafe.KeyID)
	if err != nil {
		return Principal{}, err
	}
	type customClaims struct {
		Role   string `json:"role"`
		Status string `json:"sts"`
	}
	claims, err := jwt.Verify(ctx, &jwt.VerifyParams{Token: token, JWK: key,
		CustomClaimsConstructor: func(context.Context) any { return &customClaims{} },
		AuthorizedPartyHandler: func(origin string) bool {
			for _, allowed := range a.Origins {
				if origin == allowed {
					return true
				}
			}
			return false
		}})
	if err != nil {
		return Principal{}, err
	}
	custom := claims.Custom.(*customClaims)
	if claims.Issuer != a.Issuer || claims.Subject == "" || claims.SessionID == "" || claims.Expiry == nil || claims.NotBefore == nil || custom.Status == "pending" {
		return Principal{}, errors.New("invalid session claims")
	}
	return Principal{claims.Subject, a.Role(claims.Subject, custom.Role)}, nil
}

func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if header := r.Header.Get("Authorization"); header != "" {
			scheme, token, ok := strings.Cut(header, " ")
			if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
				Error(w, 401, "UNAUTHENTICATED", "Sign in to continue.")
				return
			}
			p, err := a.Verify(r.Context(), token)
			if err != nil {
				Error(w, 401, "UNAUTHENTICATED", "Your session is invalid or expired. Sign in again.")
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
		}
		next.ServeHTTP(w, r)
	})
}
