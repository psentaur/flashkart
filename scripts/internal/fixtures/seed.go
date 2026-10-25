// Package fixtures generates only disposable local identities and service certificates.
package fixtures

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/value"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

func write(path string, b []byte, mode os.FileMode) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	if e := os.WriteFile(path, b, mode); e != nil {
		return e
	}
	return os.Chmod(path, mode)
}
func Seed() error { return SeedAt(time.Now().UTC()) }

// SeedAt refreshes tokens without replacing the retained dataset's subjects.
func SeedAt(now time.Time) error {
	private := os.Getenv("JWT_PRIVATE_KEY_FILE")
	public := os.Getenv("JWT_PUBLIC_KEY_FILE")
	tokens := os.Getenv("SEEDED_TOKENS_FILE")
	if private == "" || public == "" || tokens == "" {
		return fmt.Errorf("fixture key/token paths required")
	}
	var key *rsa.PrivateKey
	raw, e := os.ReadFile(private)
	if os.IsNotExist(e) {
		key, e = rsa.GenerateKey(rand.Reader, 2048)
		if e != nil {
			return e
		}
		if e = write(private, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); e != nil {
			return e
		}
	} else if e == nil {
		key, e = jwt.ParseRSAPrivateKeyFromPEM(raw)
	}
	if e != nil {
		return fmt.Errorf("local signing key unavailable")
	}
	der, e := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if e != nil {
		return e
	}
	if e = write(public, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0644); e != nil {
		return e
	}
	now = now.UTC()
	type identity struct {
		Subject string `json:"subject"`
		Role    string `json:"role"`
		Token   string `json:"token"`
	}
	items := []identity{}
	prior := []identity{}
	if manifest, err := os.ReadFile(tokens); err == nil {
		var saved struct {
			Identities []identity `json:"identities"`
		}
		if json.Unmarshal(manifest, &saved) != nil || len(saved.Identities) != 101 {
			return fmt.Errorf("existing identity manifest invalid; preserve it for repair")
		}
		seen := map[string]bool{}
		for i, item := range saved.Identities {
			role := "SHOPPER"
			if i == 0 {
				role = "OPERATOR"
			}
			if value.UUID(item.Subject) != nil || item.Role != role || seen[item.Subject] {
				return fmt.Errorf("existing identity manifest has invalid or duplicate participants")
			}
			seen[item.Subject] = true
		}
		prior = saved.Identities
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("existing identity manifest unavailable")
	}

	issuer := os.Getenv("JWT_ISSUER")
	if issuer == "" {
		issuer = "flashkart-local"
	}
	audience := os.Getenv("JWT_AUDIENCE")
	if audience == "" {
		audience = "flashkart"
	}
	for i := 0; i < 101; i++ {
		role := "SHOPPER"
		if i == 0 {
			role = "OPERATOR"
		}
		subject := clock.UUID()
		if len(prior) > 0 {
			subject = prior[i].Subject
		}
		claims := auth.Claims{Role: role, RegisteredClaims: jwt.RegisteredClaims{Subject: subject, Issuer: issuer, Audience: jwt.ClaimStrings{audience}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute))}}
		token, e := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
		if e != nil {
			return e
		}
		items = append(items, identity{subject, role, token})
	}
	raw, e = json.MarshalIndent(map[string]any{"mode": "foundation_fixture", "expires_at": now.Add(15 * time.Minute), "identities": items}, "", "  ")
	if e != nil {
		return e
	}
	if e = write(tokens, raw, 0600); e != nil {
		return e
	}
	return serviceCertificates(filepath.Dir(private))
}
func serviceCertificates(dir string) error {
	caPath := filepath.Join(dir, "service-ca.pem")
	keyPath := filepath.Join(dir, "service-ca-key.pem")
	var ca *x509.Certificate
	var key *rsa.PrivateKey
	raw, e := os.ReadFile(keyPath)
	if e == nil {
		key, e = jwt.ParseRSAPrivateKeyFromPEM(raw)
		if e != nil {
			return e
		}
		cert, e := os.ReadFile(caPath)
		if e != nil {
			return e
		}
		block, _ := pem.Decode(cert)
		if block == nil {
			return fmt.Errorf("invalid service CA")
		}
		ca, e = x509.ParseCertificate(block.Bytes)
		if e != nil {
			return e
		}
	} else if os.IsNotExist(e) {
		key, e = rsa.GenerateKey(rand.Reader, 2048)
		if e != nil {
			return e
		}
		ca = &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "flashkart disposable fixture CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(7 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
		der, e := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
		if e != nil {
			return e
		}
		if e = write(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); e != nil {
			return e
		}
		if e = write(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); e != nil {
			return e
		}
	} else {
		return e
	}
	for i, name := range []string{"order", "gateway", "admission", "flash-sale", "fixture"} {
		k, e := rsa.GenerateKey(rand.Reader, 2048)
		if e != nil {
			return e
		}
		uri, _ := url.Parse("spiffe://flashkart/service/" + name)
		cert := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, URIs: []*url.URL{uri}}
		der, e := x509.CreateCertificate(rand.Reader, cert, ca, &k.PublicKey, key)
		if e != nil {
			return e
		}
		if e = write(filepath.Join(dir, name+".pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); e != nil {
			return e
		}
		if e = write(filepath.Join(dir, name+"-key.pem"), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}), 0600); e != nil {
			return e
		}
	}
	return nil
}
