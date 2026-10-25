// Package doubles implements scripted contract responses, never a stock/payment engine.
package doubles

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flash-sale/contracts/gen/inventory/v1"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/fault"
	"flash-sale/internal/platform/httpapi"
	"flash-sale/internal/platform/value"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"net/http"
	"sync"
	"time"
)

type reserveFixture struct {
	called  bool
	request *inventoryv1.ReserveRequest
	result  *inventoryv1.OperationResult
}
type Inventory struct {
	inventoryv1.UnimplementedInventoryReservationServer
	mu       sync.Mutex
	fixtures map[string]reserveFixture
	Fault    fault.Injector
	Clock    clock.Clock
}

func NewInventory() *Inventory {
	return &Inventory{fixtures: map[string]reserveFixture{}, Fault: fault.None{}, Clock: clock.Real{}}
}
func (i *Inventory) Script(req *inventoryv1.ReserveRequest, result *inventoryv1.OperationResult) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.fixtures[req.OrderId] = reserveFixture{request: proto.Clone(req).(*inventoryv1.ReserveRequest), result: proto.Clone(result).(*inventoryv1.OperationResult)}
}

// SetTerminal explicitly advances fixture state; it does not run an expiry or stock ledger.
func (i *Inventory) SetTerminal(orderID string, state inventoryv1.ReservationState) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	f, ok := i.fixtures[orderID]
	if !ok || f.result.Reservation == nil {
		return fmt.Errorf("missing fixture")
	}
	f.result.Reservation.State = state
	f.result.Reservation.Version++
	return nil
}

// Cleanup advances a scripted terminal state only after explicit controlled time and cleanup gates.
func (i *Inventory) Cleanup(ctx context.Context, order string, state inventoryv1.ReservationState, due time.Time) error {
	c := i.Clock
	if c == nil {
		c = clock.Real{}
	}
	if c.Now().Before(due) {
		return fmt.Errorf("fixture cleanup time not reached")
	}
	if e := i.Fault.Hit(ctx, "cleanup"); e != nil {
		return e
	}
	return i.SetTerminal(order, state)
}
func (i *Inventory) Reserve(ctx context.Context, r *inventoryv1.ReserveRequest) (*inventoryv1.OperationResult, error) {
	if e := i.Fault.Hit(ctx, "reserve"); e != nil {
		return nil, e
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	f, ok := i.fixtures[r.OrderId]
	if !ok {
		return nil, status.Error(codes.NotFound, "unscripted Reserve")
	}
	if !proto.Equal(r, f.request) {
		return &inventoryv1.OperationResult{Code: inventoryv1.ResultCode_PAYLOAD_MISMATCH}, nil
	}
	result := proto.Clone(f.result).(*inventoryv1.OperationResult)
	if f.called && result.Code == inventoryv1.ResultCode_APPLIED {
		result.Code = inventoryv1.ResultCode_REPLAYED
	}
	f.called = true
	i.fixtures[r.OrderId] = f
	return result, nil
}
func (i *Inventory) GetReservation(ctx context.Context, r *inventoryv1.GetReservationRequest) (*inventoryv1.Reservation, error) {
	if e := i.Fault.Hit(ctx, "get"); e != nil {
		return nil, e
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, f := range i.fixtures {
		v := f.result.Reservation
		if v != nil && ((r.GetOrderId() != "" && r.GetOrderId() == v.OrderId) || (r.GetReservationId() != "" && r.GetReservationId() == v.ReservationId)) {
			return proto.Clone(v).(*inventoryv1.Reservation), nil
		}
	}
	return nil, status.Error(codes.NotFound, "unscripted reservation")
}
func (i *Inventory) transition(ctx context.Context, r *inventoryv1.TransitionRequest, name string) (*inventoryv1.OperationResult, error) {
	if e := i.Fault.Hit(ctx, name); e != nil {
		return nil, e
	}
	v, e := i.GetReservation(ctx, &inventoryv1.GetReservationRequest{Lookup: &inventoryv1.GetReservationRequest_OrderId{OrderId: r.OrderId}})
	if e != nil {
		return nil, e
	}
	if v.ReservationId != r.ReservationId {
		return &inventoryv1.OperationResult{Code: inventoryv1.ResultCode_PAYLOAD_MISMATCH}, nil
	}
	code := inventoryv1.ResultCode_ALREADY_FINAL
	if (name == "confirm" && v.State == inventoryv1.ReservationState_CONFIRMED) || (name == "release" && v.State == inventoryv1.ReservationState_RELEASED) {
		code = inventoryv1.ResultCode_REPLAYED
	}
	if v.State == inventoryv1.ReservationState_EXPIRED {
		code = inventoryv1.ResultCode_RESERVATION_EXPIRED
	}
	if v.State == inventoryv1.ReservationState_HELD {
		return nil, status.Error(codes.FailedPrecondition, "script terminal result before transition")
	}
	return &inventoryv1.OperationResult{Code: code, Reservation: v}, nil
}
func (i *Inventory) Confirm(ctx context.Context, r *inventoryv1.TransitionRequest) (*inventoryv1.OperationResult, error) {
	return i.transition(ctx, r, "confirm")
}
func (i *Inventory) Release(ctx context.Context, r *inventoryv1.TransitionRequest) (*inventoryv1.OperationResult, error) {
	return i.transition(ctx, r, "release")
}

type Response struct {
	Status int
	Body   json.RawMessage
}
type REST struct {
	Verifier  auth.Verifier
	Fault     fault.Injector
	mu        sync.Mutex
	responses map[string]Response
	notices   map[string][]json.RawMessage
}

func NewREST(v auth.Verifier) *REST {
	return &REST{Verifier: v, Fault: fault.None{}, responses: map[string]Response{}, notices: map[string][]json.RawMessage{}}
}
func (r *REST) Script(path string, status int, body json.RawMessage) error {
	if path != "/v1/products" {
		const prefix = "/v1/products/"
		if len(path) <= len(prefix) || path[:len(prefix)] != prefix || value.UUID(path[len(prefix):]) != nil {
			return fmt.Errorf("only Catalog read paths may be scripted")
		}
	}
	if _, e := value.JSON(body); e != nil {
		return e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.responses[path] = Response{status, append(json.RawMessage(nil), body...)}
	return nil
}
func (r *REST) Notice(shopper string, body json.RawMessage) error {
	if value.Validate("Notice", body) != nil {
		return fmt.Errorf("invalid notice")
	}
	var v map[string]any
	_ = json.Unmarshal(body, &v)
	if v["shopper_id"] != shopper {
		return fmt.Errorf("notice owner mismatch")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notices[shopper] = append(r.notices[shopper], append(json.RawMessage(nil), body...))
	return nil
}
func (r *REST) ServeHTTP(w http.ResponseWriter, q *http.Request) {
	if q.URL.Path == "/health/ready" {
		httpapi.WriteJSON(w, 200, map[string]string{"mode": "fixture"})
		return
	}
	if e := r.Fault.Hit(q.Context(), "http"); e != nil {
		httpapi.WriteError(w, q, "DEPENDENCY_UNAVAILABLE", "scripted failure", nil)
		return
	}
	if q.Method != "GET" {
		httpapi.WriteError(w, q, "NOT_FOUND", "no fixture route", nil)
		return
	}
	if q.URL.Path == "/v1/notifications" || q.URL.Path == "/internal/notifications" {
		p, e := r.Verifier.Request(q)
		if e != nil {
			httpapi.WriteError(w, q, "UNAUTHENTICATED", "fixture identity required", nil)
			return
		}
		r.mu.Lock()
		items := append([]json.RawMessage{}, r.notices[p.Subject]...)
		r.mu.Unlock()
		httpapi.WriteJSON(w, 200, map[string]any{"items": items, "next_cursor": nil})
		return
	}
	r.mu.Lock()
	response, ok := r.responses[q.URL.Path]
	r.mu.Unlock()
	if !ok {
		httpapi.WriteError(w, q, "NOT_FOUND", "unscripted Catalog response", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.Status)
	_, _ = w.Write(response.Body)
}

// Payment replays a predefined result for matching commands; no charge simulation runs here.
type Payment struct {
	Fault    fault.Injector
	mu       sync.Mutex
	commands map[string][32]byte
	results  map[string]events.Envelope
}

func NewPayment() *Payment {
	return &Payment{Fault: fault.None{}, commands: map[string][32]byte{}, results: map[string]events.Envelope{}}
}
func (p *Payment) Script(command, result events.Envelope) error {
	if command.Type != "PaymentRequested" && command.Type != "RefundRequested" {
		return fmt.Errorf("not a payment command")
	}
	raw, e := events.Encode(command)
	if e != nil {
		return e
	}
	if _, e = events.Encode(result); e != nil {
		return e
	}
	var input, output map[string]any
	_ = json.Unmarshal(command.Data, &input)
	_ = json.Unmarshal(result.Data, &output)
	for _, field := range []string{"order_id", "shopper_id", "sale_id", "product_id", "amount_minor", "currency", "attempt_id"} {
		if input[field] != output[field] {
			return fmt.Errorf("payment fixture identity/terms mismatch")
		}
	}
	if command.Type == "PaymentRequested" && (result.Type != "PaymentSucceeded" && result.Type != "PaymentFailed") {
		return fmt.Errorf("payment result kind mismatch")
	}
	if command.Type == "RefundRequested" && (result.Type != "RefundCompleted" || input["refund_id"] != output["refund_id"]) {
		return fmt.Errorf("refund result mismatch")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commands[command.EventID] = sha256.Sum256(raw)
	p.results[command.EventID] = result
	return nil
}
func (p *Payment) Handle(command events.Envelope) (events.Envelope, error) {
	raw, e := events.Encode(command)
	if e != nil {
		return events.Envelope{}, e
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	sum, ok := p.commands[command.EventID]
	if !ok || sum != sha256.Sum256(raw) {
		return events.Envelope{}, fmt.Errorf("unscripted or mismatched command")
	}
	return p.results[command.EventID], nil
}

// Deliver permits explicit delay/failure injection without rerandomizing a result.
func (p *Payment) Deliver(ctx context.Context, command events.Envelope) (events.Envelope, error) {
	if p.Fault != nil {
		if e := p.Fault.Hit(ctx, "payment"); e != nil {
			return events.Envelope{}, e
		}
	}
	return p.Handle(command)
}
