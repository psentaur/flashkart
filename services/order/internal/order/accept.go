package order

import (
	"context"
	"encoding/json"
	"flash-sale/contracts/gen/inventory/v1"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/eventstore"
	"flash-sale/internal/platform/httpapi"
	"flash-sale/internal/platform/value"
	"github.com/jackc/pgx/v5"
	"time"
)

func (s Store) Submit(ctx context.Context, shopper, key, attempt, correlation string, p Purchase) (httpapi.Result, error) {
	ctx = httpapi.WithCorrelation(ctx, correlation)
	raw, _ := json.Marshal(p)
	if value.UUID(shopper) != nil || value.UUID(attempt) != nil || value.UUID(correlation) != nil || value.RequestKey(key) != nil || value.Validate("Purchase", raw) != nil {
		return errorFor(ctx, "INVALID_ARGUMENT", nil), nil
	}
	total, err := value.Total(p.ExpectedUnitPriceMinor, p.Quantity)
	if err != nil {
		return errorFor(ctx, "INVALID_ARGUMENT", nil), nil
	}
	i, replay, err := s.Claim(ctx, shopper, key, attempt, correlation, p)
	if err != nil {
		return httpapi.Result{}, err
	}
	if replay != nil {
		return *replay, nil
	}
	if i.Offer == nil {
		offer, err := s.Offer(ctx, p.SaleID, correlation)
		if err != nil {
			return httpapi.Result{}, err
		}
		b, _ := json.Marshal(offer)
		if value.Validate("Offer", b) != nil || offer.SaleID != p.SaleID || offer.ProductID != p.ProductID {
			return httpapi.Result{}, ErrOfferUnavailable
		}
		if offer.Version != p.ExpectedOfferVersion || offer.UnitPriceMinor != p.ExpectedUnitPriceMinor || offer.Currency != p.Currency {
			return s.reject(ctx, i, "OFFER_CHANGED", b)
		}
		if offer.Status == "UNAVAILABLE" {
			return s.reject(ctx, i, "SALE_NOT_READY", nil)
		}
		start, _ := time.Parse(time.RFC3339Nano, offer.StartsAt)
		end, _ := time.Parse(time.RFC3339Nano, offer.EndsAt)
		if offer.Status != "ACTIVE" || s.Now().Before(start) || !s.Now().Before(end) {
			return s.reject(ctx, i, "SALE_INACTIVE", nil)
		}
		i.Offer = &offer
		err = s.Outbox().Transaction(ctx, func(tx pgx.Tx) error {
			if err := s.fenced(ctx, tx, i); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "UPDATE purchase_intents SET trusted_offer=$2 WHERE order_id=$1", i.OrderID, b)
			return err
		})
		if err != nil {
			return httpapi.Result{}, err
		}
	}
	// Reclaimed workers replay Reserve with the original terms before any fresh offer checks.
	result, err := s.Inventory.Reserve(ctx, &inventoryv1.ReserveRequest{OrderId: i.OrderID, ShopperId: shopper, SaleId: p.SaleID, ProductId: p.ProductID, Quantity: uint32(p.Quantity), ConfigVersion: uint64(i.Offer.Version)})
	if err != nil {
		return httpapi.Result{}, err
	}
	switch result.Code {
	case inventoryv1.ResultCode_PURCHASE_LIMIT_EXCEEDED:
		return s.reject(ctx, i, "PURCHASE_LIMIT_EXCEEDED", nil)
	case inventoryv1.ResultCode_SOLD_OUT:
		return s.reject(ctx, i, "SOLD_OUT", nil)
	case inventoryv1.ResultCode_INSUFFICIENT_STOCK:
		return s.reject(ctx, i, "INSUFFICIENT_STOCK", nil)
	case inventoryv1.ResultCode_SALE_INACTIVE:
		return s.reject(ctx, i, "SALE_INACTIVE", nil)
	case inventoryv1.ResultCode_SALE_NOT_READY:
		return s.reject(ctx, i, "SALE_NOT_READY", nil)
	case inventoryv1.ResultCode_OFFER_VERSION_MISMATCH:
		return s.reject(ctx, i, "VERSION_CONFLICT", nil)
	case inventoryv1.ResultCode_PAYLOAD_MISMATCH:
		return s.reject(ctx, i, "IDEMPOTENCY_CONFLICT", nil)
	case inventoryv1.ResultCode_APPLIED, inventoryv1.ResultCode_REPLAYED:
	default:
		return httpapi.Result{}, ErrOfferUnavailable
	}
	r := result.Reservation
	if r == nil || r.OrderId != i.OrderID || r.ShopperId != shopper || r.SaleId != p.SaleID || r.ProductId != p.ProductID || int64(r.Quantity) != p.Quantity || int64(r.ConfigVersion) != i.Offer.Version || r.ExpiresAt == nil {
		return httpapi.Result{}, ErrOfferUnavailable
	}
	if r.State == inventoryv1.ReservationState_RELEASED || r.State == inventoryv1.ReservationState_EXPIRED {
		return s.reject(ctx, i, "SALE_INACTIVE", nil)
	}
	if r.State != inventoryv1.ReservationState_HELD || !s.Now().Before(r.ExpiresAt.AsTime()) {
		return errorFor(ctx, "REQUEST_IN_PROGRESS", nil), nil
	}
	now := s.Now()
	stamp := now.Format(time.RFC3339Nano)
	o := Order{OrderID: i.OrderID, ShopperID: shopper, SaleID: p.SaleID, ProductID: p.ProductID, ReservationID: r.ReservationId, Quantity: p.Quantity, OfferVersion: i.Offer.Version, UnitPriceMinor: i.Offer.UnitPriceMinor, Currency: i.Offer.Currency, TotalMinor: total, State: "PENDING_PAYMENT", RefundStatus: "NONE", ExpiresAt: r.ExpiresAt.AsTime().UTC().Format(time.RFC3339Nano), CreatedAt: stamp, UpdatedAt: stamp, Version: 1}
	body, _ := json.Marshal(o)
	if err = value.Validate("Order", body); err != nil {
		return httpapi.Result{}, err
	}
	payment := clock.UUID()
	response := orderResult(o)
	snapshot, _ := json.Marshal(response)
	err = s.Outbox().Transaction(ctx, func(tx pgx.Tx) error {
		if err := s.fenced(ctx, tx, i); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO orders(order_id,shopper_id,sale_id,product_id,reservation_id,payment_attempt_id,public_state,workflow_step,aggregate_version,snapshot,updated_at) VALUES($1,$2,$3,$4,$5,$6,'PENDING_PAYMENT','AWAITING_PAYMENT',1,$7,$8)`, o.OrderID, shopper, p.SaleID, p.ProductID, o.ReservationID, payment, body, now)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "UPDATE purchase_intents SET state='ACCEPTED',reservation_id=$2,response_snapshot=$3,lease_owner=NULL,lease_until=NULL,updated_at=$4 WHERE order_id=$1", i.OrderID, o.ReservationID, snapshot, now)
		if err != nil {
			return err
		}
		if err = eventstore.Enqueue(ctx, tx, envelope("OrderPending", o.OrderID, 1, i.CorrelationID, now, o)); err != nil {
			return err
		}
		return eventstore.Enqueue(ctx, tx, envelope("PaymentRequested", payment, 1, i.CorrelationID, now, events.PaymentRequested{AttemptID: payment, OrderID: o.OrderID, ReservationID: o.ReservationID, ShopperID: shopper, SaleID: p.SaleID, ProductID: p.ProductID, AmountMinor: total, Currency: o.Currency}))
	})
	return response, err
}
