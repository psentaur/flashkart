package order

import (
	"context"
	"encoding/json"
	"flash-sale/contracts/gen/inventory/v1"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/eventstore"
	"flash-sale/internal/platform/httpapi"
	"github.com/jackc/pgx/v5"
	"time"
)

func (s Store) Consume(ctx context.Context, topic, key string, raw []byte) error {
	e, err := events.Decode(topic, key, raw)
	if err != nil {
		return err
	}
	_, err = s.Outbox().Consume(ctx, "order-payment-v1", e.EventID, raw, func(tx pgx.Tx) error { return s.Handle(ctx, tx, e) })
	return err
}
func (s Store) Handle(ctx context.Context, tx pgx.Tx, e events.Envelope) error {
	ctx = httpapi.WithCorrelation(ctx, e.CorrelationID)
	if e.Type != "PaymentSucceeded" {
		return &events.QuarantineError{Reason: "payment outcome awaits Phase 5 handler"}
	}
	var p events.PaymentSucceeded
	if err := json.Unmarshal(e.Data, &p); err != nil {
		return err
	}
	var raw, prior []byte
	var attempt string
	var version *int64
	if err := tx.QueryRow(ctx, "SELECT snapshot,payment_attempt_id::text,payment_version,payment_success FROM orders WHERE order_id=$1 FOR UPDATE", p.OrderID).Scan(&raw, &attempt, &version, &prior); err != nil {
		return err
	}
	var o Order
	if err := json.Unmarshal(raw, &o); err != nil {
		return err
	}
	if p.AttemptID != attempt || p.ReservationID != o.ReservationID || p.ShopperID != o.ShopperID || p.SaleID != o.SaleID || p.ProductID != o.ProductID || p.AmountMinor != o.TotalMinor || p.Currency != o.Currency || p.State != "SUCCEEDED" {
		return &events.QuarantineError{Reason: "payment identity or terms mismatch"}
	}
	if version != nil && *version != p.Version {
		return &events.QuarantineError{Reason: "contradictory payment result version"}
	}
	if o.State == "CONFIRMED" {
		return nil
	}
	if o.State != "PENDING_PAYMENT" {
		return &events.QuarantineError{Reason: "late successful payment requires compensation"}
	}
	// A timeout may hide a successful Confirm. Query before deciding anything else.
	result, err := s.Inventory.Confirm(ctx, o.OrderID, o.ReservationID)
	var reservation *inventoryv1.Reservation
	if err == nil {
		reservation = result.Reservation
	}
	if reservation == nil || reservation.State != inventoryv1.ReservationState_CONFIRMED {
		reservation, err = s.Inventory.Get(ctx, o.OrderID)
	}
	if err != nil || reservation == nil || reservation.State != inventoryv1.ReservationState_CONFIRMED {
		_, err = tx.Exec(ctx, "UPDATE orders SET workflow_step='RECONCILING',payment_success=$2,payment_version=$3,updated_at=$4 WHERE order_id=$1", o.OrderID, e.Data, p.Version, s.Now())
		return err
	}
	if reservation.ReservationId != o.ReservationID || reservation.OrderId != o.OrderID || reservation.ShopperId != o.ShopperID || reservation.SaleId != o.SaleID || reservation.ProductId != o.ProductID || int64(reservation.Quantity) != o.Quantity || int64(reservation.ConfigVersion) != o.OfferVersion {
		return &events.QuarantineError{Reason: "confirmed reservation terms mismatch"}
	}
	o.State = "CONFIRMED"
	o.Version++
	o.UpdatedAt = s.Now().Format(time.RFC3339Nano)
	body, _ := json.Marshal(o)
	_, err = tx.Exec(ctx, "UPDATE orders SET public_state='CONFIRMED',workflow_step='COMPLETE',aggregate_version=$2,snapshot=$3,payment_success=$4,payment_version=$5,updated_at=$6 WHERE order_id=$1", o.OrderID, o.Version, body, e.Data, p.Version, s.Now())
	if err != nil {
		return err
	}
	return eventstore.Enqueue(ctx, tx, envelope("OrderConfirmed", o.OrderID, o.Version, e.CorrelationID, s.Now(), o))
}
