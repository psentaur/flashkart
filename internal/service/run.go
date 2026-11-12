package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func Main(name string) {
	path := flag.String("config", os.Getenv("FLASHKART_CONFIG"), "JSON configuration file")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("service", name)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cfg, err := readConfig(*path, name)
	if err == nil {
		if api := os.Getenv("AWS_LAMBDA_RUNTIME_API"); name == "notification" && api != "" {
			err = runLambda(ctx, api, log)
		} else {
			err = serve(ctx, name, cfg, log)
		}
	}
	if err != nil {
		log.Error("service failed", "error", err)
		os.Exit(1)
	}
}

func serve(ctx context.Context, name string, cfg config, log *slog.Logger) error {
	listener, err := net.Listen("tcp", cfg.HTTP)
	if err != nil {
		return err
	}
	defer listener.Close()

	var grpcServer *grpc.Server
	var healthServer *health.Server
	failures := make(chan error, 2)
	if cfg.GRPC != "" {
		peerListener, err := net.Listen("tcp", cfg.GRPC)
		if err != nil {
			return err
		}
		defer peerListener.Close()
		grpcServer = grpc.NewServer()
		healthServer = health.NewServer()
		healthpb.RegisterHealthServer(grpcServer, healthServer)
		go func() { failures <- grpcServer.Serve(peerListener) }()
		log.Info("gRPC listening", "address", peerListener.Addr().String())
	}

	var requests atomic.Uint64
	var ready atomic.Bool
	ready.Store(true)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "# TYPE flashkart_http_requests_total counter\nflashkart_http_requests_total %d\n", requests.Load())
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		correlationID := r.Header.Get("X-Correlation-ID")
		if correlationID == "" {
			correlationID = rand.Text()
		}
		w.Header().Set("X-Correlation-ID", correlationID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{
			"code": "NOT_IMPLEMENTED", "message": name + " business handlers are pending",
			"correlation_id": correlationID,
		})
	})

	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			mux.ServeHTTP(w, r)
		}),
	}
	go func() { failures <- server.Serve(listener) }()
	log.Info("HTTP listening", "address", listener.Addr().String())

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-failures:
	}
	ready.Store(false)
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if grpcServer != nil {
		healthServer.Shutdown()
		stopped := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-shutdown.Done():
			grpcServer.Stop()
		}
	}
	shutdownErr := server.Shutdown(shutdown)
	if shutdownErr != nil {
		server.Close()
	}
	log.Info("service stopped")
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, shutdownErr)
}
