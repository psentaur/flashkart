package market

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

func checkoutRPC(t *testing.T, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	register(server)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///"+listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestCheckoutWorkflow(t *testing.T) {
	if os.Getenv("FLASHKART_TEST_CHECKOUT") != "1" {
		t.Skip("run make test-workflows")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	inventory := Inventory{DB: purchasePool(t, "FLASHKART_INVENTORY_DSN")}
	inventoryClient := pb.NewInventoryClient(checkoutRPC(t, func(s *grpc.Server) { pb.RegisterInventoryServer(s, inventory) }))
	ordersDB := purchasePool(t, "FLASHKART_ORDERS_DSN")
	paymentDB := purchasePool(t, "FLASHKART_PAYMENT_DSN")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, fixture := range []string{"success", "decline", "lost-response", "delay"} {
		t.Run(fixture, func(t *testing.T) {
			payment := Payment{DB: paymentDB, Fixture: fixture, Delay: time.Hour}
			paymentClient := pb.NewPaymentClient(checkoutRPC(t, func(s *grpc.Server) { pb.RegisterPaymentServer(s, payment) }))
			orders := Orders{DB: ordersDB, Inventory: inventoryClient, Payment: paymentClient, Log: log}
			orderClient := pb.NewOrderClient(checkoutRPC(t, func(s *grpc.Server) { pb.RegisterOrderServer(s, orders) }))
			sale := purchaseSale(t, inventory, 1, false)
			id := rand.Text()
			work := PurchaseWork{1, "checkout-test", time.Now().UTC(), sale, id, "buyer"}
			if err := inventory.Reserve(ctx, work); err != nil {
				t.Fatal(err)
			}
			if _, err := inventory.DB.Exec(ctx, "UPDATE inventory.order_work SET next_attempt_at='-infinity' WHERE reservation_id=$1", id); err != nil {
				t.Fatal(err)
			}
			if sent, err := inventory.deliverNext(ctx, orderClient); err != nil || !sent {
				t.Fatalf("deliver order: %v %v", sent, err)
			}
			var raw []byte
			if err := inventory.DB.QueryRow(ctx, "SELECT body FROM inventory.order_work WHERE reservation_id=$1 AND delivered_at IS NOT NULL", id).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var request pb.CreateOrderRequest
			if err := protojson.Unmarshal(raw, &request); err != nil {
				t.Fatal(err)
			}
			if ref, err := orderClient.CreateFromReservation(ctx, &request); err != nil || ref.GetOrderId() != id {
				t.Fatalf("duplicate order: %v %v", ref, err)
			}
			run := func(o Orders) error {
				if _, err := o.DB.Exec(ctx, "UPDATE orders.purchases SET next_attempt_at='-infinity' WHERE order_id=$1", id); err != nil {
					t.Fatal(err)
				}
				processed, err := o.processNext(ctx)
				if !processed {
					t.Fatalf("order was not processed: %v", err)
				}
				return err
			}
			err := run(orders)
			switch fixture {
			case "lost-response":
				if status.Code(err) != codes.Unavailable {
					t.Fatalf("expected lost payment reply: %v", err)
				}
				recovered := Orders{DB: purchasePool(t, "FLASHKART_ORDERS_DSN"), Inventory: inventoryClient, Payment: paymentClient, Log: log}
				if err := run(recovered); err != nil {
					t.Fatal(err)
				}
			case "delay":
				if err != nil {
					t.Fatal(err)
				}
				if _, err := inventory.DB.Exec(ctx, "UPDATE inventory.reservations SET expires_at=clock_timestamp()-interval '1 second' WHERE reservation_id=$1", id); err != nil {
					t.Fatal(err)
				}
				if _, err := inventory.transition(ctx, id, "EXPIRED", pb.ReservationState_RESERVATION_STATE_EXPIRED); err != nil {
					t.Fatal(err)
				}
				if _, err := paymentDB.Exec(ctx, "UPDATE payment.transactions SET due_at=clock_timestamp() WHERE payment_id=$1", id); err != nil {
					t.Fatal(err)
				}
				if err := run(orders); err != nil {
					t.Fatal(err)
				}
				if _, err := paymentClient.Refund(ctx, &pb.PaymentRequest{PaymentId: id}); err != nil {
					t.Fatal(err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			wantOrder, wantPayment, wantReservation, wantStock := "CONFIRMED", "SUCCEEDED", pb.ReservationState_RESERVATION_STATE_COMMITTED, 0
			if fixture == "decline" {
				wantOrder, wantPayment, wantReservation, wantStock = "CANCELLED", "DECLINED", pb.ReservationState_RESERVATION_STATE_RELEASED, 1
			}
			if fixture == "delay" {
				wantOrder, wantPayment, wantReservation, wantStock = "CANCELLED", "REFUNDED", pb.ReservationState_RESERVATION_STATE_EXPIRED, 1
			}
			var state string
			var outcomes int
			if err := ordersDB.QueryRow(ctx, "SELECT state,(SELECT count(*) FROM orders.outcomes WHERE event_id=$2) FROM orders.purchases WHERE order_id=$1", id, "attempt.terminal:"+id).Scan(&state, &outcomes); err != nil || state != wantOrder || outcomes != 1 {
				t.Fatalf("order state=%s outcomes=%d err=%v", state, outcomes, err)
			}
			result, err := paymentClient.Query(ctx, &pb.PaymentRequest{PaymentId: id})
			if err != nil || result.GetState().String() != "PAYMENT_STATE_"+wantPayment {
				t.Fatalf("payment: %v %v", result, err)
			}
			reservation, err := inventoryClient.InspectReservation(ctx, &pb.ReservationRequest{ReservationId: id})
			if err != nil || reservation.GetState() != wantReservation {
				t.Fatalf("reservation: %v %v", reservation, err)
			}
			var stock int
			if err := inventory.DB.QueryRow(ctx, "SELECT available_stock FROM inventory.sales WHERE sale_id=$1", sale).Scan(&stock); err != nil || stock != wantStock {
				t.Fatalf("stock: %d %v", stock, err)
			}
			var paymentEvents int
			if err := paymentDB.QueryRow(ctx, "SELECT count(*) FROM payment.events WHERE body->>'payment_id'=$1", id).Scan(&paymentEvents); err != nil || paymentEvents != 1+btoi(fixture == "delay") {
				t.Fatalf("payment events: %d %v", paymentEvents, err)
			}
			for customer, want := range map[string]int{"buyer": 200, "other": 404} {
				handler := orders.Handler("customer-token", customer, http.NotFoundHandler())
				request := httptest.NewRequest("GET", "/api/orders/"+id, nil).WithContext(ctx)
				request.Header.Set("Authorization", "Bearer customer-token")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != want {
					t.Fatalf("order HTTP access for %s: %d", customer, response.Code)
				}
			}
		})
	}
}

func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestReservationExpiryRace(t *testing.T) {
	if os.Getenv("FLASHKART_TEST_CHECKOUT") != "1" {
		t.Skip("run make test-workflows")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	inventory := Inventory{DB: purchasePool(t, "FLASHKART_INVENTORY_DSN")}
	sale := purchaseSale(t, inventory, 20, false)
	for n := range 20 {
		id := rand.Text()
		if err := inventory.Reserve(ctx, PurchaseWork{1, "race-test", time.Now().UTC(), sale, id, "buyer"}); err != nil {
			t.Fatal(err)
		}
		if n%2 == 0 {
			if _, err := inventory.DB.Exec(ctx, "UPDATE inventory.reservations SET expires_at=clock_timestamp()-interval '1 second' WHERE reservation_id=$1", id); err != nil {
				t.Fatal(err)
			}
		}
		var group sync.WaitGroup
		var commit, expire error
		group.Go(func() { _, commit = inventory.CommitReservation(ctx, &pb.ReservationRequest{ReservationId: id}) })
		group.Go(func() {
			_, expire = inventory.transition(ctx, id, "EXPIRED", pb.ReservationState_RESERVATION_STATE_EXPIRED)
		})
		group.Wait()
		if (commit == nil) == (expire == nil) {
			t.Fatalf("expected a single winner: commit=%v expiry=%v", commit, expire)
		}
		loser := commit
		if loser == nil {
			loser = expire
		}
		if status.Code(loser) != codes.FailedPrecondition {
			t.Fatal(loser)
		}
	}
	var stock, sold, expired int
	err := inventory.DB.QueryRow(ctx, `SELECT available_stock,
 (SELECT count(*) FROM inventory.reservations WHERE sale_id=$1 AND state='COMMITTED'),
 (SELECT count(*) FROM inventory.reservations WHERE sale_id=$1 AND state='EXPIRED')
 FROM inventory.sales WHERE sale_id=$1`, sale).Scan(&stock, &sold, &expired)
	if err != nil || stock != 10 || sold != 10 || expired != 10 {
		t.Fatalf("stock=%d sold=%d expired=%d: %v", stock, sold, expired, err)
	}
}
