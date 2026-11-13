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

	pb "flashkart/gen/flashkart/v1"
	"flashkart/internal/replica"
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
		err = runService(ctx, name, cfg, log)
	}
	if err != nil {
		log.Error("service failed", "error", err)
		os.Exit(1)
	}
}

func runService(ctx context.Context, name string, cfg config, log *slog.Logger) error {
	switch name {
	case "notification":
		if os.Getenv("FLASHKART_NOTIFICATION_TABLE") != "" || os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" || cfg.Environment == "aws" {
			return runNotifications(ctx, cfg, log)
		}
		return serve(ctx, name, cfg, log, nil, nil)
	case "product-catalog", "flash-sale-manager", "inventory-reservation", "admission-controller", "order-service", "payment-service":
		return runMarket(ctx, name, cfg, log)
	case "catalog-replica":
	default:
		return serve(ctx, name, cfg, log, nil, nil)
	}
	dsn := os.Getenv(cfg.Databases[cfg.ReplicaID])
	if dsn == "" {
		return errors.New("missing replica DSN in " + cfg.Databases[cfg.ReplicaID])
	}
	store, err := replica.Open(ctx, dsn, cfg.ReplicaID)
	if err != nil {
		return err
	}
	defer store.Close()
	timeout := time.Duration(cfg.OperationTimeoutMS) * time.Millisecond
	client, err := replica.NewClient(cfg.Peers, timeout)
	if err != nil {
		return err
	}
	defer client.Close()
	coordinator := replica.NewCoordinator(store, client, time.Duration(cfg.PeerTimeoutMS)*time.Millisecond, timeout)
	recoveryCtx, stopRecovery := context.WithCancel(ctx)
	recoveryDone := make(chan struct{})
	go func() {
		defer close(recoveryDone)
		coordinator.RunRecovery(recoveryCtx)
	}()
	defer func() {
		stopRecovery()
		<-recoveryDone
	}()
	return serve(ctx, name, cfg, log, store, coordinator)
}

func serve(ctx context.Context, name string, cfg config, log *slog.Logger, store *replica.Store, coordinator *replica.Coordinator, apps ...serviceApp) error {
	var app serviceApp
	if len(apps) > 0 {
		app = apps[0]
	}
	if store != nil {
		app.ready = store.Ready
	}
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
		if store != nil {
			if coordinator == nil {
				pb.RegisterCatalogReplicationServer(grpcServer, store)
			} else {
				pb.RegisterCatalogReplicationServer(grpcServer, coordinator)
			}
			pb.RegisterReplicaPeerServer(grpcServer, store)
		}
		if app.register != nil {
			app.register(grpcServer)
		}
		if app.ready != nil {
			healthServer.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
			probeContext, stopProbes := context.WithCancel(ctx)
			defer stopProbes()
			go monitorReadiness(probeContext, healthServer, app.ready)
		}
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
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		probe, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if !ready.Load() || app.ready != nil && !app.ready(probe) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "# TYPE flashkart_http_requests_total counter\nflashkart_http_requests_total %d\n", requests.Load())
		if coordinator != nil {
			coordinator.WriteMetrics(w)
		}
		if app.metrics != nil {
			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			defer cancel()
			app.metrics(ctx, w)
		}
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

	var handler http.Handler = mux
	if app.wrap != nil {
		handler = app.wrap(mux)
	}
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			handler.ServeHTTP(w, r)
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

func monitorReadiness(ctx context.Context, healthServer *health.Server, ready func(context.Context) bool) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for ctx.Err() == nil {
		probe, cancel := context.WithTimeout(ctx, time.Second)
		state := healthpb.HealthCheckResponse_NOT_SERVING
		if ready(probe) {
			state = healthpb.HealthCheckResponse_SERVING
		}
		cancel()
		healthServer.SetServingStatus("", state)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
