//go:build runtime

package integration

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flash-sale/contracts/gen/inventory/v1"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFoundationRuntime(t *testing.T) {
	client := &http.Client{Timeout: 3 * time.Second}
	for name, base := range map[string]string{"admission": os.Getenv("ADMISSION_URL"), "flash-sale": os.Getenv("FLASH_SALE_URL"), "order": os.Getenv("ORDER_URL"), "analytics": os.Getenv("ANALYTICS_URL"), "gateway": os.Getenv("GATEWAY_URL")} {
		for _, path := range []string{"/health/live", "/health/ready", "/metrics"} {
			r, e := client.Get(base + path)
			if e != nil {
				t.Fatal(name, e)
			}
			if r.StatusCode != 200 {
				r.Body.Close()
				t.Fatal(name, path, r.StatusCode)
			}
			if path == "/health/ready" {
				var v struct {
					Mode     string `json:"mode"`
					Business bool   `json:"business_ready"`
				}
				if e = json.NewDecoder(r.Body).Decode(&v); e != nil || v.Mode != "foundation" || v.Business {
					t.Fatal("false application readiness", v, e)
				}
			}
			r.Body.Close()
		}
	}
	for _, path := range []string{"/internal/orders", "/v1/token", "/v1/purchases"} {
		r, e := client.Get(os.Getenv("GATEWAY_URL") + path)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != 404 {
			t.Fatal("unfinished route exposed", path)
		}
	}
}
func TestFixtureMTLSServiceSubjects(t *testing.T) {
	dir := filepath.Dir(os.Getenv("JWT_PUBLIC_KEY_FILE"))
	raw, e := os.ReadFile(filepath.Join(dir, "service-ca.pem"))
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw) {
		t.Fatal("invalid fixture CA")
	}
	for _, name := range []string{"order", "gateway"} {
		cert, e := tls.LoadX509KeyPair(filepath.Join(dir, name+".pem"), filepath.Join(dir, name+"-key.pem"))
		if e != nil {
			t.Fatal(e)
		}
		conn, e := grpc.NewClient(os.Getenv("INVENTORY_GRPC_ADDRESS"), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{cert}, ServerName: "localhost"})))
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, e = inventoryv1.NewInventoryReservationClient(conn).Reserve(ctx, &inventoryv1.ReserveRequest{OrderId: "unscripted"})
		cancel()
		conn.Close()
		want := codes.PermissionDenied
		if name == "order" {
			want = codes.NotFound
		}
		if status.Code(e) != want {
			t.Fatal(fmt.Sprintf("%s: got %s, want %s", name, status.Code(e), want))
		}
	}
}
