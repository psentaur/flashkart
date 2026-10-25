package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/eventstore"
	"flash-sale/internal/platform/fault"
	"fmt"
	"github.com/IBM/sarama"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

func RetryDelay(completed int) (time.Duration, bool) {
	if completed < 0 || completed >= 5 {
		return 0, false
	}
	return time.Second * time.Duration(1<<completed), true
}

type FailedWork struct {
	ID                          int64
	Consumer, EventID, Topic    string
	Partition                   int32
	Offset                      int64
	Key, Payload                []byte
	Headers                     []*sarama.RecordHeader
	ErrorCode                   string
	Attempts                    int
	State                       string
	NextAt, FirstSeen, LastSeen time.Time
	Timestamp                   time.Time
}
type RetryStore struct {
	Fault fault.Injector
	Pool  *pgxpool.Pool
	Clock clock.Clock
}

func (s RetryStore) now() time.Time {
	if s.Clock == nil {
		return time.Now().UTC()
	}
	return s.Clock.Now()
}

// Schedule persists original bytes and failure metadata before a consumer may acknowledge.
func (s RetryStore) Schedule(ctx context.Context, consumer, eventID, code string, m *sarama.ConsumerMessage, invalid, outage bool) error {
	now := s.now()
	state := "RETRY"
	delay, _ := RetryDelay(0)
	if invalid {
		state = "QUARANTINED"
	}
	if outage {
		state = "PAUSED"
	}
	headers, e := json.Marshal(struct {
		Headers   []*sarama.RecordHeader `json:"headers"`
		Timestamp time.Time              `json:"timestamp"`
	}{m.Headers, m.Timestamp})
	if e != nil {
		return e
	}
	_, e = s.Pool.Exec(ctx, `INSERT INTO platform_failed_work(consumer_name,event_id,original_topic,original_partition,original_offset,record_key,payload,headers,error_code,state,next_attempt_at,first_seen_at,last_seen_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12) ON CONFLICT(consumer_name,original_topic,original_partition,original_offset) DO NOTHING`, consumer, eventID, m.Topic, m.Partition, m.Offset, m.Key, m.Value, headers, code, state, now.Add(delay), now)
	return e
}

const columns = `id,consumer_name,event_id,original_topic,original_partition,original_offset,record_key,payload,headers,error_code,attempts,state,next_attempt_at,first_seen_at,last_seen_at`

func scanWork(row pgx.Row) (FailedWork, error) {
	var f FailedWork
	var headers []byte
	e := row.Scan(&f.ID, &f.Consumer, &f.EventID, &f.Topic, &f.Partition, &f.Offset, &f.Key, &f.Payload, &headers, &f.ErrorCode, &f.Attempts, &f.State, &f.NextAt, &f.FirstSeen, &f.LastSeen)
	if e != nil {
		return f, e
	}
	var metadata struct {
		Headers   []*sarama.RecordHeader `json:"headers"`
		Timestamp time.Time              `json:"timestamp"`
	}
	e = json.Unmarshal(headers, &metadata)
	if e != nil {
		e = json.Unmarshal(headers, &f.Headers)
	} else {
		f.Headers = metadata.Headers
		f.Timestamp = metadata.Timestamp
	}
	return f, e
}
func (s RetryStore) Inspect(ctx context.Context, id int64) (FailedWork, error) {
	return scanWork(s.Pool.QueryRow(ctx, "SELECT "+columns+" FROM platform_failed_work WHERE id=$1", id))
}

// RunOne claims work durably, then releases the connection before the inbox
// handler runs. A crash after its business commit replays through the inbox;
// the fencing token prevents an expired claimant changing a newer result.
func (s RetryStore) RunOne(ctx context.Context, consumer string, handle func(context.Context, FailedWork) error, outage func(error) bool) (bool, error) {
	token := clock.UUID()
	now := s.now()
	f, e := scanWork(s.Pool.QueryRow(ctx, `UPDATE platform_failed_work SET claim_token=$3,claim_until=$4 WHERE id=(SELECT id FROM platform_failed_work WHERE consumer_name=$1 AND state='RETRY' AND next_attempt_at<=$2 AND (claim_until IS NULL OR claim_until<=$2) ORDER BY next_attempt_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING `+columns, consumer, now, token, now.Add(30*time.Second)))
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	workCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	err := handle(workCtx, f)
	if err == nil && workCtx.Err() != nil {
		err = workCtx.Err()
	}
	cancel()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if s.Fault != nil {
		if e = s.Fault.Hit(ctx, "after_retry_handle"); e != nil {
			return false, e
		}
	}
	state := "REPLAYED"
	attempts := f.Attempts + 1
	next := s.now()
	if err != nil {
		state = "QUARANTINED"
		var invalid *events.QuarantineError
		if !errors.As(err, &invalid) && outage != nil && outage(err) {
			state = "PAUSED"
			attempts = f.Attempts
		} else if !errors.As(err, &invalid) {
			if delay, ok := RetryDelay(attempts); ok {
				state = "RETRY"
				next = next.Add(delay)
			}
		}
	}
	tag, e := s.Pool.Exec(ctx, "UPDATE platform_failed_work SET state=$2,attempts=$3,next_attempt_at=$4,last_seen_at=$5,claim_token=NULL,claim_until=NULL WHERE id=$1 AND claim_token=$6", f.ID, state, attempts, next, s.now(), token)
	if e != nil {
		return false, e
	}
	if tag.RowsAffected() != 1 {
		return false, fmt.Errorf("retry claim superseded")
	}
	return true, nil
}
func (s RetryStore) Resume(ctx context.Context, consumer string) error {
	_, e := s.Pool.Exec(ctx, "UPDATE platform_failed_work SET state='RETRY',next_attempt_at=$2 WHERE consumer_name=$1 AND state='PAUSED'", consumer, s.now())
	return e
}
func (s RetryStore) Replay(ctx context.Context, id int64, operator auth.Principal, reason string) error {
	if operator.Role != "OPERATOR" || operator.Subject == "" || len(reason) < 1 || len(reason) > 1000 {
		return fmt.Errorf("authorized operator and reason required")
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	tag, e := tx.Exec(ctx, "UPDATE platform_failed_work SET state='RETRY',attempts=0,next_attempt_at=$2,last_seen_at=$2,claim_token=NULL,claim_until=NULL WHERE id=$1 AND state IN ('QUARANTINED','PAUSED')", id, s.now())
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("work is not quarantined or paused")
	}
	if _, e = tx.Exec(ctx, "INSERT INTO platform_replay_audit(failed_work_id,operator_id,reason,replayed_at) VALUES($1,$2,$3,$4)", id, operator.Subject, reason, s.now()); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// DurableHandler acknowledges only after business/inbox/outbox commit or a durable
// retry/quarantine record. Raw payloads never enter logs or fabricated identities.
func DurableHandler(store eventstore.Store, retries RetryStore, consumer string, handle func(context.Context, pgx.Tx, events.Envelope) error, outage func(error) bool) Handler {
	return func(ctx context.Context, m *sarama.ConsumerMessage) error {
		en, e := events.Decode(m.Topic, string(m.Key), m.Value)
		if e != nil {
			return retries.Schedule(ctx, consumer, en.EventID, "INVALID_EVENT", m, true, false)
		}
		_, e = store.Consume(ctx, consumer, en.EventID, m.Value, func(tx pgx.Tx) error { return handle(ctx, tx, en) })
		if e == nil {
			return nil
		}
		var invalid *events.QuarantineError
		quarantined := errors.As(e, &invalid)
		paused := !quarantined && outage != nil && outage(e)
		if saved := retries.Schedule(ctx, consumer, en.EventID, "HANDLER_FAILED", m, quarantined, paused); saved != nil {
			return saved
		}
		if paused {
			return fmt.Errorf("dependency outage: intake paused")
		}
		return nil
	}
}
