package messaging

import (
	"context"
	"errors"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/eventstore"
	"github.com/IBM/sarama"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"net"
	"time"
)

// RunService drives durable SQL consumers, delayed repair and destination receipts.
// Losing a broker connection leaves receipts/inbox work replayable on restart.
func RunService(ctx context.Context, brokers []string, consumer string, store eventstore.Store, handle func(context.Context, pgx.Tx, events.Envelope) error, tick func(context.Context) error, topics ...string) error {
	producer, e := Producer(brokers, consumer)
	if e != nil {
		return e
	}
	defer producer.Close()
	group, e := Group(brokers, consumer)
	if e != nil {
		return e
	}
	defer group.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	retries := RetryStore{Pool: store.Pool}
	outage := dependencyOutage
	handler := DurableHandler(store, retries, consumer, handle, outage)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			e := Consume(ctx, group, topics, handler)
			if ctx.Err() != nil {
				return
			}
			if e != nil {
				slog.Warn("consumer paused", "consumer", consumer)
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	defer func() { cancel(); <-done }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	nextSchedule := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			// SQL recovery resumes paused dependency failures without spending retry budget.
			if e = store.Pool.Ping(ctx); e != nil {
				continue
			}
			if e = retries.Resume(ctx, consumer); e != nil {
				continue
			}
			_, e = retries.RunOne(ctx, consumer, func(ctx context.Context, f FailedWork) error {
				en, e := events.Decode(f.Topic, string(f.Key), f.Payload)
				if e != nil {
					return e
				}
				_, e = store.Consume(ctx, consumer, en.EventID, f.Payload, func(tx pgx.Tx) error { return handle(ctx, tx, en) })
				return e
			}, outage)
			if e != nil {
				continue
			}
			if tick != nil && !now.Before(nextSchedule) {
				if e = tick(ctx); e != nil {
					slog.Warn("sale schedule deferred", "consumer", consumer)
				}
				nextSchedule = now.Add(time.Second)
			}
			for range 100 {
				sent, err := store.DispatchOne(ctx, now, func(ctx context.Context, topic, key string, raw []byte) error {
					if e := ctx.Err(); e != nil {
						return e
					}
					_, _, e := producer.SendMessage(&sarama.ProducerMessage{Topic: topic, Key: sarama.StringEncoder(key), Value: sarama.ByteEncoder(raw)})
					return e
				})
				if err != nil || !sent {
					break
				}
			}
		}
	}
}

func dependencyOutage(err error) bool {
	var invalid *events.QuarantineError
	if err == nil || errors.As(err, &invalid) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var network net.Error
	return errors.As(err, &network)
}
