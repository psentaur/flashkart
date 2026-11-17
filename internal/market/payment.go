package market

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// DeclineAttemptPrefix marks a purchase attempt whose simulated card payment is declined.
// The storefront offers it as a "declined card" option, like a payment provider's test card.
const DeclineAttemptPrefix = "decline-"

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
			if p.Fixture == "decline" || strings.HasPrefix(input.GetReservation().GetAttemptId(), DeclineAttemptPrefix) {
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
		if _, err := tx.Exec(ctx, "INSERT INTO payment.events(event_id,body) VALUES ($1,$2::jsonb)", event["event_id"], event); err != nil {
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

// Handler serves the customer's view of a payment: GET /api/payments/{payment_id}.
// The payment ID equals the order and attempt ID.
func (p Payment) Handler(token, customer string, fallback http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/payments/{payment_id}", func(w http.ResponseWriter, r *http.Request) {
		if !customerAuthorized(w, r, token) {
			return
		}
		id := r.PathValue("payment_id")
		if !validID(id) {
			writeError(w, r, 400, "INVALID_ARGUMENT", "invalid payment ID")
			return
		}
		var encoded []byte
		var state string
		err := p.DB.QueryRow(r.Context(), "SELECT request,state FROM payment.transactions WHERE payment_id=$1", id).Scan(&encoded, &state)
		var request pb.SimulatePaymentRequest
		if err == nil {
			err = proto.Unmarshal(encoded, &request)
		}
		if errors.Is(err, pgx.ErrNoRows) || err == nil && request.GetReservation().GetCustomerId() != customer {
			writeError(w, r, 404, "NOT_FOUND", "payment not found")
			return
		}
		if err != nil {
			writeError(w, r, 503, "UNAVAILABLE", "payment lookup unavailable")
			return
		}
		send(w, 200, map[string]any{"payment_id": id, "order_id": request.GetOrderId(), "state": state,
			"amount_minor": request.GetAmountMinor(), "currency": request.GetCurrency(),
			"simulated_decline": strings.HasPrefix(request.GetReservation().GetAttemptId(), DeclineAttemptPrefix)})
	})
	return serveAPI(mux, fallback)
}
