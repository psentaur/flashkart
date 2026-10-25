package main

import (
	"context"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/messaging"
	"flash-sale/internal/platform/runtime"
	"flash-sale/internal/platform/telemetry"
	app "flash-sale/services/admission"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runtime.RunApplication(ctx, "admission", func(pool *pgxpool.Pool, verifier auth.Verifier) (runtime.Application, error) {
		store := app.Store{Pool: pool}
		dir := filepath.Dir(os.Getenv("JWT_PUBLIC_KEY_FILE"))
		serverTLS, err := auth.ServerTLS(dir, "admission")
		if err != nil {
			return runtime.Application{}, err
		}
		clientTLS, err := auth.ClientTLS(dir, "admission")
		if err != nil {
			return runtime.Application{}, err
		}
		metrics := telemetry.New()
		handler := app.Handler(app.Config{Store: store, Verifier: verifier, Metrics: metrics, OrderURL: env("ORDER_INTERNAL_URL", "https://127.0.0.1:8443"), Client: &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}, Timeout: 1500 * time.Millisecond}})
		private := &http.Server{Addr: env("ADMISSION_INTERNAL_LISTEN_ADDR", "127.0.0.1:8441"), Handler: handler, TLSConfig: serverTLS, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
		return runtime.Application{Metrics: metrics, Handler: handler, Private: private, Capabilities: []string{"us1-sale-configuration", "us2-purchase-admission"}, Worker: func(ctx context.Context) error {
			return messaging.RunService(ctx, strings.Split(os.Getenv("KAFKA_BROKERS"), ","), "admission-config-v1", store.Outbox(), store.Handle, nil, "sale.config.v1")
		}}, nil
	}); err != nil {
		slog.Error("startup failed", "component", "admission", "error", err)
		os.Exit(1)
	}
}

func env(k, f string) string {
	if s := os.Getenv(k); s != "" {
		return s
	}
	return f
}
