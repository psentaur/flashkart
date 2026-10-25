// Command server runs contract fixtures on loopback only; it is never a peer implementation.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flash-sale/contracts/gen/inventory/v1"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/httpapi"
	"flash-sale/tests/doubles"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func serve(ctx context.Context) error {
	v, e := auth.Load(os.Getenv("JWT_PUBLIC_KEY_FILE"), env("JWT_ISSUER", "flashkart-local"), env("JWT_AUDIENCE", "flashkart"))
	if e != nil {
		return e
	}
	rest := doubles.NewREST(v)
	_ = rest.Script("/v1/products", 200, []byte(`{"items":[],"next_cursor":null}`))
	errors := make(chan error, 4)
	inventory := doubles.NewInventory()
	payment := doubles.NewPayment()
	if path := os.Getenv("DOUBLES_FIXTURES_FILE"); path != "" {
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		var f doubles.Fixtures
		if e = json.Unmarshal(raw, &f); e != nil {
			return e
		}
		if e = f.Apply(inventory, rest, payment); e != nil {
			return e
		}
		if len(f.Payments) > 0 {
			go func() {
				errors <- payment.Run(ctx, strings.Split(os.Getenv("KAFKA_BROKERS"), ","), "fixture-payment-"+os.Getenv("RUN_ID"))
			}()
		}
	}
	servers := []*http.Server{}
	for _, key := range []string{"CATALOG_URL", "NOTIFICATION_URL"} {
		u, e := url.Parse(os.Getenv(key))
		if e != nil || u.Hostname() == "" {
			return fmt.Errorf("fixture URL missing")
		}
		listener, e := listen(u.Host)
		if e != nil {
			return e
		}
		server := &http.Server{Handler: httpapi.Middleware(rest), ReadHeaderTimeout: 5 * time.Second}
		servers = append(servers, server)
		go func() { errors <- server.Serve(listener) }()
	}
	defer func() {
		for _, s := range servers {
			_ = s.Close()
		}
	}()
	dir := filepath.Dir(os.Getenv("JWT_PUBLIC_KEY_FILE"))
	cert, e := tls.LoadX509KeyPair(filepath.Join(dir, "fixture.pem"), filepath.Join(dir, "fixture-key.pem"))
	if e != nil {
		return e
	}
	raw, e := os.ReadFile(filepath.Join(dir, "service-ca.pem"))
	if e != nil {
		return e
	}
	ca := x509.NewCertPool()
	if !ca.AppendCertsFromPEM(raw) {
		return fmt.Errorf("fixture CA invalid")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientCAs: ca, ClientAuth: tls.RequireAndVerifyClientCert}
	grpcServer := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsConfig)), grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		p, ok := peer.FromContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "Order service required")
		}
		tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "mTLS required")
		}
		if _, e := auth.Service(&http.Request{TLS: &tlsInfo.State}, "order"); e != nil {
			return nil, status.Error(codes.PermissionDenied, "Order service required")
		}
		return handler(ctx, req)
	}))
	inventoryv1.RegisterInventoryReservationServer(grpcServer, inventory)
	listener, e := listen(os.Getenv("INVENTORY_GRPC_ADDRESS"))
	if e != nil {
		return e
	}
	go func() { errors <- grpcServer.Serve(listener) }()
	slog.Info("contract fixture server started", "mode", "fixture", "business_engine", false)
	select {
	case <-ctx.Done():
		grpcServer.Stop()
		return nil
	case e := <-errors:
		grpcServer.Stop()
		return e
	}
}
func listen(address string) (net.Listener, error) {
	host, _, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, fmt.Errorf("fixture listener must be loopback")
		}
	}
	return net.Listen("tcp", address)
}
func env(k, f string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return f
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if e := serve(ctx); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
