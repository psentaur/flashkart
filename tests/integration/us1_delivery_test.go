//go:build integration

package integration

import (
	"context"
	"errors"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/eventstore"
	"flash-sale/internal/platform/fault"
	"flash-sale/internal/platform/messaging"
	"flash-sale/tests/us1"
	"github.com/IBM/sarama"
	"github.com/jackc/pgx/v5"
	"os"
	"strings"
	"testing"
	"time"
)

func TestUS1RealOutboxPoisonAndDurableAcknowledgement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f, e := us1.New(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if _, e = f.Store.Put(ctx, f.Write(clock.UUID(), 0), clock.UUID(), clock.UUID(), true); e != nil {
		t.Fatal(e)
	}
	brokers := strings.Split(os.Getenv("KAFKA_BROKERS"), ",")
	admin, e := sarama.NewClusterAdmin(brokers, messaging.Config("us1-delivery"))
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	topic := "us1-delivery-" + clock.UUID()
	if e = admin.CreateTopic(topic, &sarama.TopicDetail{NumPartitions: 1, ReplicationFactor: 1}, false); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := admin.DeleteTopic(topic); e != nil {
			t.Error(e)
		}
	}()
	producer, e := messaging.Producer(brokers, "us1-delivery")
	if e != nil {
		t.Fatal(e)
	}
	defer producer.Close()
	consumer, e := sarama.NewConsumer(brokers, messaging.Config("us1-delivery"))
	if e != nil {
		t.Fatal(e)
	}
	defer consumer.Close()
	partition, e := consumer.ConsumePartition(topic, 0, sarama.OffsetOldest)
	if e != nil {
		t.Fatal(e)
	}
	defer partition.Close()
	// A keyless tombstone must quarantine durably, allowing the next key to progress.
	if _, _, e = producer.SendMessage(&sarama.ProducerMessage{Topic: topic}); e != nil {
		t.Fatal(e)
	}
	store := f.Store.Outbox()
	crash := fault.New()
	crash.Set("after_publish", errors.New("receipt interrupted"), nil)
	store.Fault = crash
	publish := func(ctx context.Context, dest, key string, raw []byte) error {
		if dest == "analytics.activity.v1" {
			return errors.New("analytics unavailable")
		}
		_, _, e := producer.SendMessage(&sarama.ProducerMessage{Topic: topic, Key: sarama.StringEncoder(key), Value: sarama.ByteEncoder(raw)})
		return e
	}
	now := f.Clock.Now().Add(time.Second)
	// Analytics fails independently, then the domain publication loses its receipt.
	if _, e = store.DispatchOne(ctx, now, publish); e == nil {
		t.Fatal("analytics failure hidden")
	}
	if _, e = store.DispatchOne(ctx, now, publish); e == nil {
		t.Fatal("post-publication crash missing")
	}
	store.Fault = nil
	if sent, e := store.DispatchOne(ctx, now, publish); e != nil || !sent {
		t.Fatal(sent, e)
	}
	if _, e = f.Pool.Exec(ctx, "UPDATE platform_outbox SET created_at=$1", now.Add(-9*24*time.Hour)); e != nil {
		t.Fatal(e)
	}
	if n, e := store.Prune(ctx, now); e != nil || n != 0 {
		t.Fatal("outstanding destination pruned", n, e)
	}
	// Consume the actual log records. The inbox callback is SQL work; acknowledgement
	// probes that commit, and a post-commit interruption repeats the original event.
	p := database(t)
	if e := eventstore.Migrate(ctx, p); e != nil {
		t.Fatal(e)
	}
	downstream := eventstore.Store{Pool: p}
	retry := messaging.RetryStore{Pool: p, Clock: f.Clock}
	if _, e = p.Exec(ctx, "CREATE TABLE delivery_effects(id uuid PRIMARY KEY)"); e != nil {
		t.Fatal(e)
	}
	handle := messaging.DurableHandler(downstream, retry, "us1-delivery", func(ctx context.Context, tx pgx.Tx, en events.Envelope) error {
		if _, e := tx.Exec(ctx, "INSERT INTO delivery_effects VALUES($1)", en.EventID); e != nil {
			return e
		}
		return eventstore.Enqueue(ctx, tx, en)
	}, nil)
	for i := 0; i < 3; i++ {
		select {
		case m := <-partition.Messages():
			m.Topic = "sale.config.v1"
			ack := false
			if e = messaging.Complete(ctx, m, handle, func() {
				var n int
				query := "SELECT count(*) FROM delivery_effects"
				if i == 0 {
					query = "SELECT count(*) FROM platform_failed_work WHERE state='QUARANTINED' AND record_key IS NULL AND payload IS NULL"
				}
				if err := p.QueryRow(ctx, query).Scan(&n); err != nil || n != 1 {
					t.Errorf("ack preceded durable handling: %d %v", n, err)
				}
				ack = true
			}); e != nil || !ack {
				t.Fatal(e)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	// Both destination receipts eventually acknowledge, then retention can prune.
	if sent, e := store.DispatchOne(ctx, now.Add(time.Second), func(_ context.Context, _ string, key string, raw []byte) error {
		_, _, e := producer.SendMessage(&sarama.ProducerMessage{Topic: topic, Key: sarama.StringEncoder(key), Value: sarama.ByteEncoder(raw)})
		return e
	}); e != nil || !sent {
		t.Fatal(sent, e)
	}
	if n, e := store.Prune(ctx, now); e != nil || n != 1 {
		t.Fatal("completed record not pruned", n, e)
	}
}
