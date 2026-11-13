package market

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

type Window struct {
	StartsAt          time.Time `json:"starts_at"`
	EndsAt            time.Time `json:"ends_at"`
	Demand            int64     `json:"demand"`
	AdmissionRejected int64     `json:"admission_rejections"`
	StockRejected     int64     `json:"stock_rejections"`
	Reservations      int64     `json:"reservations"`
	Confirmed         int64     `json:"confirmed_purchases"`
	AverageLatency    *float64  `json:"average_latency_ms"`
}

type SaleWindow struct {
	SaleID string `json:"sale_id"`
	Window
}

type analyticsResult struct {
	Version    int       `json:"schema_version"`
	ID         string    `json:"event_id"`
	Type       string    `json:"event_type"`
	OccurredAt time.Time `json:"occurred_at"`
	TraceID    string    `json:"trace_id"`
	SaleID     string    `json:"sale_id"`
	Payload    Window    `json:"payload"`
}

type Analytics struct {
	client *dynamodb.Client
	table  *string
}

func NewAnalytics(ctx context.Context, table, region, endpoint, credentials, ca string, local bool) (*Analytics, error) {
	if table == "" {
		return nil, errors.New("analytics table is required")
	}
	client, err := newDynamoClient(ctx, region, endpoint, credentials, ca, local)
	if err != nil {
		return nil, err
	}
	return &Analytics{client, aws.String(table)}, nil
}

func parseAnalyticsResult(body []byte) (analyticsResult, error) {
	var result analyticsResult
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil || dec.Decode(new(any)) != io.EOF {
		return result, errors.New("invalid analytics result JSON")
	}
	w := &result.Payload
	w.StartsAt, w.EndsAt = w.StartsAt.UTC(), w.EndsAt.UTC()
	if result.Version != 1 || result.Type != "analytics.window_finalized" || !validID(result.SaleID) ||
		result.OccurredAt.IsZero() || result.TraceID == "" ||
		result.ID != fmt.Sprintf("analytics.window_finalized:%s:%d", result.SaleID, w.StartsAt.UnixMilli()) ||
		w.StartsAt.IsZero() || !w.StartsAt.Truncate(time.Minute).Equal(w.StartsAt) || w.EndsAt.Sub(w.StartsAt) != time.Minute ||
		w.Demand < 0 || w.AdmissionRejected < 0 || w.StockRejected < 0 || w.Reservations < 0 || w.Confirmed < 0 ||
		(w.Confirmed == 0) != (w.AverageLatency == nil) || w.AverageLatency != nil && *w.AverageLatency < 0 {
		return result, errors.New("invalid finalized analytics window")
	}
	return result, nil
}

func (a *Analytics) project(ctx context.Context, key, body []byte) error {
	result, err := parseAnalyticsResult(body)
	if err != nil {
		return err
	}
	if string(key) != result.SaleID {
		return errors.New("analytics Kafka key does not match sale ID")
	}
	snapshot, err := json.Marshal(result.Payload)
	if err != nil {
		return err
	}
	// Complete finalized snapshots are immutable. An identical retry establishes
	// the same projection, while a conflicting result must not overwrite it.
	_, err = a.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: a.table,
		Item: map[string]types.AttributeValue{
			"sale_id": av(result.SaleID), "starts_at": av(result.Payload.StartsAt.Format(time.RFC3339)),
			"event_id": av(result.ID), "snapshot": av(string(snapshot)),
		},
		ConditionExpression:      aws.String("attribute_not_exists(starts_at) OR (event_id = :id AND #snapshot = :snapshot)"),
		ExpressionAttributeNames: map[string]string{"#snapshot": "snapshot"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":id": av(result.ID), ":snapshot": av(string(snapshot)),
		},
	})
	return err
}

func (a *Analytics) list(ctx context.Context, sale string) ([]SaleWindow, error) {
	pages := dynamodb.NewQueryPaginator(a.client, &dynamodb.QueryInput{
		TableName: a.table, ConsistentRead: aws.Bool(true), KeyConditionExpression: aws.String("sale_id = :sale"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":sale": av(sale)},
	})
	windows := []SaleWindow{}
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			value, ok := item["snapshot"].(*types.AttributeValueMemberS)
			if !ok {
				return nil, errors.New("analytics snapshot missing")
			}
			window := SaleWindow{SaleID: sale}
			if err := json.Unmarshal([]byte(value.Value), &window.Window); err != nil {
				return nil, err
			}
			windows = append(windows, window)
		}
	}
	return windows, nil
}

func (a *Analytics) Handler(fallback http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/analytics/sales/{sale_id}", func(w http.ResponseWriter, r *http.Request) {
		sale := r.PathValue("sale_id")
		if !validID(sale) {
			writeError(w, r, 400, "INVALID_ARGUMENT", "invalid sale ID")
			return
		}
		windows, err := a.list(r.Context(), sale)
		if err != nil {
			writeError(w, r, 503, "UNAVAILABLE", "analytics unavailable")
			return
		}
		send(w, 200, windows)
	})
	return serveAPI(mux, fallback)
}

func (a *Analytics) Run(ctx context.Context, brokers []string, auth, region, group string, log *slog.Logger) error {
	producer, err := newKafkaClient(brokers, auth, region, kgo.RecordPartitioner(kgo.ManualPartitioner()))
	if err != nil {
		return err
	}
	defer producer.Close()
	done := make(chan struct{})
	heartbeatCtx, stop := context.WithCancel(ctx)
	go func() {
		defer close(done)
		for heartbeatCtx.Err() == nil {
			work, cancel := context.WithTimeout(heartbeatCtx, 4*time.Second)
			err := publishHeartbeats(work, producer)
			cancel()
			if err != nil && heartbeatCtx.Err() == nil {
				log.Warn("analytics heartbeat failed", "error", err)
			}
			pause(heartbeatCtx, 5*time.Second)
		}
	}()
	defer func() { stop(); <-done }()

	for ctx.Err() == nil {
		consumer, err := newKafkaClient(brokers, auth, region, kgo.ConsumerGroup(group),
			kgo.ConsumeTopics("analytics-results"), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
			kgo.FetchIsolationLevel(kgo.ReadCommitted()),
			kgo.DisableAutoCommit(), kgo.BlockRebalanceOnPoll())
		if err != nil {
			return err
		}
		for ctx.Err() == nil {
			err = a.consume(ctx, consumer)
			if err != nil {
				break
			}
		}
		consumer.CloseAllowingRebalance()
		if err != nil && ctx.Err() == nil {
			log.Warn("analytics projection will retry from committed offsets", "error", err)
			pause(ctx, time.Second)
		}
	}
	return nil
}

func (a *Analytics) consume(ctx context.Context, consumer *kgo.Client) error {
	fetches := consumer.PollRecords(ctx, 1)
	defer consumer.AllowRebalance()
	if err := fetches.Err(); err != nil {
		return err
	}
	for _, record := range fetches.Records() {
		work, cancel := context.WithTimeout(ctx, 4*time.Second)
		err := a.project(work, record.Key, record.Value)
		cancel()
		if err == nil {
			work, cancel = context.WithTimeout(ctx, 4*time.Second)
			err = consumer.CommitRecords(work, record)
			cancel()
		}
		if err != nil {
			return fmt.Errorf("partition %d offset %d: %w", record.Partition, record.Offset, err)
		}
	}
	return nil
}

func publishHeartbeats(ctx context.Context, producer *kgo.Client) error {
	const topic = "purchase-events"
	request := kmsg.NewPtrMetadataRequest()
	request.AllowAutoTopicCreation = false
	request.Topics = []kmsg.MetadataRequestTopic{{Topic: aws.String(topic)}}
	response, err := request.RequestWith(ctx, producer)
	if err != nil {
		return err
	}
	if len(response.Topics) != 1 || response.Topics[0].ErrorCode != 0 || len(response.Topics[0].Partitions) == 0 {
		return errors.New("purchase-events partitions unavailable")
	}
	id := "analytics.heartbeat:" + rand.Text()
	body, err := json.Marshal(map[string]any{
		"schema_version": 1, "event_id": id, "event_type": "analytics.heartbeat",
		"occurred_at": time.Now().UTC(), "trace_id": id, "payload": map[string]any{},
	})
	if err != nil {
		return err
	}
	records := make([]*kgo.Record, 0, len(response.Topics[0].Partitions))
	for _, partition := range response.Topics[0].Partitions {
		records = append(records, &kgo.Record{Topic: topic, Partition: partition.Partition, Value: body})
	}
	return producer.ProduceSync(ctx, records...).FirstErr()
}
