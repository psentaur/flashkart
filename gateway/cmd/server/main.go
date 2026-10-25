package main

import (
	"context"
	"flash-sale/frontend"
	"flash-sale/gateway"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/runtime"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
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
		return runtime.Application{Handler: gateway.Handler(gateway.Config{Verifier: verifier, SaleURL: os.Getenv("FLASH_SALE_URL"), CatalogURL: os.Getenv("CATALOG_URL"), Frontend: handler}), Capabilities: []string{"us1-browsing"}}, nil
	}); err != nil {
		slog.Error("startup failed", "component", "gateway", "error", err)
		os.Exit(1)
	}
}
