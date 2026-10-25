// Package runtime wires foundation lifecycle checks without claiming story readiness.
package runtime

import (
	"context"
	"errors"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/bootstrap"
	"flash-sale/internal/platform/httpapi"
	"flash-sale/internal/platform/messaging"
	"flash-sale/internal/platform/postgres"
	"flash-sale/internal/platform/telemetry"
	"fmt"
	"github.com/IBM/sarama"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type Check func(context.Context) error
type Lifecycle struct {
	Component     string
	Checks        map[string]Check
	Metrics       *telemetry.Metrics
	Revision      string
	Capabilities  []string
	BusinessReady bool
}

func (l Lifecycle) Handler(frontend http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		httpapi.WriteJSON(w, 200, map[string]any{"component": l.Component, "status": "alive", "revision": l.Revision})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		status := 200
		checks := map[string]string{}
		for name, check := range l.Checks {
			checks[name] = "ready"
			if check(ctx) != nil {
				status = 503
				checks[name] = "unavailable"
			}
		}
		httpapi.WriteJSON(w, status, map[string]any{"component": l.Component, "status": map[bool]string{true: "foundation_ready", false: "dependency_unavailable"}[status == 200], "mode": "foundation", "business_ready": l.BusinessReady, "capabilities": l.Capabilities, "revision": l.Revision, "checks": checks})
	})
	mux.Handle("GET /metrics", l.Metrics.Handler())
	if frontend != nil {
		mux.Handle("/", frontend)
	}
	return httpapi.Middleware(l.Metrics.Observe("foundation", mux))
}

type Application struct {
	Metrics      *telemetry.Metrics
	Private      *http.Server
	Handler      http.Handler
	Worker       func(context.Context) error
	Checks       map[string]Check
	Capabilities []string
}
type Build func(*pgxpool.Pool, auth.Verifier) (Application, error)

func Run(ctx context.Context, component string, frontend http.Handler) error {
	return RunApplication(ctx, component, func(_ *pgxpool.Pool, _ auth.Verifier) (Application, error) {
		return Application{Handler: frontend}, nil
	})
}
func RunApplication(ctx context.Context, component string, build Build) error {
	ctx, cancelApplication := context.WithCancel(ctx)
	defer cancelApplication()
	cfg, e := bootstrap.Load(component)
	if e != nil {
		return e
	}
	verifier, e := auth.Load(os.Getenv("JWT_PUBLIC_KEY_FILE"), env("JWT_ISSUER", "flashkart-local"), env("JWT_AUDIENCE", "flashkart"))
	if e != nil {
		return e
	}
	checks := map[string]Check{}
	var pool *pgxpool.Pool
	if component == "admission" || component == "flash-sale" || component == "order" {
		prefix := strings.ToUpper(strings.ReplaceAll(component, "-", "_"))
		initCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		pool, e = postgres.Open(initCtx, component, os.Getenv(prefix+"_DATABASE_URL"))
		cancel()
		if e != nil {
			return e
		}
		defer pool.Close()
		checks["owner_database"] = func(ctx context.Context) error {
			if e := pool.Ping(ctx); e != nil {
				return e
			}
			var present bool
			e := pool.QueryRow(ctx, "SELECT to_regclass('platform_inbox') IS NOT NULL").Scan(&present)
			if e != nil {
				return e
			}
			if !present {
				return fmt.Errorf("migrations missing")
			}
			return nil
		}
	}
	if component != "gateway" {
		checks["kafka"] = func(ctx context.Context) error {
			c := messaging.Config(component + "-health")
			c.Net.DialTimeout = time.Second
			c.Net.ReadTimeout = time.Second
			c.Net.WriteTimeout = time.Second
			c.Metadata.Retry.Max = 0
			if d, ok := ctx.Deadline(); ok && time.Until(d) <= 0 {
				return ctx.Err()
			}
			admin, e := sarama.NewClusterAdmin(strings.Split(os.Getenv("KAFKA_BROKERS"), ","), c)
			if e != nil {
				return e
			}
			defer admin.Close()
			_, _, e = admin.DescribeCluster()
			return e
		}
	}
	app, e := build(pool, verifier)
	if e != nil {
		return e
	}
	for name, check := range app.Checks {
		checks[name] = check
	}
	l := Lifecycle{Capabilities: app.Capabilities, Component: component, Checks: checks, Metrics: telemetry.New(), Revision: env("APP_REVISION", "development")}
	if app.Metrics != nil {
		l.Metrics = app.Metrics
	}
	listener, e := net.Listen("tcp", cfg.ListenAddress)
	if e != nil {
		return e
	}
	server := &http.Server{Handler: l.Handler(app.Handler), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	done := make(chan error, 2)
	if app.Private != nil {
		privateListener, err := net.Listen("tcp", app.Private.Addr)
		if err != nil {
			_ = listener.Close()
			return err
		}
		defer app.Private.Close()
		go func() { done <- app.Private.ServeTLS(privateListener, "", "") }()
	}
	go func() { done <- server.Serve(listener) }()
	defer server.Close()
	workerDone := make(chan error, 1)
	if app.Worker != nil {
		go func() { workerDone <- app.Worker(ctx) }()
	} else {
		workerDone = nil
	}
	defer func() {
		cancelApplication()
		if workerDone != nil {
			<-workerDone
		}
	}()
	slog.Info("service started", "capabilities", app.Capabilities, "component", component, "revision", l.Revision, "business_ready", false)
	select {
	case e := <-workerDone:
		workerDone = nil
		_ = server.Close()
		if e == nil && ctx.Err() == nil {
			return fmt.Errorf("application worker stopped")
		}
		return e
	case e := <-done:
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return e
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if app.Private != nil {
			if err := app.Private.Shutdown(shutdown); err != nil {
				_ = server.Close()
				return err
			}
		}
		if e = server.Shutdown(shutdown); e != nil {
			_ = server.Close()
			return e
		}
		e = <-done
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return e
	}
}
func env(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
