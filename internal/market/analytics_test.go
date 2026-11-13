package market

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

func analyticsFixture(sale string) []byte {
	return []byte(fmt.Sprintf(`{
		"schema_version":1,"event_id":"analytics.window_finalized:%s:1800000000000",
		"event_type":"analytics.window_finalized","occurred_at":"2027-01-15T08:01:30Z",
		"trace_id":"window","sale_id":"%s","payload":{
			"starts_at":"2027-01-15T08:00:00Z","ends_at":"2027-01-15T08:01:00Z",
			"demand":3,"admission_rejections":1,"stock_rejections":1,"reservations":1,
			"confirmed_purchases":1,"average_latency_ms":20
		}
	}`, sale, sale))
}

func TestAnalyticsResult(t *testing.T) {
	fixture := analyticsFixture("sale")
	result, err := parseAnalyticsResult(fixture)
	if err != nil || result.Payload.Demand != 3 {
		t.Fatalf("invalid fixture: %+v %v", result, err)
	}
	if _, err := parseAnalyticsResult(append(fixture, []byte(" true")...)); err == nil {
		t.Fatal("accepted trailing data")
	}
}

func TestAnalyticsProjection(t *testing.T) {
	table := os.Getenv("FLASHKART_TEST_ANALYTICS_TABLE")
	if table == "" {
		t.Skip("requires a dedicated local DynamoDB analytics table")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	newProjection := func() *Analytics {
		t.Helper()
		projection, err := NewAnalytics(ctx, table, "us-east-1", os.Getenv("FLASHKART_TEST_DYNAMODB_ENDPOINT"),
			os.Getenv("FLASHKART_TEST_DYNAMODB_CREDENTIALS"), os.Getenv("FLASHKART_TEST_DYNAMODB_CA"), true)
		if err != nil {
			t.Fatal(err)
		}
		return projection
	}
	sale := rand.Text()
	body := analyticsFixture(sale)
	if err := newProjection().project(ctx, []byte(sale), body); err != nil {
		t.Fatal(err)
	} // Recreate the projector after its write and replay the uncommitted record.
	restarted := newProjection()
	if broker := os.Getenv("FLASHKART_TEST_KAFKA"); broker != "" {
		testAnalyticsOffsetRecovery(ctx, t, broker, sale, body, restarted)
	} else if err := restarted.project(ctx, []byte(sale), body); err != nil {
		t.Fatal(err)
	}
	changed, err := parseAnalyticsResult(body)
	if err != nil {
		t.Fatal(err)
	}
	changed.Payload.Demand++
	conflict, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	var conditional *types.ConditionalCheckFailedException
	if err := restarted.project(ctx, []byte(sale), conflict); !errors.As(err, &conditional) {
		t.Fatalf("conflicting final snapshot was not rejected: %v", err)
	}
	handler := restarted.Handler(http.NotFoundHandler())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/analytics/sales/"+sale, nil).WithContext(ctx))
	var windows []SaleWindow
	if err := json.Unmarshal(response.Body.Bytes(), &windows); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(windows) != 1 || windows[0].Demand != 3 || windows[0].SaleID != sale {
		t.Fatalf("unexpected projection: %d %+v", response.Code, windows)
	}
}

func testAnalyticsOffsetRecovery(ctx context.Context, t *testing.T, broker, sale string, body []byte, restarted *Analytics) {
	t.Helper()
	brokers := []string{broker}
	topic := "analytics-projection-test-" + rand.Text()
	admin, err := newKafkaClient(brokers, "plaintext", "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	create := kmsg.NewPtrCreateTopicsRequest()
	create.Topics = []kmsg.CreateTopicsRequestTopic{{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}}
	created, err := create.RequestWith(ctx, admin)
	if err != nil || len(created.Topics) != 1 || created.Topics[0].ErrorCode != 0 {
		t.Fatalf("create isolated projection topic: %v %v", created, err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		request := kmsg.NewPtrDeleteTopicsRequest()
		request.TopicNames = []string{topic}
		if _, err := request.RequestWith(cleanup, admin); err != nil {
			t.Errorf("delete projection test topic: %v", err)
		}
	}()
	if err := admin.ProduceSync(ctx, &kgo.Record{Topic: topic, Key: []byte(sale), Value: body}).FirstErr(); err != nil {
		t.Fatal(err)
	}
	newConsumer := func() *kgo.Client {
		client, err := newKafkaClient(brokers, "plaintext", "", kgo.ConsumerGroup(topic),
			kgo.ConsumeTopics(topic), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
			kgo.DisableAutoCommit(), kgo.BlockRebalanceOnPoll())
		if err != nil {
			t.Fatal(err)
		}
		return client
	}
	// Consume the record without committing to model the crash after PutItem.
	before := newConsumer()
	fetches := before.PollRecords(ctx, 1)
	before.CloseAllowingRebalance()
	if fetches.Err() != nil || len(fetches.Records()) != 1 {
		t.Fatalf("initial consumption failed: %v", fetches.Err())
	}
	after := newConsumer()
	defer after.CloseAllowingRebalance()
	if err := restarted.consume(ctx, after); err != nil {
		t.Fatal(err)
	}
	request := kmsg.NewPtrOffsetFetchRequest()
	request.Group = topic
	request.Topics = []kmsg.OffsetFetchRequestTopic{{Topic: topic, Partitions: []int32{0}}}
	response, err := request.RequestWith(ctx, admin)
	if err != nil || len(response.Topics) != 1 || len(response.Topics[0].Partitions) != 1 ||
		response.Topics[0].Partitions[0].Offset != fetches.Records()[0].Offset+1 {
		t.Fatalf("projection offset was not committed: %v %v", response, err)
	}
}
