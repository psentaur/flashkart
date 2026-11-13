package market

import (
	"context"
	"errors"
	"log/slog"
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

type Orders struct {
	pb.UnimplementedOrderServer
	DB        *pgxpool.Pool
	Inventory pb.InventoryClient
	Payment   pb.PaymentClient
	Log       *slog.Logger
}

func (o Orders) CreateFromReservation(ctx context.Context, req *pb.CreateOrderRequest) (*pb.OrderRef, error) {
	r, t := req.GetReservation(), req.GetTerms()
	if !validID(r.GetReservationId()) || r.GetReservationId() != r.GetAttemptId() || !validID(r.GetCustomerId()) ||
		!validID(r.GetSaleId()) || r.GetSaleId() != t.GetSaleId() || t.GetPriceMinor() < 0 || len(t.GetCurrency()) != 3 ||
		r.GetState() != pb.ReservationState_RESERVATION_STATE_HELD || !validID(req.GetCall().GetCorrelationId()) {
		return nil, status.Error(codes.InvalidArgument, "held reservation and matching sale terms required")
	}
	id := r.ReservationId
	data, err := proto.Marshal(req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid order request")
	}
	err = pgx.BeginFunc(ctx, o.DB, func(tx pgx.Tx) error {
		if err := lockAttempt(ctx, tx, "orders", id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO orders.purchases(order_id,customer_id,request) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, id, r.CustomerId, data); err != nil {
			return err
		}
		var stored []byte
		if err := tx.QueryRow(ctx, "SELECT request FROM orders.purchases WHERE order_id=$1", id).Scan(&stored); err != nil {
			return err
		}
		var previous pb.CreateOrderRequest
		if err := proto.Unmarshal(stored, &previous); err != nil {
			return err
		}
		if !proto.Equal(previous.Reservation, r) || !proto.Equal(previous.Terms, t) {
			return status.Error(codes.AlreadyExists, "reservation already has different order terms")
		}
		return nil
	})
	if err != nil {
		return nil, reservationError(err)
	}
	return &pb.OrderRef{OrderId: id}, nil
}

func (o Orders) Handler(token, customer string, fallback http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/orders/{order_id}", func(w http.ResponseWriter, r *http.Request) {
		supplied, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || !matchesToken(token, supplied) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, r, 401, "UNAUTHENTICATED", "customer bearer token required")
			return
		}
		id := r.PathValue("order_id")
		if !validID(id) {
			writeError(w, r, 400, "INVALID_ARGUMENT", "invalid order ID")
			return
		}
		var state string
		err := o.DB.QueryRow(r.Context(), "SELECT state FROM orders.purchases WHERE order_id=$1 AND customer_id=$2", id, customer).Scan(&state)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, r, 404, "NOT_FOUND", "order not found")
			return
		}
		if err != nil {
			writeError(w, r, 503, "UNAVAILABLE", "order lookup unavailable")
			return
		}
		send(w, 200, map[string]string{"order_id": id, "attempt_id": id, "state": state})
	})
	return serveAPI(mux, fallback)
}

func (o Orders) Run(ctx context.Context) {
	runWork(ctx, o.Log, "order", o.processNext)
}

func (o Orders) processNext(ctx context.Context) (bool, error) {
	var id string
	var data []byte
	var created time.Time
	err := o.DB.QueryRow(ctx, `WITH claim AS (
 SELECT order_id FROM orders.purchases WHERE state='PENDING' AND next_attempt_at<=clock_timestamp()
 ORDER BY next_attempt_at,order_id LIMIT 1 FOR UPDATE SKIP LOCKED
 ) UPDATE orders.purchases p SET next_attempt_at=clock_timestamp()+interval '6 seconds'
 FROM claim WHERE p.order_id=claim.order_id RETURNING p.order_id,p.request,p.created_at`).Scan(&id, &data, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var req pb.CreateOrderRequest
	if err := proto.Unmarshal(data, &req); err != nil {
		return false, err
	}
	return true, o.process(ctx, id, created, &req)
}

func (o Orders) process(ctx context.Context, id string, created time.Time, req *pb.CreateOrderRequest) error {
	call := &pb.Call{OperationId: id, CorrelationId: req.GetCall().GetCorrelationId()}
	reservation := &pb.ReservationRequest{Call: call, ReservationId: req.Reservation.ReservationId}
	paymentID := id
	payment, err := o.Payment.Query(ctx, &pb.PaymentRequest{Call: call, PaymentId: paymentID})
	if status.Code(err) == codes.NotFound {
		payment, err = o.Payment.Simulate(ctx, &pb.SimulatePaymentRequest{Call: call, PaymentId: paymentID, OrderId: id,
			AmountMinor: req.Terms.PriceMinor, Currency: req.Terms.Currency, Reservation: req.Reservation})
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	switch payment.GetState() {
	case pb.PaymentState_PAYMENT_STATE_PENDING:
		return nil
	case pb.PaymentState_PAYMENT_STATE_DECLINED:
		if _, err := o.Inventory.ReleaseReservation(ctx, reservation); err != nil && status.Code(err) != codes.FailedPrecondition {
			return err
		}
		return o.finish(ctx, id, created, req, "CANCELLED", "PAYMENT_DECLINED")
	case pb.PaymentState_PAYMENT_STATE_SUCCEEDED:
		_, err := o.Inventory.CommitReservation(ctx, reservation)
		if err == nil {
			return o.finish(ctx, id, created, req, "CONFIRMED", "")
		}
		if status.Code(err) != codes.FailedPrecondition {
			return err
		}
		if _, err := o.Payment.Refund(ctx, &pb.PaymentRequest{Call: call, PaymentId: paymentID}); err != nil {
			return err
		}
		fallthrough
	case pb.PaymentState_PAYMENT_STATE_REFUNDED:
		current, err := o.Inventory.InspectReservation(ctx, reservation)
		if err != nil {
			return err
		}
		return o.finish(ctx, id, created, req, "CANCELLED", reservationReason(current.State))
	default:
		return errors.New("unexpected payment state")
	}
}

func reservationReason(state pb.ReservationState) string {
	if state == pb.ReservationState_RESERVATION_STATE_RELEASED {
		return "RESERVATION_RELEASED"
	}
	return "RESERVATION_EXPIRED"
}

func (o Orders) finish(ctx context.Context, id string, created time.Time, req *pb.CreateOrderRequest, state, reason string) error {
	return pgx.BeginFunc(ctx, o.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL synchronous_commit=on"); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, "UPDATE orders.purchases SET state=$2 WHERE order_id=$1 AND state='PENDING'", id, state)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		var now time.Time
		if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
			return err
		}
		r := req.Reservation
		work := PurchaseWork{1, req.GetCall().GetCorrelationId(), now.UTC(), r.SaleId, r.AttemptId, r.CustomerId}
		name := "order.confirmed"
		payload := map[string]any{"latency_ms": max(int64(0), now.Sub(created).Milliseconds())}
		if state == "CANCELLED" {
			name = "order.cancelled"
			payload = map[string]any{"reason": reason}
		}
		event := purchaseEvent(work, name, r.ReservationId, payload)
		event["order_id"] = id
		if _, err := tx.Exec(ctx, "INSERT INTO orders.events VALUES ($1,$2::jsonb)", event["event_id"], event); err != nil {
			return err
		}
		outcome := terminalEvent(work, state, reason, id)
		_, err = tx.Exec(ctx, "INSERT INTO orders.outcomes VALUES ($1,$2::jsonb)", outcome["event_id"], outcome)
		return err
	})
}

func terminalEvent(work PurchaseWork, state, reason, orderID string) map[string]any {
	payload := map[string]any{"state": state}
	if reason != "" {
		payload["reason"] = reason
	}
	if orderID != "" {
		payload["order_id"] = orderID
	}
	return purchaseEvent(work, "attempt.terminal", "", payload)
}

func runWork(ctx context.Context, log *slog.Logger, name string, next func(context.Context) (bool, error)) {
	for ctx.Err() == nil {
		work, cancel := context.WithTimeout(ctx, 4*time.Second)
		processed, err := next(work)
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Warn("work will retry", "worker", name, "code", status.Code(err).String())
		}
		if !processed || err != nil {
			pause(ctx, 200*time.Millisecond)
		}
	}
}
