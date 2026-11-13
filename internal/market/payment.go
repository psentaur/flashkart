package market

import (
	"context"
	"errors"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type Payment struct {
	pb.UnimplementedPaymentServer
	DB      *pgxpool.Pool
	Fixture string
	Delay   time.Duration
}

func (p Payment) Simulate(ctx context.Context, req *pb.SimulatePaymentRequest) (*pb.PaymentResult, error) {
	r := req.GetReservation()
	if !validID(req.GetPaymentId()) || !validID(req.GetOrderId()) || !validID(r.GetReservationId()) ||
		!validID(r.GetAttemptId()) || !validID(r.GetSaleId()) || !validID(r.GetCustomerId()) || req.GetAmountMinor() < 0 || len(req.GetCurrency()) != 3 {
		return nil, status.Error(codes.InvalidArgument, "invalid payment")
	}
	return p.run(ctx, req.PaymentId, req, false)
}

func (p Payment) Query(ctx context.Context, req *pb.PaymentRequest) (*pb.PaymentResult, error) {
	return p.run(ctx, req.GetPaymentId(), nil, false)
}

func (p Payment) Refund(ctx context.Context, req *pb.PaymentRequest) (*pb.PaymentResult, error) {
	return p.run(ctx, req.GetPaymentId(), nil, true)
}

func (p Payment) run(ctx context.Context, id string, input *pb.SimulatePaymentRequest, refund bool) (*pb.PaymentResult, error) {
	if !validID(id) {
		return nil, status.Error(codes.InvalidArgument, "payment ID required")
	}
	var state string
	var lost bool
	err := pgx.BeginFunc(ctx, p.DB, func(tx pgx.Tx) error {
		if err := lockAttempt(ctx, tx, "payment", id); err != nil {
			return err
		}
		var encoded []byte
		var due, now time.Time
		var decision string
		var loseReply bool
		err := tx.QueryRow(ctx, `SELECT request,state,decision,due_at,lose_reply,clock_timestamp()
    FROM payment.transactions WHERE payment_id=$1`, id).Scan(&encoded, &state, &decision, &due, &loseReply, &now)
		if errors.Is(err, pgx.ErrNoRows) {
			if input == nil {
				return status.Error(codes.NotFound, "payment not found")
			}
			if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
				return err
			}
			decision = "SUCCEEDED"
			if p.Fixture == "decline" {
				decision = "DECLINED"
			}
			due = now
			if p.Fixture == "delay" {
				due = due.Add(p.Delay)
			}
			loseReply = p.Fixture == "lost-response"
			state = "PENDING"
			encoded, err = proto.Marshal(input)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "INSERT INTO payment.transactions VALUES ($1,$2,$3,$4,$5,$6)", id, encoded, state, decision, due, loseReply); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		var original pb.SimulatePaymentRequest
		if err := proto.Unmarshal(encoded, &original); err != nil {
			return err
		}
		if input != nil {
			a, b := proto.CloneOf(input), proto.CloneOf(&original)
			a.Call, b.Call = nil, nil
			if !proto.Equal(a, b) {
				return status.Error(codes.AlreadyExists, "payment ID already has different terms")
			}
		}
		next := state
		if refund {
			if state == "REFUNDED" {
				return nil
			}
			if state != "SUCCEEDED" {
				return status.Error(codes.FailedPrecondition, "payment must succeed before refund")
			}
			next = "REFUNDED"
		} else if state == "PENDING" && !now.Before(due) {
			next = decision
		}
		if next == state {
			return nil
		}
		if _, err := tx.Exec(ctx, "UPDATE payment.transactions SET state=$2,lose_reply=false WHERE payment_id=$1", id, next); err != nil {
			return err
		}
		r := original.Reservation
		work := PurchaseWork{1, original.GetCall().GetCorrelationId(), now.UTC(), r.SaleId, r.AttemptId, r.CustomerId}
		name := map[string]string{"SUCCEEDED": "payment.succeeded", "DECLINED": "payment.declined", "REFUNDED": "payment.refunded"}[next]
		event := purchaseEvent(work, name, r.ReservationId, map[string]any{})
		event["payment_id"], event["order_id"] = id, original.OrderId
		if _, err := tx.Exec(ctx, "INSERT INTO payment.events VALUES ($1,$2::jsonb)", event["event_id"], event); err != nil {
			return err
		}
		state = next
		lost = loseReply
		return nil
	})
	if err != nil {
		return nil, reservationError(err)
	}
	if lost {
		return nil, status.Error(codes.Unavailable, "simulated lost reply after payment commit")
	}
	return &pb.PaymentResult{PaymentId: id, State: pb.PaymentState(pb.PaymentState_value["PAYMENT_STATE_"+state])}, nil
}
