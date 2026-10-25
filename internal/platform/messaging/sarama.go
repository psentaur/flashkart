// Package messaging provides checked Kafka delivery and durable acknowledgement seams.
package messaging

import (
	"context"
	"fmt"
	"github.com/IBM/sarama"
)

func Config(clientID string) *sarama.Config {
	c := sarama.NewConfig()
	c.Version = sarama.V3_6_0_0
	c.ClientID = clientID
	c.Producer.RequiredAcks = sarama.WaitForAll
	c.Producer.Idempotent = true
	c.Net.MaxOpenRequests = 1
	c.Producer.Return.Successes = true
	c.Producer.Return.Errors = true
	c.Consumer.Return.Errors = true
	c.Consumer.Offsets.AutoCommit.Enable = false
	c.Consumer.Offsets.Initial = sarama.OffsetOldest
	return c
}
func Check(c *sarama.Config) error {
	if c == nil || c.Producer.RequiredAcks != sarama.WaitForAll || !c.Producer.Idempotent || c.Net.MaxOpenRequests != 1 || !c.Producer.Return.Successes || !c.Producer.Return.Errors || c.Consumer.Offsets.AutoCommit.Enable {
		return fmt.Errorf("unsafe delivery configuration")
	}
	return c.Validate()
}
func Producer(brokers []string, id string) (sarama.SyncProducer, error) {
	c := Config(id)
	if e := Check(c); e != nil {
		return nil, e
	}
	return sarama.NewSyncProducer(brokers, c)
}
func Group(brokers []string, id string) (sarama.ConsumerGroup, error) {
	c := Config(id)
	if e := Check(c); e != nil {
		return nil, e
	}
	return sarama.NewConsumerGroup(brokers, id, c)
}

type Handler func(context.Context, *sarama.ConsumerMessage) error

// Complete makes the durability boundary explicit and can be checked without a broker.
func Complete(ctx context.Context, m *sarama.ConsumerMessage, handle Handler, ack func()) error {
	if e := handle(ctx, m); e != nil {
		return e
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	ack()
	return nil
}

type groupHandler struct {
	handle Handler
	fail   func(error)
}

func (groupHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (groupHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }
func (h groupHandler) ConsumeClaim(s sarama.ConsumerGroupSession, c sarama.ConsumerGroupClaim) error {
	for {
		select {
		case <-s.Context().Done():
			return nil
		case m, ok := <-c.Messages():
			if !ok {
				return nil
			}
			if e := Complete(s.Context(), m, h.handle, func() { s.MarkMessage(m, ""); s.Commit() }); e != nil {
				h.fail(e)
				return e
			}
		}
	}
}
func Consume(ctx context.Context, g sarama.ConsumerGroup, topics []string, handler Handler) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	failed := make(chan error, 1)
	h := groupHandler{handler, func(e error) {
		select {
		case failed <- e:
		default:
		}
		cancel()
	}}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-g.Errors():
				if !ok {
					return
				}
				h.fail(e)
			}
		}
	}()
	for ctx.Err() == nil {
		if e := g.Consume(ctx, topics, h); e != nil {
			return e
		}
	}
	select {
	case e := <-failed:
		return e
	default:
		return ctx.Err()
	}
}
