// Package inventory is only Order's authenticated client, not a reservation engine.
package inventory

import (
	"context"
	"flash-sale/contracts/gen/inventory/v1"
	"flash-sale/internal/platform/httpapi"
	"flash-sale/internal/platform/value"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"time"
)

type Client struct {
	RPC inventoryv1.InventoryReservationClient
}

func New(c grpc.ClientConnInterface) Client {
	return Client{inventoryv1.NewInventoryReservationClient(c)}
}
func callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx = httpapi.WithCorrelation(ctx, httpapi.Correlation(ctx))
	c, cancel := httpapi.Budget(ctx, 1500*time.Millisecond)
	return metadata.AppendToOutgoingContext(c, "x-correlation-id", httpapi.Correlation(ctx)), cancel
}
func Validate(r *inventoryv1.Reservation) error {
	if r == nil || value.UUID(r.ReservationId) != nil || value.UUID(r.OrderId) != nil || value.UUID(r.ShopperId) != nil || value.UUID(r.SaleId) != nil || value.UUID(r.ProductId) != nil || r.Quantity < 1 || r.Quantity > 100 || r.ConfigVersion < 1 || r.Version < 1 || r.CreatedAt == nil || r.ExpiresAt == nil || r.CreatedAt.CheckValid() != nil || r.ExpiresAt.CheckValid() != nil || !r.CreatedAt.AsTime().Before(r.ExpiresAt.AsTime()) || r.State < inventoryv1.ReservationState_HELD || r.State > inventoryv1.ReservationState_EXPIRED {
		return fmt.Errorf("invalid reservation response")
	}
	return nil
}
func result(r *inventoryv1.OperationResult, err error) (*inventoryv1.OperationResult, error) {
	if err != nil {
		return nil, err
	}
	if r == nil || r.Code < inventoryv1.ResultCode_APPLIED || r.Code > inventoryv1.ResultCode_PURCHASE_LIMIT_EXCEEDED {
		return nil, fmt.Errorf("invalid Inventory result")
	}
	if r.Code == inventoryv1.ResultCode_APPLIED || r.Code == inventoryv1.ResultCode_REPLAYED || r.Code == inventoryv1.ResultCode_ALREADY_FINAL || r.Code == inventoryv1.ResultCode_RESERVATION_EXPIRED {
		if err = Validate(r.Reservation); err != nil {
			return nil, err
		}
	} else if r.Reservation != nil {
		return nil, fmt.Errorf("rejection unexpectedly contains a hold")
	}
	return r, nil
}
func (c Client) Reserve(ctx context.Context, r *inventoryv1.ReserveRequest) (*inventoryv1.OperationResult, error) {
	ctx, cancel := callContext(ctx)
	defer cancel()
	v, err := result(c.RPC.Reserve(ctx, r))
	if err == nil && v.Reservation != nil {
		a := v.Reservation
		if a.OrderId != r.OrderId || a.ShopperId != r.ShopperId || a.SaleId != r.SaleId || a.ProductId != r.ProductId || a.Quantity != r.Quantity || a.ConfigVersion != r.ConfigVersion {
			return nil, fmt.Errorf("reservation terms mismatch")
		}
	}
	return v, err
}
func (c Client) Get(ctx context.Context, order string) (*inventoryv1.Reservation, error) {
	ctx, cancel := callContext(ctx)
	defer cancel()
	r, err := c.RPC.GetReservation(ctx, &inventoryv1.GetReservationRequest{Lookup: &inventoryv1.GetReservationRequest_OrderId{OrderId: order}})
	if err != nil {
		return nil, err
	}
	if err = Validate(r); err != nil {
		return nil, err
	}
	if r.OrderId != order {
		return nil, fmt.Errorf("reservation lookup mismatch")
	}
	return r, nil
}
func (c Client) transition(ctx context.Context, order, id string, confirm bool) (*inventoryv1.OperationResult, error) {
	ctx, cancel := callContext(ctx)
	defer cancel()
	req := &inventoryv1.TransitionRequest{OrderId: order, ReservationId: id}
	var r *inventoryv1.OperationResult
	var err error
	if confirm {
		r, err = c.RPC.Confirm(ctx, req)
	} else {
		r, err = c.RPC.Release(ctx, req)
	}
	r, err = result(r, err)
	if err == nil && (r.Reservation == nil || r.Reservation.OrderId != order || r.Reservation.ReservationId != id) {
		return nil, fmt.Errorf("reservation transition mismatch")
	}
	return r, err
}
func (c Client) Confirm(ctx context.Context, order, id string) (*inventoryv1.OperationResult, error) {
	return c.transition(ctx, order, id, true)
}
func (c Client) Release(ctx context.Context, order, id string) (*inventoryv1.OperationResult, error) {
	return c.transition(ctx, order, id, false)
}
