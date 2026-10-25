//go:build integration

package integration

import (
	"context"
	"errors"
	"flash-sale/internal/platform/auth"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/eventstore"
	"flash-sale/internal/platform/messaging"
	"github.com/IBM/sarama"
	"testing"
	"time"
)

func TestRetryQuarantineAndAuditedReplay(t *testing.T) {
	ctx := context.Background()
	p := database(t)
	if e := eventstore.Migrate(ctx, p); e != nil {
		t.Fatal(e)
	}
	c := clock.NewManual(time.Now())
	s := messaging.RetryStore{Pool: p, Clock: c}
	name := "test-" + clock.UUID()
	m := &sarama.ConsumerMessage{Topic: "original", Partition: 1, Offset: 42, Timestamp: c.Now(), Key: []byte("key"), Value: []byte("unchanged"), Headers: []*sarama.RecordHeader{{Key: []byte("source"), Value: []byte("original")}}}
	if e := s.Schedule(ctx, name, "event-id", "dependency", m, false, false); e != nil {
		t.Fatal(e)
	}
	var id int64
	_ = p.QueryRow(ctx, "SELECT id FROM platform_failed_work WHERE consumer_name=$1", name).Scan(&id)
	t.Cleanup(func() {
		_, _ = p.Exec(ctx, "DELETE FROM platform_replay_audit WHERE failed_work_id=$1", id)
		_, _ = p.Exec(ctx, "DELETE FROM platform_failed_work WHERE id=$1", id)
	})
	for _, delay := range []time.Duration{1, 2, 4, 8, 16} {
		c.Advance(delay * time.Second)
		ran, e := s.RunOne(ctx, name, func(_ context.Context, f messaging.FailedWork) error {
			if string(f.Payload) != "unchanged" || f.Offset != 42 || !f.Timestamp.Equal(m.Timestamp) || len(f.Headers) != 1 {
				t.Fatal("lost original")
			}
			return errors.New("transient")
		}, nil)
		if e != nil || !ran {
			t.Fatal(ran, e)
		}
	}
	f, e := s.Inspect(ctx, id)
	if e != nil || f.Attempts != 5 || f.State != "QUARANTINED" {
		t.Fatal(f, e)
	}
	if s.Replay(ctx, id, auth.Principal{Role: "SHOPPER"}, "retry") == nil {
		t.Fatal("unauthorized replay")
	}
	operator := auth.Principal{Role: "OPERATOR", Subject: clock.UUID()}
	if e = s.Replay(ctx, id, operator, "dependency repaired"); e != nil {
		t.Fatal(e)
	}
	var audit int
	_ = p.QueryRow(ctx, "SELECT count(*) FROM platform_replay_audit WHERE failed_work_id=$1", id).Scan(&audit)
	if audit != 1 {
		t.Fatal("missing audit")
	}
	ran, e := s.RunOne(ctx, name, func(context.Context, messaging.FailedWork) error { return nil }, nil)
	if e != nil || !ran {
		t.Fatal(e)
	}
	m.Offset++
	if e = s.Schedule(ctx, name, "event2", "outage", m, false, true); e != nil {
		t.Fatal(e)
	}
	if ran, e = s.RunOne(ctx, name, func(context.Context, messaging.FailedWork) error { t.Fatal("outage should pause"); return nil }, nil); e != nil || ran {
		t.Fatal(ran, e)
	}
	if e = s.Resume(ctx, name); e != nil {
		t.Fatal(e)
	}
	if ran, e = s.RunOne(ctx, name, func(context.Context, messaging.FailedWork) error { return nil }, nil); e != nil || !ran {
		t.Fatal(ran, e)
	}
	_, _ = p.Exec(ctx, "DELETE FROM platform_failed_work WHERE consumer_name=$1 AND id<>$2", name, id)
}
