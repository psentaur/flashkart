package service

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"

	"flashkart/internal/market"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
)

func notificationStore(ctx context.Context, cfg config) (*market.Notifications, error) {
	return market.NewNotifications(ctx, os.Getenv("FLASHKART_NOTIFICATION_TABLE"), cfg.AWS.Region, cfg.AWS.Endpoints["dynamodb"],
		os.Getenv("FLASHKART_DYNAMODB_CREDENTIALS_FILE"), os.Getenv("FLASHKART_DYNAMODB_CA_FILE"), cfg.Environment == "local")
}

func outcomeQueue(ctx context.Context, cfg config) (market.PurchaseQueue, error) {
	return market.NewPurchaseQueue(ctx, os.Getenv("FLASHKART_OUTCOME_QUEUE_URL"), cfg.AWS.Region, cfg.AWS.Endpoints["sqs"],
		os.Getenv("FLASHKART_SQS_CREDENTIALS_FILE"), os.Getenv("FLASHKART_SQS_CA_FILE"), cfg.Environment == "local")
}

func startEventDelivery(ctx context.Context, name string, cfg config, db *pgxpool.Pool, app *serviceApp, log *slog.Logger) (func(), error) {
	events, outcomes := "", ""
	switch name {
	case "admission-controller":
		events = "admission.events"
	case "inventory-reservation":
		events, outcomes = "inventory.events", "inventory.outcomes"
	case "order-service":
		events, outcomes = "orders.events", "orders.outcomes"
	case "payment-service":
		events = "payment.events"
	default:
		return func() {}, nil
	}
	var workers []market.EventDelivery
	var kafka *kgo.Client
	if len(cfg.Kafka.Brokers) > 0 && (cfg.Environment == "aws" || os.Getenv("FLASHKART_PUBLISH_EVENTS") == "1") {
		var err error
		kafka, err = market.NewEventProducer(cfg.Kafka.Brokers, cfg.Kafka.Auth, cfg.AWS.Region, os.Getenv("FLASHKART_PURCHASE_EVENTS_TOPIC"))
		if err != nil {
			return nil, err
		}
		worker, _ := market.NewEventDelivery(db, events, func(ctx context.Context, key, body string) error {
			return kafka.ProduceSync(ctx, &kgo.Record{Key: []byte(key), Value: []byte(body)}).FirstErr()
		}, log)
		workers = append(workers, worker)
	}
	if outcomes != "" && (os.Getenv("FLASHKART_OUTCOME_QUEUE_URL") != "" || cfg.Environment == "aws") {
		queue, err := outcomeQueue(ctx, cfg)
		if err != nil {
			if kafka != nil {
				kafka.Close()
			}
			return nil, err
		}
		worker, _ := market.NewEventDelivery(db, outcomes, func(ctx context.Context, _ string, body string) error { return queue.Send(ctx, body) }, log)
		workers = append(workers, worker)
	}
	if len(workers) == 0 {
		return func() {}, nil
	}
	workerCtx, stop := context.WithCancel(ctx)
	var group sync.WaitGroup
	for _, worker := range workers {
		group.Go(func() { worker.Run(workerCtx) })
	}
	app.metrics = func(ctx context.Context, w io.Writer) {
		io.WriteString(w, "# TYPE flashkart_outbox_pending gauge\n# TYPE flashkart_outbox_dead gauge\n")
		for _, worker := range workers {
			worker.Metrics(ctx, w)
		}
	}
	return func() {
		stop()
		group.Wait()
		if kafka != nil {
			kafka.Close()
		}
	}, nil
}

func runNotifications(ctx context.Context, cfg config, log *slog.Logger) error {
	store, err := notificationStore(ctx, cfg)
	if err != nil {
		return err
	}
	if api := os.Getenv("AWS_LAMBDA_RUNTIME_API"); api != "" {
		return runLambda(ctx, api, store.Batch)
	}
	token, customer, err := customerConfig()
	if err != nil {
		return err
	}
	if cfg.Environment == "local" {
		queue, err := outcomeQueue(ctx, cfg)
		if err != nil {
			return err
		}
		work, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			store.Run(work, queue, func() { log.Warn("notification message will retry") })
		}()
		defer func() { stop(); <-done }()
	}
	app := serviceApp{wrap: func(fallback http.Handler) http.Handler { return store.Handler(token, customer, fallback) }}
	return serve(ctx, "notification", cfg, log, nil, nil, app)
}
