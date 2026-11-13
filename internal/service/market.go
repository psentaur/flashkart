package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
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
	if name != "inventory-reservation" && token == "" {
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
			table = "inventory.sales"
		}
		app.ready = func(ctx context.Context) bool {
			var ready bool
			return db.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&ready) == nil && ready
		}
	}
	if name == "inventory-reservation" {
		if cfg.GRPC == "" {
			return errors.New("inventory requires a gRPC listener")
		}
		app.register = func(server *grpc.Server) {
			pb.RegisterInventoryServer(server, market.Inventory{DB: db})
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
