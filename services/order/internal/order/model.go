// Package order owns durable purchase identity and saga progress, never stock.
package order

import (
	"context"
	"encoding/json"
	"flash-sale/contracts/gen/inventory/v1"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/eventstore"
	"flash-sale/internal/platform/fault"
	"flash-sale/internal/platform/httpapi"
	"flash-sale/internal/platform/postgres"
	"flash-sale/services/order/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type Purchase = httpapi.Purchase
type Order = events.OrderPending
type Offer struct {
	SaleID            string `json:"sale_id"`
	ProductID         string `json:"product_id"`
	Version           int64  `json:"version"`
	UnitPriceMinor    int64  `json:"unit_price_minor"`
	Currency          string `json:"currency"`
	StartsAt          string `json:"starts_at"`
	EndsAt            string `json:"ends_at"`
	Status            string `json:"status"`
	PerShopperUnitCap int64  `json:"per_shopper_unit_cap"`
}
type Inventory interface {
	Reserve(context.Context, *inventoryv1.ReserveRequest) (*inventoryv1.OperationResult, error)
	Get(context.Context, string) (*inventoryv1.Reservation, error)
	Confirm(context.Context, string, string) (*inventoryv1.OperationResult, error)
	Release(context.Context, string, string) (*inventoryv1.OperationResult, error)
}
type Store struct {
	Pool      *pgxpool.Pool
	Clock     clock.Clock
	Inventory Inventory
	Offer     func(context.Context, string, string) (Offer, error)
	Fault     fault.Injector
}

func (s Store) Now() time.Time {
	if s.Clock != nil {
		return s.Clock.Now().UTC()
	}
	return time.Now().UTC()
}
func (s Store) Outbox() eventstore.Store { return eventstore.Store{Pool: s.Pool, Fault: s.Fault} }
func Migrate(ctx context.Context, p *pgxpool.Pool) error {
	return postgres.Migrate(ctx, p, []postgres.Migration{{Name: "001_orders", SQL: migrations.Orders}})
}
func (s Store) Get(ctx context.Context, id, shopper string) (Order, error) {
	var raw []byte
	var o Order
	err := s.Pool.QueryRow(ctx, "SELECT snapshot FROM orders WHERE order_id=$1 AND shopper_id=$2", id, shopper).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &o)
	}
	return o, err
}
func envelope(kind, aggregate string, version int64, correlation string, now time.Time, data any) events.Envelope {
	b, _ := json.Marshal(data)
	return events.Envelope{EventID: clock.UUID(), SchemaVersion: 1, Type: kind, OccurredAt: now.Format(time.RFC3339Nano), Producer: "order", AggregateID: aggregate, AggregateVersion: version, CorrelationID: correlation, Data: b}
}
