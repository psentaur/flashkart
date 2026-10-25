// Package events validates known-major event contracts before a handler sees them.
package events

import (
	"encoding/json"
	"flash-sale/internal/platform/value"
	"fmt"
	"strings"
	"time"
)

type Envelope struct {
	EventID          string          `json:"event_id"`
	SchemaVersion    int             `json:"schema_version"`
	Type             string          `json:"type"`
	OccurredAt       string          `json:"occurred_at"`
	Producer         string          `json:"producer"`
	AggregateID      string          `json:"aggregate_id"`
	AggregateVersion int64           `json:"aggregate_version"`
	CorrelationID    string          `json:"correlation_id"`
	Data             json.RawMessage `json:"data"`
}
type QuarantineError struct{ Reason string }

func (e *QuarantineError) Error() string { return "quarantine: " + e.Reason }

type routing struct{ producer, topic, aggregate, key string }

var routes = map[string]routing{
	"SaleConfigured": {"flash-sale", "sale.config.v1", "sale_id", "sale_id"}, "SaleActivated": {"flash-sale", "sale.config.v1", "sale_id", "sale_id"}, "SaleCancelled": {"flash-sale", "sale.config.v1", "sale_id", "sale_id"}, "SaleReady": {"", "sale.ready.v1", "sale_id", "sale_id"},
	"AttemptRecorded": {"admission", "analytics.activity.v1", "attempt_id", ""},
	"ReservationHeld": {"inventory", "inventory.events.v1", "reservation_id", "order_id"}, "ReservationConfirmed": {"inventory", "inventory.events.v1", "reservation_id", "order_id"}, "ReservationReleased": {"inventory", "inventory.events.v1", "reservation_id", "order_id"}, "ReservationExpired": {"inventory", "inventory.events.v1", "reservation_id", "order_id"},
	"PaymentRequested": {"order", "payment.requests.v1", "attempt_id", "order_id"}, "RefundRequested": {"order", "payment.requests.v1", "refund_id", "order_id"},
	"PaymentSucceeded": {"payment", "payment.events.v1", "attempt_id", "order_id"}, "PaymentFailed": {"payment", "payment.events.v1", "attempt_id", "order_id"}, "RefundCompleted": {"payment", "payment.events.v1", "refund_id", "order_id"},
	"OrderPending": {"order", "order.events.v1", "order_id", "order_id"}, "OrderConfirmed": {"order", "order.events.v1", "order_id", "order_id"}, "OrderFailed": {"order", "order.events.v1", "order_id", "order_id"}, "OrderExpired": {"order", "order.events.v1", "order_id", "order_id"}, "RefundPending": {"order", "order.events.v1", "refund_id", "order_id"},
	"AnalyticsHeartbeat": {"analytics", "analytics.activity.v1", "", ""}, "WindowSummaryUpdated": {"analytics", "analytics.results.v1", "summary_id", ""}, "LateActivityObserved": {"analytics", "analytics.late.v1", "", "original_event_id"},
}

func Decode(topic, key string, raw []byte) (Envelope, error) {
	var e Envelope
	reject := func(reason string) (Envelope, error) { return e, &QuarantineError{reason} }
	if err := json.Unmarshal(raw, &e); err != nil {
		return reject("malformed envelope")
	}
	r, ok := routes[e.Type]
	if !ok || e.SchemaVersion != 1 {
		return reject("unknown kind or major")
	}
	schema, err := value.Schema("events.schema.json", "", true)
	if err != nil {
		return e, err
	}
	doc, err := value.JSON(raw)
	if err != nil || schema.Validate(doc) != nil {
		return reject("invalid payload")
	}
	decoded, _ := value.JSON(e.Data)
	data := decoded.(map[string]any)
	if r.producer != "" && e.Producer != r.producer {
		return reject("unauthorized producer")
	}
	if e.Type == "SaleReady" && (data["participant"] != e.Producer || !strings.Contains("|catalog|admission|inventory|", "|"+e.Producer+"|")) {
		return reject("readiness producer")
	}
	if topic != r.topic && !(topic == "analytics.activity.v1" && r.producer != "analytics") {
		return reject("unauthorized topic")
	}
	if v, ok := data["version"].(json.Number); ok {
		n, err := v.Int64()
		if err != nil || n != e.AggregateVersion {
			return reject("aggregate version mismatch")
		}
	}
	if r.aggregate != "" && data[r.aggregate] != e.AggregateID {
		return reject("aggregate identity mismatch")
	}
	_, expected := DestinationFor(e, topic)
	if expected != key {
		return reject("topic key mismatch")
	}
	return e, nil
}
func Destination(e Envelope) (string, string) { r := routes[e.Type]; return DestinationFor(e, r.topic) }
func DestinationFor(e Envelope, topic string) (string, string) {
	r := routes[e.Type]
	var data map[string]any
	_ = json.Unmarshal(e.Data, &data)
	if topic == "analytics.activity.v1" || topic == "analytics.results.v1" {
		start, _ := data["window_start"].(string)
		if start == "" {
			t, _ := time.Parse(time.RFC3339Nano, e.OccurredAt)
			start = t.UTC().Truncate(time.Minute).Format(time.RFC3339)
		}
		return topic, fmt.Sprintf("%s:%s:%s", data["sale_id"], data["product_id"], start)
	}
	key, _ := data[r.key].(string)
	return topic, key
}
func Encode(e Envelope) ([]byte, error) {
	raw, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	topic, key := Destination(e)
	if _, err = Decode(topic, key, raw); err != nil {
		return nil, err
	}
	return raw, nil
}
