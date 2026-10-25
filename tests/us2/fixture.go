// Package us2 checks Mukthish's purchase slice with real owned SQL and scripted peers.
// No scripted reservation result proves Inventory's atomic stock/allowance guarantee.
package us2

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flash-sale/contracts/gen/inventory/v1"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/eventstore"
	"flash-sale/internal/platform/httpapi"
	order "flash-sale/services/order"
	"flash-sale/tests/doubles"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

func database(ctx context.Context, owner string) (*pgxpool.Pool, func(), error) {
	key := strings.ToUpper(strings.ReplaceAll(owner, "-", "_")) + "_DATABASE_URL"
	raw := os.Getenv(key)
	if raw == "" {
		return nil, nil, fmt.Errorf("%s required", key)
	}
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		return nil, nil, err
	}
	schema := "us2_" + strings.ReplaceAll(clock.UUID(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		return nil, nil, err
	}
	cfg := admin.Config()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.MaxConns = 16
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	close := func() {
		if pool != nil {
			pool.Close()
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	}
	if err == nil {
		err = eventstore.Migrate(ctx, pool)
	}
	if err != nil {
		close()
		return nil, nil, err
	}
	return pool, close, nil
}

type countingInventory struct {
	*doubles.Inventory
	calls        atomic.Int64
	LoseResponse atomic.Bool
}

func (i *countingInventory) Reserve(ctx context.Context, r *inventoryv1.ReserveRequest) (*inventoryv1.OperationResult, error) {
	i.calls.Add(1)
	result, err := i.Inventory.Reserve(ctx, r)
	if err == nil && i.LoseResponse.Swap(false) {
		return nil, context.DeadlineExceeded
	}
	return result, err
}

type Fixture struct {
	Pool     *pgxpool.Pool
	Store    order.Store
	Peer     *countingInventory
	Clock    *clock.Manual
	Purchase order.Purchase
	Offer    order.Offer
	close    func()
}

func New(ctx context.Context) (*Fixture, error) {
	pool, cleanup, err := database(ctx, "order")
	if err != nil {
		return nil, err
	}
	if err = order.Migrate(ctx, pool); err != nil {
		cleanup()
		return nil, err
	}
	c := clock.NewManual(time.Now().UTC().Truncate(time.Second))
	i := &countingInventory{Inventory: doubles.NewInventory()}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cleanup()
		return nil, err
	}
	server := grpc.NewServer()
	inventoryv1.RegisterInventoryReservationServer(server, i)
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		server.Stop()
		cleanup()
		return nil, err
	}
	f := &Fixture{Pool: pool, Peer: i, Clock: c}
	f.close = func() { _ = conn.Close(); server.Stop(); cleanup() }
	f.Purchase = order.Purchase{SaleID: clock.UUID(), ProductID: clock.UUID(), Quantity: 1, ExpectedOfferVersion: 1, ExpectedUnitPriceMinor: 2500, Currency: "INR"}
	f.Offer = order.Offer{SaleID: f.Purchase.SaleID, ProductID: f.Purchase.ProductID, Version: 1, UnitPriceMinor: 2500, Currency: "INR", Status: "ACTIVE", StartsAt: c.Now().Add(-time.Hour).Format(time.RFC3339), EndsAt: c.Now().Add(2 * time.Hour).Format(time.RFC3339), PerShopperUnitCap: 1}
	f.Store = order.Store{Pool: pool, Clock: c, Inventory: order.NewInventoryClient(conn), Offer: func(context.Context, string, string) (order.Offer, error) { return f.Offer, nil }}
	return f, nil
}
func (f *Fixture) Close() { f.close() }
func (f *Fixture) Script(ctx context.Context, shopper, key string, p order.Purchase, code inventoryv1.ResultCode) (string, error) {
	i, _, err := f.Store.Claim(ctx, shopper, key, clock.UUID(), clock.UUID(), p)
	if err != nil {
		return "", err
	}
	req := &inventoryv1.ReserveRequest{OrderId: i.OrderID, ShopperId: shopper, SaleId: p.SaleID, ProductId: p.ProductID, Quantity: uint32(p.Quantity), ConfigVersion: uint64(p.ExpectedOfferVersion)}
	result := &inventoryv1.OperationResult{Code: code}
	if code == inventoryv1.ResultCode_APPLIED {
		result.Reservation = &inventoryv1.Reservation{ReservationId: clock.UUID(), OrderId: i.OrderID, ShopperId: shopper, SaleId: p.SaleID, ProductId: p.ProductID, Quantity: uint32(p.Quantity), ConfigVersion: 1, State: inventoryv1.ReservationState_HELD, CreatedAt: timestamppb.New(f.Clock.Now()), ExpiresAt: timestamppb.New(f.Clock.Now().Add(120 * time.Second)), Version: 1}
	}
	f.Peer.Script(req, result)
	f.Clock.Advance(6 * time.Second)
	return i.OrderID, nil
}
func (f *Fixture) Submit(ctx context.Context, shopper, key string, p order.Purchase) (httpapi.Result, error) {
	return f.Store.Submit(ctx, shopper, key, clock.UUID(), clock.UUID(), p)
}
func count(ctx context.Context, p *pgxpool.Pool, kind string) (int, error) {
	var n int
	err := p.QueryRow(ctx, "SELECT count(*) FROM platform_outbox WHERE convert_from(envelope,'UTF8')::jsonb->>'type'=$1", kind).Scan(&n)
	return n, err
}
func expect(result httpapi.Result, status int, code string) error {
	if result.Status != status || code != "" && result.ErrorCode() != code {
		return fmt.Errorf("want %d/%s, got %d %s", status, code, result.Status, result.Body)
	}
	return nil
}
func serviceRequest(r *http.Request, name string) {
	u, _ := url.Parse("spiffe://flashkart/service/" + name)
	r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{URIs: []*url.URL{u}}}}}
}
func trusted(name string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serviceRequest(r, name); next.ServeHTTP(w, r) })
}
func response(handler http.Handler, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(string(body)))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}
func decoded(w *httptest.ResponseRecorder) httpapi.Result {
	return httpapi.Result{Status: w.Code, Body: w.Body.Bytes(), Header: w.Header()}
}

type hook func(context.Context, string) error

func (h hook) Hit(ctx context.Context, name string) error { return h(ctx, name) }
