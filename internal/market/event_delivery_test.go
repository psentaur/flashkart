package market

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

func TestBatchFailures(t *testing.T) {
	var handled []string
	result, err := handleBatch(t.Context(), []byte(`{"Records":[{"messageId":"ok","body":"one"},{"messageId":"bad","body":"two"},{"messageId":"later","body":"three"}]}`), func(_ context.Context, body string) error {
		handled = append(handled, body)
		if body == "two" {
			return errors.New("failed")
		}
		return nil
	})
	if err != nil || len(result.Failures) != 1 || result.Failures[0].ID != "bad" || len(handled) != 3 {
		t.Fatalf("unexpected partial response: %v %v %v", result, err, handled)
	}
}

func TestEventDelivery(t *testing.T) {
	if os.Getenv("FLASHKART_TEST_EVENTS") != "1" {
		t.Skip("run make test-events-notifications")
	}
	ctx := t.Context()
	db := purchasePool(t, "FLASHKART_INVENTORY_DSN")
	id := rand.Text()
	work := PurchaseWork{1, "trace", time.Now().UTC(), "sale", id, "customer"}
	event := purchaseEvent(work, "attempt.rejected", "", map[string]any{"reason": "SOLD_OUT"})
	outcome := terminalEvent(work, "REJECTED", "SOLD_OUT", "")
	for source, body := range map[string]map[string]any{"inventory.events": event, "inventory.outcomes": outcome} {
		if _, err := db.Exec(ctx, "INSERT INTO "+source+"(event_id,body,next_attempt_at) VALUES ($1,$2::jsonb,'-infinity')", body["event_id"], body); err != nil {
			t.Fatal(err)
		}
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var sends []string
	failed := true
	kafka, _ := NewEventDelivery(db, "inventory.events", func(_ context.Context, key, body string) error {
		if key != work.SaleID {
			t.Fatalf("incorrect event key %s", key)
		}
		sends = append(sends, body)
		if failed {
			return errors.New("publish reply lost")
		}
		return nil
	}, log)
	queue, _ := NewEventDelivery(db, "inventory.outcomes", func(_ context.Context, _, body string) error {
		if !strings.Contains(body, "attempt.terminal:"+id) {
			t.Fatal("unexpected outcome")
		}
		return nil
	}, log)
	if sent, err := kafka.next(ctx); !sent || err == nil {
		t.Fatal("Kafka failure was not recorded")
	}
	if sent, err := queue.next(ctx); !sent || err != nil {
		t.Fatalf("independent outcome delivery: %v %v", sent, err)
	}
	if _, err := db.Exec(ctx, "UPDATE inventory.events SET next_attempt_at='-infinity',delivery_attempts=9 WHERE event_id=$1", event["event_id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := kafka.next(ctx); err == nil {
		t.Fatal("expected dead letter failure")
	}
	var dead bool
	if err := db.QueryRow(ctx, "SELECT dead_at IS NOT NULL AND delivered_at IS NULL FROM inventory.events WHERE event_id=$1", event["event_id"]).Scan(&dead); err != nil || !dead {
		t.Fatal("failed event was not retained in dead state")
	}
	if err := kafka.Replay(ctx, event["event_id"].(string)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "UPDATE inventory.events SET next_attempt_at='-infinity' WHERE event_id=$1", event["event_id"]); err != nil {
		t.Fatal(err)
	}
	failed = false
	if sent, err := kafka.next(ctx); !sent || err != nil {
		t.Fatalf("replay: %v %v", sent, err)
	}
	if len(sends) != 3 || sends[0] != sends[2] {
		t.Fatal("replay changed the event")
	}
}

func TestNotificationAndKafka(t *testing.T) {
	table := os.Getenv("FLASHKART_TEST_NOTIFICATION_TABLE")
	if table == "" {
		t.Skip("requires local DynamoDB, outcome queue and Kafka fixtures")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	endpoint, credentials, ca := os.Getenv("FLASHKART_TEST_QUEUE_ENDPOINT"), os.Getenv("FLASHKART_TEST_QUEUE_CREDENTIALS"), os.Getenv("FLASHKART_TEST_QUEUE_CA")
	store, err := NewNotifications(ctx, table, "us-east-1", endpoint, credentials, ca, true)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := NewPurchaseQueue(ctx, os.Getenv("FLASHKART_TEST_QUEUE_URL"), "us-east-1", endpoint, credentials, ca, true)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	admission := NewAdmission(purchasePool(t, "FLASHKART_ADMISSION_DSN"), log, 100, 100, 4)
	admission.Notifications = store
	inventory := Inventory{DB: purchasePool(t, "FLASHKART_INVENTORY_DSN")}
	customer := rand.Text()
	sale := purchaseSale(t, inventory, 1, false)
	work := PurchaseWork{1, "trace", time.Now().UTC(), sale, rand.Text(), customer}
	if err := inventory.Reserve(ctx, PurchaseWork{1, "trace", time.Now().UTC(), sale, rand.Text(), customer}); err != nil {
		t.Fatal(err)
	}
	if _, err := admission.DB.Exec(ctx, "INSERT INTO admission.attempts(attempt_id,sale_id,customer_id) VALUES ($1,$2,$3)", work.AttemptID, sale, customer); err != nil {
		t.Fatal(err)
	}
	get := func(handler http.Handler, path string, want int, dest any) {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("%s returned %d: %s", path, response.Code, response.Body.String())
		}
		if dest != nil {
			if err := json.NewDecoder(response.Body).Decode(dest); err != nil {
				t.Fatal(err)
			}
		}
	}
	path := "/api/attempts/" + work.AttemptID
	handler := admission.Handler("token", customer, http.NotFoundHandler())
	var attempt AttemptView
	get(handler, path, 200, &attempt)
	if attempt.State != "QUEUED" {
		t.Fatalf("attempt was not queued: %+v", attempt)
	}
	get(admission.Handler("token", "other", http.NotFoundHandler()), path, 404, nil)

	if err := inventory.Reserve(ctx, work); err != nil {
		t.Fatal(err)
	}
	prioritize := func(source, id string) {
		t.Helper()
		if _, err := inventory.DB.Exec(ctx, "UPDATE "+source+" SET next_attempt_at='-infinity' WHERE event_id=$1", id); err != nil {
			t.Fatal(err)
		}
	}
	prioritize("inventory.outcomes", "attempt.terminal:"+work.AttemptID)
	delivery, _ := NewEventDelivery(inventory.DB, "inventory.outcomes", func(ctx context.Context, _, body string) error { return queue.Send(ctx, body) }, log)
	if sent, err := delivery.next(ctx); !sent || err != nil {
		t.Fatalf("outcome delivery: %v %v", sent, err)
	}
	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		store.Run(workerCtx, queue, func() {})
	}()
	defer func() { stop(); <-done }()
	wait := func(id, state, reason string) {
		t.Helper()
		for ctx.Err() == nil {
			got, err := store.Get(ctx, customer, id)
			if err != nil {
				t.Fatal(err)
			}
			if got != nil {
				if got.Attempt.State != state || got.Attempt.Reason != reason {
					t.Fatalf("unexpected terminal outcome: %+v", got)
				}
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal(ctx.Err())
	}
	wait(work.AttemptID, "REJECTED", "SOLD_OUT")
	// Replay and a conflicting older outcome both preserve the first terminal view.
	for _, state := range []string{"REJECTED", "CONFIRMED"} {
		body, err := json.Marshal(terminalEvent(work, state, "", ""))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Handle(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	get(handler, path, 200, &attempt)
	if attempt.State != "REJECTED" || attempt.Reason != "SOLD_OUT" {
		t.Fatalf("attempt did not converge: %+v", attempt)
	}
	for _, outcome := range []struct{ state, reason string }{
		{"CONFIRMED", ""}, {"CANCELLED", "PAYMENT_DECLINED"}, {"CANCELLED", "RESERVATION_EXPIRED"},
	} {
		fixture := work
		fixture.AttemptID = rand.Text()
		body, err := json.Marshal(terminalEvent(fixture, outcome.state, outcome.reason, fixture.AttemptID))
		if err != nil {
			t.Fatal(err)
		}
		if err := queue.Send(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
		wait(fixture.AttemptID, outcome.state, outcome.reason)
	}
	var values []Notification
	get(store.Handler("token", customer, http.NotFoundHandler()), "/api/notifications", 200, &values)
	if len(values) != 4 {
		t.Fatalf("incorrect notification count: %v", values)
	}
	brokers := []string{os.Getenv("FLASHKART_TEST_KAFKA")}
	producer, err := NewEventProducer(brokers, "plaintext", "")
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	eventID := "attempt.rejected:" + work.AttemptID
	prioritize("inventory.events", eventID)
	var event string
	if err := inventory.DB.QueryRow(ctx, "SELECT body::text FROM inventory.events WHERE event_id=$1", eventID).Scan(&event); err != nil {
		t.Fatal(err)
	}
	var record *kgo.Record
	kafka, _ := NewEventDelivery(inventory.DB, "inventory.events", func(ctx context.Context, key, body string) error {
		var err error
		record, err = producer.ProduceSync(ctx, &kgo.Record{Key: []byte(key), Value: []byte(body)}).First()
		return err
	}, log)
	if sent, err := kafka.next(ctx); !sent || err != nil {
		t.Fatalf("Kafka delivery: %v %v", sent, err)
	}
	consumer, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{"purchase-events": {record.Partition: kgo.NewOffset().At(record.Offset)}}))
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	fetch := consumer.PollRecords(ctx, 1)
	if errs := fetch.Errors(); len(errs) > 0 {
		t.Fatal(errs)
	}
	found := fetch.Records()
	if len(found) != 1 || string(found[0].Value) != event || string(found[0].Key) != work.SaleID {
		t.Fatal("Kafka record did not match the published event")
	}
}
