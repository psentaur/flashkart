package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"flashkart/internal/market"
	"flashkart/internal/replica"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type serviceApp struct {
	wrap     func(http.Handler) http.Handler
	register func(*grpc.Server)
	ready    func(context.Context) bool
}

func runMarket(ctx context.Context, name string, cfg config, log *slog.Logger) error {
	app := serviceApp{}
	token := os.Getenv("FLASHKART_ADMIN_TOKEN")
	if (name == "product-catalog" || name == "flash-sale-manager") && token == "" {
		return errors.New("FLASHKART_ADMIN_TOKEN is required")
	}
	var db *pgxpool.Pool
	if name != "product-catalog" {
		dsn := os.Getenv(cfg.Databases["transactional"])
		if dsn == "" {
			return errors.New("transactional database DSN is required")
		}
		poolConfig, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			return errors.New("invalid transactional database DSN")
		}
		poolConfig.ConnConfig.RuntimeParams["synchronous_commit"] = "on"
		db, err = pgxpool.NewWithConfig(ctx, poolConfig)
		if err != nil {
			return errors.New("could not initialize transactional database pool")
		}
		defer db.Close()
		table := "flash_sale.drafts"
		if name == "inventory-reservation" {
			table = "inventory.reservations"
		}
		switch name {
		case "admission-controller":
			table = "admission.attempts"
		case "order-service":
			table = "orders.purchases"
		case "payment-service":
			table = "payment.transactions"
		}
		app.ready = func(ctx context.Context) bool {
			var ready bool
			return db.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&ready) == nil && ready
		}
	}
	if name == "order-service" || name == "payment-service" {
		return runCheckout(ctx, name, cfg, log, db, app)
	}

	if name == "admission-controller" || name == "inventory-reservation" {
		queueURL := os.Getenv("FLASHKART_PURCHASE_QUEUE_URL")
		var queue market.PurchaseQueue
		if queueURL != "" || cfg.Environment == "aws" {
			var err error
			queue, err = market.NewPurchaseQueue(ctx, queueURL, cfg.AWS.Region, cfg.AWS.Endpoints["sqs"],
				os.Getenv("FLASHKART_SQS_CREDENTIALS_FILE"), os.Getenv("FLASHKART_SQS_CA_FILE"), cfg.Environment == "local")
			if err != nil {
				return err
			}
		}
		workerCtx, stop := context.WithCancel(ctx)
		defer stop()
		var worker func()
		if name == "admission-controller" {
			customerToken, customerID := os.Getenv("FLASHKART_CUSTOMER_TOKEN"), os.Getenv("FLASHKART_CUSTOMER_ID")
			if strings.TrimSpace(customerToken) == "" || strings.ContainsAny(customerToken, " \t\r\n") ||
				strings.TrimSpace(customerID) == "" || len(customerID) > 200 || strings.ContainsRune(customerID, 0) || customerToken == token {
				return errors.New("a distinct customer token and customer ID are required")
			}
			settings := []struct {
				name  string
				value int
			}{{"FLASHKART_ADMISSION_RATE", 200}, {"FLASHKART_ADMISSION_BURST", 200}, {"FLASHKART_ADMISSION_CONCURRENCY", 32}}
			for i := range settings {
				if raw := os.Getenv(settings[i].name); raw != "" {
					n, err := strconv.Atoi(raw)
					if err != nil || n < 1 || n > 100000 {
						return errors.New("invalid " + settings[i].name)
					}
					settings[i].value = n
				}
			}
			admission := market.NewAdmission(db, log, settings[0].value, settings[1].value, settings[2].value)
			app.wrap = func(fallback http.Handler) http.Handler {
				return admission.Handler(customerToken, customerID, fallback)
			}
			worker = func() { admission.Run(workerCtx, queue) }
		} else {
			if cfg.GRPC == "" {
				return errors.New("inventory requires a gRPC listener")
			}
			hold, err := checkoutDuration("FLASHKART_RESERVATION_TTL", "2m")
			if err != nil {
				return err
			}
			sweep, err := checkoutDuration("FLASHKART_EXPIRY_INTERVAL", "5s")
			if err != nil {
				return err
			}
			inventory := market.Inventory{DB: db, HoldDuration: hold}
			orderConn, err := checkoutClient(cfg.OrderGRPC)
			if err != nil {
				return err
			}
			defer orderConn.Close()
			var workers sync.WaitGroup
			workers.Go(func() { inventory.DeliverOrders(workerCtx, pb.NewOrderClient(orderConn), log) })
			workers.Go(func() { inventory.SweepExpired(workerCtx, log, sweep) })
			defer func() { stop(); workers.Wait() }()
			app.register = func(server *grpc.Server) { pb.RegisterInventoryServer(server, inventory) }
			worker = func() { inventory.Consume(workerCtx, queue, log) }
		}
		if queue != nil {
			done := make(chan struct{})
			go func() { defer close(done); worker() }()
			defer func() { stop(); <-done }()
		}
		return serve(ctx, name, cfg, log, nil, nil, app)
	}

	client, err := replica.NewClient(cfg.Peers, time.Duration(cfg.OperationTimeoutMS)*time.Millisecond)
	if err != nil {
		return err
	}
	defer client.Close()
	if name == "product-catalog" {
		images, err := market.NewImages(ctx, os.Getenv("FLASHKART_PRODUCT_IMAGES_BUCKET"), cfg.AWS.Region,
			cfg.AWS.Endpoints["s3"], os.Getenv("FLASHKART_S3_CREDENTIALS_FILE"), cfg.Environment == "local")
		if err != nil {
			return err
		}
		catalog := market.Catalog{Replicas: client, Images: images}
		app.wrap = func(fallback http.Handler) http.Handler { return catalog.Handler(token, fallback) }
		return serve(ctx, name, cfg, log, nil, nil, app)
	}

	if !address(cfg.InventoryGRPC, false) {
		return errors.New("invalid inventory gRPC address")
	}
	conn, err := grpc.NewClient("passthrough:///"+cfg.InventoryGRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	publisher := market.Publisher{DB: db, Replicas: client, Inventory: pb.NewInventoryClient(conn), Log: log}
	app.wrap = func(fallback http.Handler) http.Handler { return publisher.Handler(token, fallback) }
	workCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); publisher.Run(workCtx) }()
	defer func() { stop(); <-done }()
	return serve(ctx, name, cfg, log, nil, nil, app)
}
