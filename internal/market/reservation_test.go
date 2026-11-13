package market

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type testPurchaseQueue struct {
	bodies  []string
	fail    bool
	deleted int
}

func (q *testPurchaseQueue) Send(_ context.Context, body string) error {
	q.bodies = append(q.bodies, body)
	if q.fail {
		return errors.New("lost send reply")
	}
	return nil
}
func (q *testPurchaseQueue) Receive(context.Context) ([]PurchaseMessage, error) { return nil, nil }
func (q *testPurchaseQueue) Delete(context.Context, string) error               { q.deleted++; return nil }

func purchasePool(t *testing.T, variable string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(variable)
	if dsn == "" {
		t.Fatalf("missing %s", variable)
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func purchaseSale(t *testing.T, inventory Inventory, stock uint32, ended bool) string {
	t.Helper()
	id := rand.Text()
	now := time.Now().UTC()
	end := now.Add(time.Hour)
	if ended {
		end = now.Add(-time.Minute)
	}
	call := &pb.Call{OperationId: id}
	terms := &pb.SaleTerms{SaleId: id, ProductId: "product", PriceMinor: 100, Currency: "INR", Stock: stock,
		StartsAt: now.Add(-time.Hour).Format(time.RFC3339Nano), EndsAt: end.Format(time.RFC3339Nano)}
	if _, err := inventory.PrepareSale(t.Context(), &pb.PrepareSaleRequest{Call: call, Terms: terms}); err != nil {
		t.Fatal(err)
	}
	if _, err := inventory.ActivateSale(t.Context(), &pb.ActivateSaleRequest{Call: call, SaleId: id}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestReservations(t *testing.T) {
	if os.Getenv("FLASHKART_TEST_PURCHASE") != "1" {
		t.Skip("run make test-reservations")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	admissionDB := purchasePool(t, "FLASHKART_ADMISSION_DSN")
	inventory := Inventory{DB: purchasePool(t, "FLASHKART_INVENTORY_DSN")}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	admission := NewAdmission(admissionDB, log, 10000, 10000, 32)
	handler := admission.Handler("customer-token", "customer", http.NotFoundHandler())
	sale := purchaseSale(t, inventory, 100, false)
	id := rand.Text()
	request := func(attempt, sale, token string) int {
		data, _ := json.Marshal(map[string]string{"attempt_id": attempt, "sale_id": sale})
		r := httptest.NewRequest("POST", "/api/attempts", bytes.NewReader(data)).WithContext(ctx)
		r.Header.Set("Idempotency-Key", attempt)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if request(id, sale, "wrong") != 401 || request(id, sale, "customer-token") != 202 ||
		request(id, sale, "customer-token") != 202 || request(id, "different", "customer-token") != 409 {
		t.Fatal("unexpected admission authentication or idempotency response")
	}

	var duplicates sync.WaitGroup
	responses := make(chan int, 16)
	for range 16 {
		duplicates.Go(func() { responses <- request(id, sale, "customer-token") })
	}
	duplicates.Wait()
	close(responses)
	for code := range responses {
		if code != 202 {
			t.Fatalf("concurrent duplicate returned %d", code)
		}
	}

	// A send may reach SQS before the worker receives a reply or records success.
	queue := &testPurchaseQueue{fail: true}
	if _, err := admissionDB.Exec(ctx, "UPDATE admission.purchase_outbox SET next_attempt_at='epoch' WHERE attempt_id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := admission.sendNext(ctx, queue); err == nil {
		t.Fatal("expected send failure")
	}
	var sent bool
	if err := admissionDB.QueryRow(ctx, "SELECT sent_at IS NOT NULL FROM admission.purchase_outbox WHERE attempt_id=$1", id).Scan(&sent); err != nil || sent {
		t.Fatalf("failed send was marked complete: %v", err)
	}
	if _, err := admissionDB.Exec(ctx, "UPDATE admission.purchase_outbox SET next_attempt_at='epoch' WHERE attempt_id=$1", id); err != nil {
		t.Fatal(err)
	}
	restarted := NewAdmission(admissionDB, log, 10000, 10000, 32)
	queue.fail = false
	if delivered, err := restarted.sendNext(ctx, queue); err != nil || !delivered || len(queue.bodies) != 2 || queue.bodies[0] != queue.bodies[1] {
		t.Fatalf("outbox did not replay the same work: %v %v", delivered, err)
	}
	for _, body := range queue.bodies {
		if err := inventory.processMessage(ctx, queue, PurchaseMessage{body, "receipt"}); err != nil {
			t.Fatal(err)
		}
	}
	if queue.deleted != 2 {
		t.Fatal("committed messages were not acknowledged")
	}

	work := PurchaseWork{1, "trace", time.Now().UTC(), sale, id, "customer"}
	var wg sync.WaitGroup
	errs := make(chan error, 1020)
	for n := range 1019 {
		wg.Go(func() {
			copy := work
			if n < 999 {
				copy.AttemptID = fmt.Sprintf("%s-%d", id, n)
			}
			if err := inventory.Reserve(ctx, copy); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var available, held, sold, rejected, orders int
	counts := func() {
		t.Helper()
		err := inventory.DB.QueryRow(ctx, `SELECT available_stock,
			(SELECT count(*) FROM inventory.reservations WHERE sale_id=$1 AND state='HELD'),
			(SELECT count(*) FROM inventory.reservations WHERE sale_id=$1 AND state='COMMITTED'),
			(SELECT count(*) FROM inventory.attempts WHERE sale_id=$1 AND rejection='SOLD_OUT'),
			(SELECT count(*) FROM inventory.order_work w JOIN inventory.reservations r USING(reservation_id) WHERE r.sale_id=$1)
			FROM inventory.sales WHERE sale_id=$1`, sale).Scan(&available, &held, &sold, &rejected, &orders)
		if err != nil {
			t.Fatal(err)
		}
		if available+held+sold != 100 || orders != 100 || rejected != 900 {
			t.Fatalf("stock invariant failed: available=%d held=%d sold=%d rejected=%d orders=%d", available, held, sold, rejected, orders)
		}
	}
	counts()
	if held != 100 {
		t.Fatalf("expected exactly 100 held, got %d", held)
	}
	call := &pb.ReservationRequest{ReservationId: id}
	for range 2 {
		if _, err := inventory.CommitReservation(ctx, call); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := inventory.ReleaseReservation(ctx, call); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("release after commit: %v", err)
	}
	counts()
	if sold != 1 {
		t.Fatal("commit was not idempotent")
	}
	var releaseID string
	if err := inventory.DB.QueryRow(ctx, "SELECT reservation_id FROM inventory.reservations WHERE sale_id=$1 AND state='HELD' LIMIT 1", sale).Scan(&releaseID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := inventory.ReleaseReservation(ctx, &pb.ReservationRequest{ReservationId: releaseID}); err != nil {
			t.Fatal(err)
		}
	}
	counts()
	if available != 1 {
		t.Fatal("release returned more than one unit")
	}
	for reason, saleID := range map[string]string{"ENDED": purchaseSale(t, inventory, 1, true), "INACTIVE": rand.Text()} {
		rejectedWork := work
		rejectedWork.AttemptID, rejectedWork.SaleID = rand.Text(), saleID
		for range 2 {
			if err := inventory.Reserve(ctx, rejectedWork); err != nil {
				t.Fatal(err)
			}
		}
		var got string
		if err := inventory.DB.QueryRow(ctx, "SELECT rejection FROM inventory.attempts WHERE attempt_id=$1", rejectedWork.AttemptID).Scan(&got); err != nil || got != reason {
			t.Fatalf("expected %s, got %s %v", reason, got, err)
		}
	}
	work.CustomerID = "another"
	if err := inventory.Reserve(ctx, work); !errors.Is(err, errAttemptConflict) {
		t.Fatalf("conflicting queue replay: %v", err)
	}

	limited := NewAdmission(admissionDB, log, 0, 1, 1)
	handler = limited.Handler("customer-token", "customer", http.NotFoundHandler())
	accepted, throttled := rand.Text(), rand.Text()
	if request(accepted, sale, "customer-token") != 202 || request(throttled, sale, "customer-token") != 429 || request(accepted, sale, "customer-token") != 202 {
		t.Fatal("token bucket or replay behavior was incorrect")
	}
	var pending int
	if err := admissionDB.QueryRow(ctx, "SELECT count(*) FROM admission.purchase_outbox WHERE attempt_id=$1", throttled).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("throttled request entered the queue")
	}
}

func TestPurchaseQueue(t *testing.T) {
	url := os.Getenv("FLASHKART_TEST_QUEUE_URL")
	if url == "" {
		t.Skip("set emulator fixture to test real SQS delivery")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	q, err := NewPurchaseQueue(ctx, url, "us-east-1", os.Getenv("FLASHKART_TEST_QUEUE_ENDPOINT"),
		os.Getenv("FLASHKART_TEST_QUEUE_CREDENTIALS"), os.Getenv("FLASHKART_TEST_QUEUE_CA"), true)
	if err != nil {
		t.Fatal(err)
	}
	inventory := Inventory{DB: purchasePool(t, "FLASHKART_INVENTORY_DSN")}
	admission := NewAdmission(purchasePool(t, "FLASHKART_ADMISSION_DSN"), slog.New(slog.NewTextHandler(io.Discard, nil)), 100, 100, 1)
	sale := purchaseSale(t, inventory, 1, false)
	work := PurchaseWork{1, "sqs-trace", time.Now().UTC(), sale, rand.Text(), "customer"}
	if err := admission.accept(ctx, work); err != nil {
		t.Fatal(err)
	}
	if _, err := admission.DB.Exec(ctx, "UPDATE admission.purchase_outbox SET next_attempt_at='epoch' WHERE attempt_id=$1", work.AttemptID); err != nil {
		t.Fatal(err)
	}
	if sent, err := admission.sendNext(ctx, q); err != nil || !sent {
		t.Fatalf("SQS outbox send: %v %v", sent, err)
	}
	body, err := json.Marshal(work)
	if err != nil {
		t.Fatal(err)
	}
	for delivery := range 2 {
		if delivery == 1 {
			if err := q.Send(ctx, string(body)); err != nil {
				t.Fatal(err)
			}
		}
		messages, err := q.Receive(ctx)
		if err != nil || len(messages) != 1 {
			t.Fatalf("SQS receive: %d %v", len(messages), err)
		}
		if err := inventory.processMessage(ctx, q, messages[0]); err != nil {
			t.Fatal(err)
		}
	}
	var held int
	if err := inventory.DB.QueryRow(ctx, "SELECT count(*) FROM inventory.reservations WHERE sale_id=$1", sale).Scan(&held); err != nil || held != 1 {
		t.Fatalf("duplicate SQS delivery changed reservations: %d %v", held, err)
	}
}
