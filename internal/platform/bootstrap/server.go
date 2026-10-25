package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Handler exposes liveness while keeping unfinished application readiness false.
func Handler(component string, frontend http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusOK, component, "alive")
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusServiceUnavailable, component, "phase_1_scaffold")
	})
	if frontend != nil {
		mux.Handle("/", frontend)
	}
	return mux
}

func writeStatus(w http.ResponseWriter, code int, component, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"component": component, "status": status})
}

func Run(ctx context.Context, component string, frontend http.Handler) error {
	config, err := Load(component)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: config.ListenAddress, Handler: Handler(component, frontend), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	stopped := make(chan error, 1)
	go func() { stopped <- server.ListenAndServe() }()
	slog.Info("starting Phase 1 scaffold", "component", component, "address", config.ListenAddress)
	select {
	case err := <-stopped:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve %s: %w", component, err)
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return fmt.Errorf("shutdown %s: %w", component, err)
		}
		if err := <-stopped; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve %s: %w", component, err)
		}
		return nil
	}
}
