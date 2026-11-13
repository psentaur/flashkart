package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"flashkart/internal/market"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	source := flag.String("source", "", "schema.events or schema.outcomes")
	id := flag.String("id", "", "event ID from the source outbox")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := replay(ctx, *source, *id); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Event returned to the delivery queue.")
}
func replay(ctx context.Context, source, id string) error {
	dsn := os.Getenv("FLASHKART_TRANSACTIONAL_DSN")
	if dsn == "" {
		return fmt.Errorf("FLASHKART_TRANSACTIONAL_DSN is required")
	}
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("invalid transactional database configuration")
	}
	defer db.Close()
	worker, err := market.NewEventDelivery(db, source, nil, slog.Default())
	if err != nil {
		return err
	}
	return worker.Replay(ctx, id)
}
