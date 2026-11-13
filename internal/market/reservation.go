package market

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (i Inventory) Reserve(ctx context.Context, work PurchaseWork) error {
	if !work.valid() {
		return status.Error(codes.InvalidArgument, "invalid purchase work")
	}
	return pgx.BeginFunc(ctx, i.DB, func(tx pgx.Tx) error {
		if err := lockAttempt(ctx, tx, "inventory", work.AttemptID); err != nil {
			return err
		}
		var sale, customer string
		err := tx.QueryRow(ctx, "SELECT sale_id,customer_id FROM inventory.attempts WHERE attempt_id=$1", work.AttemptID).Scan(&sale, &customer)
		if err == nil {
			if sale != work.SaleID || customer != work.CustomerID {
				return errAttemptConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var terms Terms
		var active bool
		var stock int64
		err = tx.QueryRow(ctx, "SELECT active,available_stock,terms FROM inventory.sales WHERE sale_id=$1 FOR UPDATE", work.SaleID).Scan(&active, &stock, &terms)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&work.OccurredAt); err != nil {
			return err
		}
		work.OccurredAt = work.OccurredAt.UTC()
		reason := "INACTIVE"
		if active {
			start, err1 := time.Parse(time.RFC3339Nano, terms.StartsAt)
			end, err2 := time.Parse(time.RFC3339Nano, terms.EndsAt)
			if err1 != nil || err2 != nil {
				return errors.New("invalid stored sale window")
			}
			switch {
			case !work.OccurredAt.Before(end):
				reason = "ENDED"
			case work.OccurredAt.Before(start):
			case stock == 0:
				reason = "SOLD_OUT"
			default:
				reason = ""
			}
		}
		if _, err = tx.Exec(ctx, "INSERT INTO inventory.attempts VALUES ($1,$2,$3,nullif($4,''))", work.AttemptID, work.SaleID, work.CustomerID, reason); err != nil {
			return err
		}
		if reason != "" {
			return inventoryEvent(ctx, tx, purchaseEvent(work, "attempt.rejected", "", map[string]any{"reason": reason}))
		}
		tag, err := tx.Exec(ctx, "UPDATE inventory.sales SET available_stock=available_stock-1 WHERE sale_id=$1 AND available_stock>0", work.SaleID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("stock decrement was not confirmed")
		}
		expires := work.OccurredAt.Add(2 * time.Minute)
		if _, err = tx.Exec(ctx, `INSERT INTO inventory.reservations VALUES ($1,$2,$3,$4,$5,'HELD')`,
			work.AttemptID, work.SaleID, work.CustomerID, work.TraceID, expires); err != nil {
			return err
		}
		reservation := &pb.Reservation{ReservationId: work.AttemptID, AttemptId: work.AttemptID, SaleId: work.SaleID, CustomerId: work.CustomerID,
			ExpiresAt: expires.Format(time.RFC3339Nano), State: pb.ReservationState_RESERVATION_STATE_HELD}
		order := &pb.CreateOrderRequest{Call: &pb.Call{OperationId: work.AttemptID, CorrelationId: work.TraceID},
			Reservation: reservation, Terms: (Sale{work.SaleID, terms}).protobuf()}
		if _, err = tx.Exec(ctx, "INSERT INTO inventory.order_work VALUES ($1,$2::jsonb)", work.AttemptID, order); err != nil {
			return err
		}
		return inventoryEvent(ctx, tx, purchaseEvent(work, "reservation.held", work.AttemptID, map[string]any{"expires_at": reservation.ExpiresAt}))
	})
}

func inventoryEvent(ctx context.Context, tx pgx.Tx, event map[string]any) error {
	_, err := tx.Exec(ctx, "INSERT INTO inventory.events VALUES ($1,$2::jsonb)", event["event_id"], event)
	return err
}

type rowReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func reservationRow(ctx context.Context, db rowReader, id, suffix string) (*pb.Reservation, string, error) {
	var result pb.Reservation
	var state, trace string
	var expires time.Time
	err := db.QueryRow(ctx, `SELECT reservation_id,sale_id,customer_id,expires_at,state,trace_id
		FROM inventory.reservations WHERE reservation_id=$1`+suffix, id).Scan(&result.ReservationId, &result.SaleId, &result.CustomerId, &expires, &state, &trace)
	if err != nil {
		return nil, "", err
	}
	result.AttemptId = id
	result.ExpiresAt = expires.UTC().Format(time.RFC3339Nano)
	result.State = pb.ReservationState(pb.ReservationState_value["RESERVATION_STATE_"+state])
	return &result, trace, nil
}

func reservationError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return status.Error(codes.NotFound, "reservation not found")
	}
	if s, ok := status.FromError(err); ok {
		return s.Err()
	}
	return status.Error(codes.Unavailable, "reservation operation could not be confirmed")
}

func (i Inventory) InspectReservation(ctx context.Context, req *pb.ReservationRequest) (*pb.Reservation, error) {
	if !validID(req.GetReservationId()) {
		return nil, status.Error(codes.InvalidArgument, "reservation ID is required")
	}
	result, _, err := reservationRow(ctx, i.DB, req.GetReservationId(), "")
	if err != nil {
		return nil, reservationError(err)
	}
	return result, nil
}

func (i Inventory) CommitReservation(ctx context.Context, req *pb.ReservationRequest) (*pb.Reservation, error) {
	return i.transition(ctx, req.GetReservationId(), "COMMITTED", pb.ReservationState_RESERVATION_STATE_COMMITTED)
}

func (i Inventory) ReleaseReservation(ctx context.Context, req *pb.ReservationRequest) (*pb.Reservation, error) {
	return i.transition(ctx, req.GetReservationId(), "RELEASED", pb.ReservationState_RESERVATION_STATE_RELEASED)
}

func (i Inventory) transition(ctx context.Context, id, state string, target pb.ReservationState) (*pb.Reservation, error) {
	if !validID(id) {
		return nil, status.Error(codes.InvalidArgument, "reservation ID is required")
	}
	var result *pb.Reservation
	err := pgx.BeginFunc(ctx, i.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL synchronous_commit=on"); err != nil {
			return err
		}
		current, trace, err := reservationRow(ctx, tx, id, " FOR UPDATE")
		if err != nil {
			return err
		}
		result = current
		if current.State == target {
			return nil
		}
		if current.State != pb.ReservationState_RESERVATION_STATE_HELD {
			return status.Error(codes.FailedPrecondition, "reservation is terminal")
		}
		var now time.Time
		if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
			return err
		}
		expires, err := time.Parse(time.RFC3339Nano, current.ExpiresAt)
		if err != nil {
			return err
		}
		if target == pb.ReservationState_RESERVATION_STATE_COMMITTED && !now.Before(expires) {
			return status.Error(codes.FailedPrecondition, "reservation has expired")
		}
		if _, err = tx.Exec(ctx, "UPDATE inventory.reservations SET state=$2 WHERE reservation_id=$1", id, state); err != nil {
			return err
		}
		if target == pb.ReservationState_RESERVATION_STATE_RELEASED {
			if _, err = tx.Exec(ctx, "UPDATE inventory.sales SET available_stock=available_stock+1 WHERE sale_id=$1", current.SaleId); err != nil {
				return err
			}
		}
		work := PurchaseWork{1, trace, now.UTC(), current.SaleId, id, current.CustomerId}
		if err := inventoryEvent(ctx, tx, purchaseEvent(work, "reservation."+strings.ToLower(state), id, map[string]any{})); err != nil {
			return err
		}
		current.State = target
		return nil
	})
	if err != nil {
		return nil, reservationError(err)
	}
	return result, nil
}

func (i Inventory) Consume(ctx context.Context, queue PurchaseQueue, log *slog.Logger) {
	for ctx.Err() == nil {
		poll, cancel := context.WithTimeout(ctx, 15*time.Second)
		messages, err := queue.Receive(poll)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("purchase queue receive will retry")
			}
			pause(ctx, time.Second)
			continue
		}
		for _, message := range messages {
			workCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := i.processMessage(workCtx, queue, message)
			cancel()
			if err != nil && ctx.Err() == nil {
				log.Warn("purchase message left for redelivery")
			}
		}
	}
}

func (i Inventory) processMessage(ctx context.Context, queue PurchaseQueue, message PurchaseMessage) error {
	var work PurchaseWork
	decoder := json.NewDecoder(strings.NewReader(message.Body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&work); err != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid purchase work message")
	}
	if err := i.Reserve(ctx, work); err != nil {
		return err
	}
	return queue.Delete(ctx, message.Receipt)
}
