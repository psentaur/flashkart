package main

import (
	"context"
	"flash-sale/internal/platform/runtime"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runtime.Run(ctx, "order", nil); err != nil {
		slog.Error("startup failed", "component", "order", "error", err)
		os.Exit(1)
	}
}
