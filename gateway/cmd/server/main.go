package main

import (
	"context"
	"flash-sale/frontend"
	"flash-sale/gateway"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/runtime"
	"flash-sale/internal/platform/telemetry"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	handler, err := frontend.Handler()
	if err != nil {
		slog.Error("load frontend", "error", err)
		os.Exit(1)
	}
	if err := runtime.RunApplication(ctx, "gateway", func(_ *pgxpool.Pool, verifier auth.Verifier) (runtime.Application, error) {
		tlsConfig, err := auth.ClientTLS(filepath.Dir(os.Getenv("JWT_PUBLIC_KEY_FILE")), "gateway")
		if err != nil {
			return runtime.Application{}, err
		}
		metrics := telemetry.New()
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}, Timeout: 2 * time.Second}
		return runtime.Application{Metrics: metrics, Handler: gateway.Handler(gateway.Config{Verifier: verifier, Metrics: metrics, Client: client, AdmissionURL: env("ADMISSION_INTERNAL_URL", "https://127.0.0.1:8441"), OrderURL: os.Getenv("ORDER_URL"), SaleURL: os.Getenv("FLASH_SALE_URL"), CatalogURL: os.Getenv("CATALOG_URL"), Frontend: handler}), Capabilities: []string{"us1-browsing"}}, nil
	}); err != nil {
		slog.Error("startup failed", "component", "gateway", "error", err)
		os.Exit(1)
	}
}

func env(k, f string) string {
	if s := os.Getenv(k); s != "" {
		return s
	}
	return f
}
