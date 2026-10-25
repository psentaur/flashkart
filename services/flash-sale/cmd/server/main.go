package main

import (
	"context"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/messaging"
	"flash-sale/internal/platform/runtime"
	app "flash-sale/services/flash-sale"
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
	if err := runtime.RunApplication(ctx, "flash-sale", func(pool *pgxpool.Pool, verifier auth.Verifier) (runtime.Application, error) {
		store := app.Store{Pool: pool}
		tlsConfig, e := auth.ServerTLS(filepath.Dir(os.Getenv("JWT_PUBLIC_KEY_FILE")), "flash-sale")
		if e != nil {
			return runtime.Application{}, e
		}
		privateAddress := os.Getenv("FLASH_SALE_INTERNAL_LISTEN_ADDR")
		if privateAddress == "" {
			privateAddress = "127.0.0.1:8442"
		}
		handler := app.Handler(store, verifier)
		private := &http.Server{Addr: privateAddress, TLSConfig: tlsConfig, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/internal/offers/") {
				handler.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
		}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
		return runtime.Application{Handler: handler, Private: private, Capabilities: []string{"us1-sale-configuration"}, Worker: func(ctx context.Context) error {
			return messaging.RunService(ctx, strings.Split(os.Getenv("KAFKA_BROKERS"), ","), "flash-sale-readiness-v1", store.Outbox(), store.Handle, store.Advance, "sale.ready.v1")
		}}, nil
	}); err != nil {
		slog.Error("startup failed", "component", "flash-sale", "error", err)
		os.Exit(1)
	}
}
