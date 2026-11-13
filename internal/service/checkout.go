package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"flashkart/internal/market"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func checkoutDuration(variable, fallback string) (time.Duration, error) {
	raw := os.Getenv(variable)
	if raw == "" {
		raw = fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, errors.New("invalid " + variable)
	}
	return d, nil
}

func checkoutClient(endpoint string) (*grpc.ClientConn, error) {
	if !address(endpoint, false) {
		return nil, errors.New("invalid checkout gRPC address")
	}
	return grpc.NewClient("passthrough:///"+endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

func runCheckout(ctx context.Context, name string, cfg config, log *slog.Logger, db *pgxpool.Pool, app serviceApp) error {
	if cfg.GRPC == "" {
		return errors.New("checkout service requires a gRPC listener")
	}
	if name == "payment-service" {
		fixture := os.Getenv("FLASHKART_PAYMENT_FIXTURE")
		switch fixture {
		case "", "success", "decline", "delay", "lost-response":
		default:
			return errors.New("invalid FLASHKART_PAYMENT_FIXTURE")
		}
		delay, err := checkoutDuration("FLASHKART_PAYMENT_DELAY", "3m")
		if err != nil {
			return err
		}
		app.register = func(s *grpc.Server) {
			pb.RegisterPaymentServer(s, market.Payment{DB: db, Fixture: fixture, Delay: delay})
		}
		return serve(ctx, name, cfg, log, nil, nil, app)
	}
	token, customer := os.Getenv("FLASHKART_CUSTOMER_TOKEN"), os.Getenv("FLASHKART_CUSTOMER_ID")
	if strings.TrimSpace(token) == "" || strings.TrimSpace(customer) == "" || token == os.Getenv("FLASHKART_ADMIN_TOKEN") {
		return errors.New("a distinct customer token and customer ID are required")
	}
	inventory, err := checkoutClient(cfg.InventoryGRPC)
	if err != nil {
		return err
	}
	defer inventory.Close()
	payment, err := checkoutClient(cfg.PaymentGRPC)
	if err != nil {
		return err
	}
	defer payment.Close()
	orders := market.Orders{DB: db, Inventory: pb.NewInventoryClient(inventory), Payment: pb.NewPaymentClient(payment), Log: log}
	app.register = func(s *grpc.Server) { pb.RegisterOrderServer(s, orders) }
	app.wrap = func(fallback http.Handler) http.Handler { return orders.Handler(token, customer, fallback) }
	work, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); orders.Run(work) }()
	defer func() { stop(); <-done }()
	return serve(ctx, name, cfg, log, nil, nil, app)
}
