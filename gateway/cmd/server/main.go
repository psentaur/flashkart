package main

import (
	"context"
	"flash-sale/frontend"
	"flash-sale/internal/platform/bootstrap"
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
	if err := bootstrap.Run(ctx, "gateway", handler); err != nil {
		slog.Error("startup failed", "component", "gateway", "error", err)
		os.Exit(1)
	}
}
