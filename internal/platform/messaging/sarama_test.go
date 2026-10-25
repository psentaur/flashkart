package messaging

import (
	"context"
	"errors"
	"github.com/IBM/sarama"
	"testing"
)

func TestCheckedAcknowledgement(t *testing.T) {
	c := Config("test")
	if e := Check(c); e != nil {
		t.Fatal(e)
	}
	c.Net.MaxOpenRequests = 2
	if Check(c) == nil {
		t.Fatal("unsafe config")
	}
	acked := false
	err := Complete(context.Background(), &sarama.ConsumerMessage{}, func(context.Context, *sarama.ConsumerMessage) error { return errors.New("rollback") }, func() { acked = true })
	if err == nil || acked {
		t.Fatal("acked before durability")
	}
	err = Complete(context.Background(), &sarama.ConsumerMessage{}, func(context.Context, *sarama.ConsumerMessage) error { return nil }, func() { acked = true })
	if err != nil || !acked {
		t.Fatal("missing ack")
	}
}
