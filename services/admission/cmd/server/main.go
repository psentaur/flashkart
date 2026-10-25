package main

import (
	"context"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/messaging"
	"flash-sale/internal/platform/runtime"
	app "flash-sale/services/admission"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runtime.RunApplication(ctx, "admission", func(pool *pgxpool.Pool, verifier auth.Verifier) (runtime.Application, error) {
		store := app.Store{Pool: pool}
		return runtime.Application{Handler: nil, Capabilities: []string{"us1-sale-configuration"}, Worker: func(ctx context.Context) error {
			return messaging.RunService(ctx, strings.Split(os.Getenv("KAFKA_BROKERS"), ","), "admission-config-v1", store.Outbox(), store.Handle, nil, "sale.config.v1")
		}}, nil
	}); err != nil {
		slog.Error("startup failed", "component", "admission", "error", err)
		os.Exit(1)
	}
}
