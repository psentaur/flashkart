package main

import (
	"context"
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
	if err := bootstrap.Run(ctx, "admission", nil); err != nil {
		slog.Error("startup failed", "component", "admission", "error", err)
		os.Exit(1)
	}
}
