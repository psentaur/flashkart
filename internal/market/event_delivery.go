package market

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EventDelivery struct {
	db     *pgxpool.Pool
	source string
	send   func(context.Context, string, string) error
	log    *slog.Logger
}

func NewEventDelivery(db *pgxpool.Pool, source string, send func(context.Context, string, string) error, log *slog.Logger) (EventDelivery, error) {
	switch source {
	case "admission.events", "inventory.events", "inventory.outcomes", "orders.events", "orders.outcomes", "payment.events":
		return EventDelivery{db, source, send, log}, nil
	default:
		return EventDelivery{}, errors.New("unknown event source")
	}
}

func (d EventDelivery) Run(ctx context.Context) { runWork(ctx, d.log, d.source, d.next) }

func (d EventDelivery) next(ctx context.Context) (bool, error) {
	var id, sale, body string
	var attempt int
	var lease time.Time
	err := d.db.QueryRow(ctx, fmt.Sprintf(`WITH claim AS (
 SELECT event_id FROM %[1]s WHERE delivered_at IS NULL AND dead_at IS NULL AND next_attempt_at<=clock_timestamp()
 ORDER BY next_attempt_at,event_id LIMIT 1 FOR UPDATE SKIP LOCKED
 ) UPDATE %[1]s o SET next_attempt_at=clock_timestamp()+interval '15 seconds',
 delivery_attempts=delivery_attempts+1
 FROM claim WHERE o.event_id=claim.event_id
 RETURNING o.event_id,coalesce(o.body->>'sale_id',''),o.body::text,o.delivery_attempts,o.next_attempt_at`, d.source)).Scan(&id, &sale, &body, &attempt, &lease)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	err = d.send(ctx, sale, body)
	if err == nil {
		_, err = d.db.Exec(ctx, "UPDATE "+d.source+" SET delivered_at=clock_timestamp() WHERE event_id=$1 AND next_attempt_at=$2 AND delivered_at IS NULL AND dead_at IS NULL", id, lease)
	} else if attempt >= 10 {
		// A separate bounded context records the failure even when the send timed out.
		record, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_, markErr := d.db.Exec(record, "UPDATE "+d.source+" SET dead_at=clock_timestamp() WHERE event_id=$1 AND next_attempt_at=$2 AND delivered_at IS NULL AND dead_at IS NULL", id, lease)
		err = errors.Join(err, markErr)
	}
	return true, err
}

func (d EventDelivery) Replay(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" || len(id) > 512 || strings.ContainsRune(id, 0) {
		return errors.New("event ID required")
	}
	tag, err := d.db.Exec(ctx, "UPDATE "+d.source+" SET dead_at=NULL,delivery_attempts=0,next_attempt_at=clock_timestamp() WHERE event_id=$1 AND dead_at IS NOT NULL AND delivered_at IS NULL", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("dead event not found")
	}
	return nil
}

func (d EventDelivery) Metrics(ctx context.Context, w io.Writer) {
	var pending, dead int64
	err := d.db.QueryRow(ctx, "SELECT count(*) FILTER (WHERE dead_at IS NULL),count(*) FILTER (WHERE dead_at IS NOT NULL) FROM "+d.source+" WHERE delivered_at IS NULL").Scan(&pending, &dead)
	if err == nil {
		fmt.Fprintf(w, "flashkart_outbox_pending{source=%q} %d\nflashkart_outbox_dead{source=%q} %d\n", d.source, pending, d.source, dead)
	}
}
