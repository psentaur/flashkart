package main

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"flashkart/internal/market"
	"github.com/twmb/franz-go/pkg/kmsg"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client, err := market.NewEventProducer(strings.Split(os.Getenv("KAFKA_BROKERS"), ","), "iam", os.Getenv("AWS_REGION"), "")
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
	req := kmsg.NewPtrCreateTopicsRequest()
	for _, name := range []string{"purchase-events", "analytics-results"} {
		req.Topics = append(req.Topics, kmsg.CreateTopicsRequestTopic{Topic: name, NumPartitions: 3, ReplicationFactor: -1})
	}
	result, err := req.RequestWith(ctx, client)
	if err != nil {
		log.Fatal(err)
	}
	if len(result.Topics) != len(req.Topics) {
		log.Fatal("incomplete Kafka create-topics response")
	}
	for _, topic := range result.Topics {
		if topic.ErrorCode != 0 && topic.ErrorCode != 36 { // Topic already exists.
			log.Fatalf("create %s: Kafka error %d (%v)", topic.Topic, topic.ErrorCode, topic.ErrorMessage)
		}
	}
}
