package auth

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
)

// ServerTLS uses an owner service certificate and verifies the calling service's
// chain. Routes still authorize the exact SPIFFE subject through Service.
func ServerTLS(dir, service string) (*tls.Config, error) {
	cert, e := tls.LoadX509KeyPair(filepath.Join(dir, service+".pem"), filepath.Join(dir, service+"-key.pem"))
	if e != nil {
		return nil, fmt.Errorf("service certificate unavailable")
	}
	raw, e := os.ReadFile(filepath.Join(dir, "service-ca.pem"))
	if e != nil {
		return nil, fmt.Errorf("service CA unavailable")
	}
	ca := x509.NewCertPool()
	if !ca.AppendCertsFromPEM(raw) {
		return nil, fmt.Errorf("service CA invalid")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: ca}, nil
}

// ClientTLS presents the caller's service identity and verifies the server chain.
func ClientTLS(dir, service string) (*tls.Config, error) {
	cfg, err := ServerTLS(dir, service)
	if err != nil {
		return nil, err
	}
	cfg.RootCAs = cfg.ClientCAs
	cfg.ClientCAs = nil
	cfg.ClientAuth = tls.NoClientCert
	return cfg, nil
}
