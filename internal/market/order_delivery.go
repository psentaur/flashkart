package market

import (
	"context"
	"errors"
	"log/slog"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

func (i Inventory) DeliverOrders(ctx context.Context, client pb.OrderClient, log *slog.Logger) {
	runWork(ctx, log, "order delivery", func(ctx context.Context) (bool, error) { return i.deliverNext(ctx, client) })
}

func (i Inventory) deliverNext(ctx context.Context, client pb.OrderClient) (bool, error) {
	var id string
	var body []byte
	err := i.DB.QueryRow(ctx, `WITH claim AS (
 SELECT reservation_id FROM inventory.order_work WHERE delivered_at IS NULL AND next_attempt_at<=clock_timestamp()
 ORDER BY next_attempt_at,reservation_id LIMIT 1 FOR UPDATE SKIP LOCKED
 ) UPDATE inventory.order_work w SET next_attempt_at=clock_timestamp()+interval '6 seconds'
 FROM claim WHERE w.reservation_id=claim.reservation_id RETURNING w.reservation_id,w.body`).Scan(&id, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var req pb.CreateOrderRequest
	if err := protojson.Unmarshal(body, &req); err != nil {
		return false, err
	}
	// Phase 6 persisted enum numbers and snake_case JSON, both accepted by protojson.
	if _, err := client.CreateFromReservation(ctx, &req); err != nil {
		return false, err
	}
	_, err = i.DB.Exec(ctx, "UPDATE inventory.order_work SET delivered_at=clock_timestamp() WHERE reservation_id=$1", id)
	return true, err
}

func (i Inventory) SweepExpired(ctx context.Context, log *slog.Logger, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for ctx.Err() == nil {
		work, cancel := context.WithTimeout(ctx, 4*time.Second)
		err := i.expireDue(work)
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Warn("reservation expiry sweep will retry")
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (i Inventory) expireDue(ctx context.Context) error {
	rows, err := i.DB.Query(ctx, `SELECT reservation_id FROM inventory.reservations WHERE state='HELD'
 AND expires_at<=clock_timestamp() ORDER BY expires_at,reservation_id LIMIT 100`)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := i.transition(ctx, id, "EXPIRED", pb.ReservationState_RESERVATION_STATE_EXPIRED); err != nil &&
			status.Code(err) != codes.FailedPrecondition {
			return err
		}
	}
	return nil
}
