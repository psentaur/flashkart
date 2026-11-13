package service

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"flashkart/internal/market"
)

func runAnalytics(ctx context.Context, cfg config, log *slog.Logger) error {
	projection, err := market.NewAnalytics(ctx, os.Getenv("FLASHKART_ANALYTICS_TABLE"), cfg.AWS.Region, cfg.AWS.Endpoints["dynamodb"],
		os.Getenv("FLASHKART_DYNAMODB_CREDENTIALS_FILE"), os.Getenv("FLASHKART_DYNAMODB_CA_FILE"), cfg.Environment == "local")
	if err != nil {
		return err
	}
	group := os.Getenv("FLASHKART_ANALYTICS_GROUP")
	if group == "" {
		group = "flashkart-analytics-projection-v1"
	}
	topic := os.Getenv("FLASHKART_ANALYTICS_RESULTS_TOPIC")
	if topic == "" {
		topic = "analytics-results"
	}
	eventsTopic := os.Getenv("FLASHKART_PURCHASE_EVENTS_TOPIC")
	if eventsTopic == "" {
		eventsTopic = "purchase-events"
	}
	work, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 1)
	go func() {
		done <- projection.Run(work, cfg.Kafka.Brokers, cfg.Kafka.Auth, cfg.AWS.Region, group, eventsTopic, topic, log)
		stop()
	}()
	err = serve(work, "analytics-api", cfg, log, nil, nil, serviceApp{wrap: func(fallback http.Handler) http.Handler {
		return projection.Handler(fallback)
	}})
	stop()
	if workerErr := <-done; err == nil {
		err = workerErr
	}
	return err
}
