package main

import (
	"context"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/messaging"
	"flash-sale/internal/platform/runtime"
	app "flash-sale/services/order"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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
	if err := runtime.RunApplication(ctx, "order", func(pool *pgxpool.Pool, verifier auth.Verifier) (runtime.Application, error) {
		dir := filepath.Dir(os.Getenv("JWT_PUBLIC_KEY_FILE"))
		clientTLS, err := auth.ClientTLS(dir, "order")
		if err != nil {
			return runtime.Application{}, err
		}
		serverTLS, err := auth.ServerTLS(dir, "order")
		if err != nil {
			return runtime.Application{}, err
		}
		conn, err := grpc.NewClient(os.Getenv("INVENTORY_GRPC_ADDRESS"), grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
		if err != nil {
			return runtime.Application{}, err
		}
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}, Timeout: 1500 * time.Millisecond}
		store := app.Store{Pool: pool, Inventory: app.NewInventoryClient(conn), Offer: app.OfferClient(client, env("FLASH_SALE_INTERNAL_URL", "https://127.0.0.1:8442"))}
		handler := app.Handler(store, verifier)
		private := &http.Server{Addr: env("ORDER_INTERNAL_LISTEN_ADDR", "127.0.0.1:8443"), Handler: handler, TLSConfig: serverTLS, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
		return runtime.Application{Handler: handler, Private: private, Capabilities: []string{"us2-purchase-order"}, Worker: func(ctx context.Context) error {
			defer conn.Close()
			defer client.CloseIdleConnections()
			return messaging.RunService(ctx, strings.Split(os.Getenv("KAFKA_BROKERS"), ","), "order-payment-v1", store.Outbox(), store.Handle, nil, "payment.events.v1")
		}}, nil
	}); err != nil {
		slog.Error("startup failed", "component", "order", "error", err)
		os.Exit(1)
	}
}

func env(k, f string) string {
	if s := os.Getenv(k); s != "" {
		return s
	}
	return f
}
