package us2

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"flash-sale/contracts/gen/inventory/v1"
	"flash-sale/gateway"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/httpapi"
	"flash-sale/internal/platform/telemetry"
	"flash-sale/internal/platform/value"
	admission "flash-sale/services/admission"
	order "flash-sale/services/order"
	"flash-sale/tests/doubles"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"net"
	"net/http"
	"net/http/httptest"
	"time"
)

func identity() (auth.Verifier, func(string, string) string, error) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return auth.Verifier{}, nil, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	v := auth.Verifier{Key: &k.PublicKey, Issuer: "us2", Audience: "flashkart"}
	token := func(subject, role string) string {
		s, _ := jwt.NewWithClaims(jwt.SigningMethodRS256, auth.Claims{Role: role, RegisteredClaims: jwt.RegisteredClaims{Subject: subject, Issuer: "us2", Audience: jwt.ClaimStrings{"flashkart"}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute))}}).SignedString(k)
		return s
	}
	return v, token, nil
}
func excluded(m *telemetry.Metrics) map[string]float64 {
	out := map[string]float64{}
	families, _ := m.Registry.Gather()
	for _, f := range families {
		if f.GetName() == "purchase_excluded_submissions_total" {
			for _, metric := range f.Metric {
				for _, label := range metric.Label {
					if label.GetName() == "reason" {
						out[label.GetValue()] = metric.GetCounter().GetValue()
					}
				}
			}
		}
	}
	return out
}
func Transport() error {
	v, token, err := identity()
	if err != nil {
		return err
	}
	shopper := clock.UUID()
	p := httpapi.Purchase{SaleID: clock.UUID(), ProductID: clock.UUID(), Quantity: 1, ExpectedOfferVersion: 1, ExpectedUnitPriceMinor: 2500, Currency: "INR"}
	body, _ := json.Marshal(p)
	metrics := telemetry.New()
	calls := 0
	upstream := httptest.NewServer(httpapi.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Shopper-ID") != shopper || r.Header.Get("X-Role") != "SHOPPER" || value.UUID(r.Header.Get("X-Attempt-ID")) != nil || r.Header.Get("X-Attempt-ID") == "forged" {
			httpapi.WriteError(w, r, "INVALID_ARGUMENT", "Forged identity reached upstream", nil)
			return
		}
		httpapi.WriteError(w, r, "PURCHASE_LIMIT_EXCEEDED", "cap", nil)
	})))
	defer upstream.Close()
	h := gateway.Handler(gateway.Config{Verifier: v, AdmissionURL: upstream.URL, Metrics: metrics})
	for _, tc := range []struct {
		token, key, body string
		status           int
	}{{"", "key", string(body), 401}, {token(shopper, "OPERATOR"), "key", string(body), 403}, {token(shopper, "SHOPPER"), "", string(body), 400}, {token(shopper, "SHOPPER"), "key", "{}", 400}} {
		w := response(h, "POST", "/v1/purchases", []byte(tc.body), map[string]string{"Authorization": "Bearer " + tc.token, "Idempotency-Key": tc.key, "Content-Type": "application/json", "X-Shopper-ID": shopper, "X-Role": "SHOPPER"})
		if w.Code != tc.status {
			return fmt.Errorf("gateway validation %d %s", w.Code, w.Body.String())
		}
	}
	if calls != 0 {
		return errors.New("excluded traffic reached Admission")
	}
	for range 2 {
		w := response(h, "POST", "/v1/purchases", body, map[string]string{"Authorization": "Bearer " + token(shopper, "SHOPPER"), "Idempotency-Key": "same-key", "Content-Type": "application/json", "X-Shopper-ID": clock.UUID(), "X-Attempt-ID": "forged"})
		if err = expect(decoded(w), 409, "PURCHASE_LIMIT_EXCEEDED"); err != nil {
			return err
		}
		if w.Header().Get("Retry-After") != "" {
			return errors.New("cap rejection has Retry-After")
		}
	}
	ex := excluded(metrics)
	if ex["unauthorized"] != 2 || ex["malformed"] != 2 || len(ex) != 2 {
		return fmt.Errorf("ingress exclusion counts %v", ex)
	}
	// Service headers without a verified certificate cannot invoke the internal Order route.
	h = order.Handler(order.Store{}, v)
	w := response(h, "POST", "/internal/orders", []byte(`{}`), map[string]string{"X-Service-ID": "admission", "X-Shopper-ID": shopper, "X-Role": "SHOPPER"})
	if err = expect(decoded(w), 401, "UNAUTHENTICATED"); err != nil {
		return err
	}
	h = trusted("admission", h)
	w = response(h, "POST", "/internal/orders", []byte(`{}`), map[string]string{"Idempotency-Key": "key"})
	if err = expect(decoded(w), 400, "INVALID_ARGUMENT"); err != nil {
		return err
	}
	// Admission also rejects unauthorized/malformed traffic before SQL or an attempt exists.
	am := telemetry.New()
	a := admission.Handler(admission.Config{Verifier: v, Metrics: am})
	w = response(a, "POST", "/v1/purchases", body, map[string]string{"X-Service-ID": "gateway"})
	if err = expect(decoded(w), 401, "UNAUTHENTICATED"); err != nil {
		return err
	}
	w = response(trusted("gateway", a), "POST", "/v1/purchases", []byte(`{}`), map[string]string{"X-Shopper-ID": shopper, "X-Role": "SHOPPER", "Content-Type": "application/json", "Idempotency-Key": "key"})
	return expect(decoded(w), 400, "INVALID_ARGUMENT")
}
func InventoryContract(ctx context.Context) error {
	peer := doubles.NewInventory()
	now := time.Now().UTC()
	req := &inventoryv1.ReserveRequest{OrderId: clock.UUID(), ShopperId: clock.UUID(), SaleId: clock.UUID(), ProductId: clock.UUID(), Quantity: 2, ConfigVersion: 1}
	held := &inventoryv1.Reservation{OrderId: req.OrderId, ShopperId: req.ShopperId, SaleId: req.SaleId, ProductId: req.ProductId, Quantity: 2, ConfigVersion: 1, ReservationId: clock.UUID(), State: inventoryv1.ReservationState_HELD, CreatedAt: timestamppb.New(now), ExpiresAt: timestamppb.New(now.Add(120 * time.Second)), Version: 1}
	peer.Script(req, &inventoryv1.OperationResult{Code: inventoryv1.ResultCode_APPLIED, Reservation: held})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	deadline := make(chan time.Duration, 8)
	server := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		if len(md.Get("x-correlation-id")) != 1 || value.UUID(md.Get("x-correlation-id")[0]) != nil {
			return nil, errors.New("missing correlation")
		}
		if d, ok := ctx.Deadline(); ok {
			deadline <- time.Until(d)
		}
		return handler(ctx, req)
	}))
	inventoryv1.RegisterInventoryReservationServer(server, peer)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	client := order.NewInventoryClient(conn)
	ctx, cancel := context.WithTimeout(ctx, 700*time.Millisecond)
	defer cancel()
	for _, state := range []inventoryv1.ReservationState{inventoryv1.ReservationState_HELD, inventoryv1.ReservationState_CONFIRMED, inventoryv1.ReservationState_EXPIRED, inventoryv1.ReservationState_RELEASED} {
		if err = peer.SetTerminal(req.OrderId, state); err != nil {
			return err
		}
		r, e := client.Reserve(ctx, req)
		if e != nil {
			return e
		}
		if r.Reservation.State != state {
			return errors.New("Reserve replay returned stale state")
		}
	}
	rejected := proto.Clone(req).(*inventoryv1.ReserveRequest)
	rejected.OrderId = clock.UUID()
	peer.Script(rejected, &inventoryv1.OperationResult{Code: inventoryv1.ResultCode_PURCHASE_LIMIT_EXCEEDED})
	for range 2 {
		r, e := client.Reserve(ctx, rejected)
		if e != nil {
			return e
		}
		if r.Code != inventoryv1.ResultCode_PURCHASE_LIMIT_EXCEEDED || r.Reservation != nil {
			return errors.New("rejected Reserve changed")
		}
	}
	for range 6 {
		if remaining := <-deadline; remaining > 700*time.Millisecond {
			return errors.New("gRPC stacked a new timeout")
		}
	}
	// A changed request remains a payload mismatch, not another reservation.
	changed := proto.Clone(req).(*inventoryv1.ReserveRequest)
	changed.Quantity = 1
	r, e := client.Reserve(ctx, changed)
	if e != nil {
		return e
	}
	if r.Code != inventoryv1.ResultCode_PAYLOAD_MISMATCH {
		return errors.New("Reserve payload mismatch not preserved")
	}
	return nil
}
