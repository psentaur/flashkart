package us2

import (
	"context"
	"encoding/json"
	"errors"
	"flash-sale/contracts/gen/inventory/v1"
	"flash-sale/gateway"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/messaging"
	admission "flash-sale/services/admission"
	order "flash-sale/services/order"
	"flash-sale/tests/doubles"
	"fmt"
	"github.com/IBM/sarama"
	"net/http/httptest"
	"os"
	"strings"
	"time"
)

// Journey crosses the public gateway, real owner SQL, gRPC and real Kafka.
// HTTP service identity is a controlled test boundary; live mTLS is checked separately.
func Journey(ctx context.Context) error {
	f, err := New(ctx)
	if err != nil {
		return err
	}
	defer f.Close()
	pool, cleanup, err := database(ctx, "admission")
	if err != nil {
		return err
	}
	defer cleanup()
	if err = admission.Migrate(ctx, pool); err != nil {
		return err
	}
	a := admission.Store{Pool: pool, Clock: f.Clock}
	config := events.SaleConfigured{SaleID: f.Purchase.SaleID, ProductID: f.Purchase.ProductID, Version: 1, UnitPriceMinor: 2500, Currency: "INR", AllocatedUnits: 100, PerShopperUnitCap: 1, StartsAt: f.Offer.StartsAt, EndsAt: f.Offer.EndsAt, Status: "ACTIVE", ReadyParticipants: []string{}}
	config.AdmissionLimits.ShopperPerSecond = 100
	config.AdmissionLimits.SalePerSecond = 100
	config.AdmissionLimits.MaxInFlight = 50
	data, _ := json.Marshal(config)
	for _, kind := range []string{"SaleConfigured", "SaleActivated"} {
		e := events.Envelope{EventID: clock.UUID(), SchemaVersion: 1, Type: kind, OccurredAt: f.Clock.Now().Format(time.RFC3339Nano), Producer: "flash-sale", AggregateID: config.SaleID, AggregateVersion: 1, CorrelationID: clock.UUID(), Data: data}
		raw, e2 := events.Encode(e)
		if e2 != nil {
			return e2
		}
		if e2 = a.Consume(ctx, "sale.config.v1", config.SaleID, raw); e2 != nil {
			return e2
		}
	}
	v, token, err := identity()
	if err != nil {
		return err
	}
	orders := httptest.NewServer(trusted("admission", order.Handler(f.Store, v)))
	defer orders.Close()
	gate := httptest.NewServer(trusted("gateway", admission.Handler(admission.Config{Store: a, Verifier: v, OrderURL: orders.URL})))
	defer gate.Close()
	edge := gateway.Handler(gateway.Config{Verifier: v, AdmissionURL: gate.URL, OrderURL: orders.URL})
	shopper, key := clock.UUID(), "journey-key"
	id, err := f.Script(ctx, shopper, key, f.Purchase, inventoryv1.ResultCode_APPLIED)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(f.Purchase)
	headers := map[string]string{"Authorization": "Bearer " + token(shopper, "SHOPPER"), "Idempotency-Key": key, "Content-Type": "application/json"}
	for range 2 {
		w := response(edge, "POST", "/v1/purchases", body, headers)
		if err = expect(decoded(w), 202, ""); err != nil {
			return err
		}
		if w.Header().Get("Location") != "/v1/orders/"+id || w.Header().Get("X-Attempt-ID") == "" {
			return errors.New("missing accepted Location/attempt identity")
		}
	}
	for _, subject := range []string{shopper, clock.UUID()} {
		w := response(edge, "GET", "/v1/orders/"+id, nil, map[string]string{"Authorization": "Bearer " + token(subject, "SHOPPER")})
		want := 200
		if subject != shopper {
			want = 404
		}
		if w.Code != want {
			return fmt.Errorf("owner-filtered order got %d", w.Code)
		}
	}
	if err = settleKafka(ctx, f, id); err != nil {
		return err
	}
	w := response(edge, "POST", "/v1/purchases", body, headers)
	if err = expect(decoded(w), 200, ""); err != nil {
		return err
	}
	var o order.Order
	_ = json.Unmarshal(w.Body.Bytes(), &o)
	if o.OrderID != id || o.State != "CONFIRMED" {
		return fmt.Errorf("public terminal replay %+v", o)
	}
	return nil
}
func settleKafka(ctx context.Context, f *Fixture, id string) error {
	brokers := strings.Split(os.Getenv("KAFKA_BROKERS"), ",")
	admin, err := sarama.NewClusterAdmin(brokers, messaging.Config("us2-journey"))
	if err != nil {
		return err
	}
	defer admin.Close()
	topic := "us2-journey-" + clock.UUID()
	if err = admin.CreateTopic(topic, &sarama.TopicDetail{NumPartitions: 1, ReplicationFactor: 1}, false); err != nil {
		return err
	}
	defer func() { _ = admin.DeleteTopic(topic) }()
	producer, err := messaging.Producer(brokers, "us2-journey")
	if err != nil {
		return err
	}
	defer producer.Close()
	consumer, err := sarama.NewConsumer(brokers, messaging.Config("us2-journey"))
	if err != nil {
		return err
	}
	defer consumer.Close()
	partition, err := consumer.ConsumePartition(topic, 0, sarama.OffsetOldest)
	if err != nil {
		return err
	}
	defer partition.Close()
	for range 10 {
		sent, e := f.Store.Outbox().DispatchOne(ctx, f.Clock.Now().Add(time.Second), func(_ context.Context, _, key string, raw []byte) error {
			_, _, e := producer.SendMessage(&sarama.ProducerMessage{Topic: topic, Key: sarama.StringEncoder(key), Value: sarama.ByteEncoder(raw)})
			return e
		})
		if e != nil {
			return e
		}
		if !sent {
			break
		}
	}
	payment := doubles.NewPayment()
	scripted := false
	for {
		select {
		case m := <-partition.Messages():
			var e events.Envelope
			if err = json.Unmarshal(m.Value, &e); err != nil {
				return err
			}
			switch e.Type {
			case "PaymentRequested":
				if scripted {
					continue
				}
				scripted = true
				var data map[string]any
				_ = json.Unmarshal(e.Data, &data)
				data["state"] = "SUCCEEDED"
				data["version"] = 1
				b, _ := json.Marshal(data)
				result := events.Envelope{EventID: clock.UUID(), SchemaVersion: 1, Type: "PaymentSucceeded", OccurredAt: f.Clock.Now().Format(time.RFC3339Nano), Producer: "payment", AggregateID: e.AggregateID, AggregateVersion: 1, CorrelationID: e.CorrelationID, Data: b}
				if err = payment.Script(e, result); err != nil {
					return err
				}
				result, err = payment.Deliver(ctx, e)
				if err != nil {
					return err
				}
				if err = f.Peer.SetTerminal(id, inventoryv1.ReservationState_CONFIRMED); err != nil {
					return err
				}
				raw, err := events.Encode(result)
				if err != nil {
					return err
				}
				_, key := events.Destination(result)
				if _, _, err = producer.SendMessage(&sarama.ProducerMessage{Topic: topic, Key: sarama.StringEncoder(key), Value: sarama.ByteEncoder(raw)}); err != nil {
					return err
				}
			case "PaymentSucceeded":
				if err = f.Store.Consume(ctx, "payment.events.v1", string(m.Key), m.Value); err != nil {
					return err
				}
				if err = f.Store.Consume(ctx, "payment.events.v1", string(m.Key), m.Value); err != nil {
					return err
				}
				if n, e := count(ctx, f.Pool, "PaymentRequested"); e != nil || n != 1 {
					return fmt.Errorf("payment effects %d %v", n, e)
				}
				return nil
			}
		case e := <-partition.Errors():
			return e
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
