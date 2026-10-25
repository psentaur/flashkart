//go:build integration

package integration

import (
	"context"
	"errors"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/messaging"
	"github.com/IBM/sarama"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSaramaDurableAcknowledgementAndRedelivery(t *testing.T) {
	brokers := strings.Split(os.Getenv("KAFKA_BROKERS"), ",")
	if brokers[0] == "" {
		t.Fatal("KAFKA_BROKERS required")
	}
	cfg := messaging.Config("foundation-test")
	admin, e := sarama.NewClusterAdmin(brokers, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	topic := "foundation-" + clock.UUID()
	if e = admin.CreateTopic(topic, &sarama.TopicDetail{NumPartitions: 1, ReplicationFactor: 1}, false); e != nil {
		t.Fatal(e)
	}
	defer admin.DeleteTopic(topic)
	producer, e := messaging.Producer(brokers, "foundation-test")
	if e != nil {
		t.Fatal(e)
	}
	defer producer.Close()
	_, offset, e := producer.SendMessage(&sarama.ProducerMessage{Topic: topic, Key: sarama.StringEncoder("stable"), Value: sarama.StringEncoder("durable-event")})
	if e != nil {
		t.Fatal(e)
	}
	groupID := "foundation-" + clock.UUID()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	group, e := messaging.Group(brokers, groupID)
	if e != nil {
		t.Fatal(e)
	}
	sentinel := errors.New("crash before commit")
	if e = messaging.Consume(ctx, group, []string{topic}, func(context.Context, *sarama.ConsumerMessage) error { return sentinel }); !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
	_ = group.Close()
	group, e = messaging.Group(brokers, groupID)
	if e != nil {
		t.Fatal(e)
	}
	defer group.Close()
	done := make(chan error, 1)
	second, cancelSecond := context.WithCancel(ctx)
	defer cancelSecond()
	handled := make(chan struct{}, 1)
	go func() {
		done <- messaging.Consume(second, group, []string{topic}, func(_ context.Context, m *sarama.ConsumerMessage) error {
			if string(m.Value) != "durable-event" {
				return errors.New("payload changed")
			}
			handled <- struct{}{}
			return nil
		})
	}()
	select {
	case <-handled:
	case <-ctx.Done():
		t.Fatal("uncommitted event was lost")
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		response, e := admin.ListConsumerGroupOffsets(groupID, map[string][]int32{topic: {0}})
		if e == nil {
			block := response.GetBlock(topic, 0)
			if block != nil && block.Offset == offset+1 {
				break
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("durable work offset not committed")
		case <-ticker.C:
		}
	}
	cancelSecond()
	<-done
}
