package market

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type Outcome struct {
	PurchaseWork
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	Payload   struct {
		State   string `json:"state"`
		Reason  string `json:"reason,omitempty"`
		OrderID string `json:"order_id,omitempty"`
	} `json:"payload"`
}

type AttemptView struct {
	ID      string `json:"attempt_id"`
	Sale    string `json:"sale_id"`
	State   string `json:"state"`
	Reason  string `json:"reason,omitempty"`
	OrderID string `json:"order_id,omitempty"`
}

type Notification struct {
	EventID string      `json:"event_id"`
	Attempt AttemptView `json:"attempt"`
}

type Notifications struct {
	client *dynamodb.Client
	table  *string
}

func NewNotifications(ctx context.Context, table, region, endpoint, credentials, ca string, local bool) (*Notifications, error) {
	if table == "" {
		return nil, errors.New("notification table is required")
	}
	client, err := newDynamoClient(ctx, region, endpoint, credentials, ca, local)
	if err != nil {
		return nil, err
	}
	return &Notifications{client, aws.String(table)}, nil
}

func newDynamoClient(ctx context.Context, region, endpoint, credentials, ca string, local bool) (*dynamodb.Client, error) {
	sdk, err := awsConfig(ctx, region, endpoint, credentials, local)
	if err != nil {
		return nil, err
	}
	if local {
		sdk.HTTPClient, err = localAWSHTTP(ca)
		if err != nil {
			return nil, err
		}
	}
	return dynamodb.NewFromConfig(sdk, func(o *dynamodb.Options) {
		if local {
			o.BaseEndpoint = aws.String(endpoint)
		}
	}), nil
}

func av(value string) *types.AttributeValueMemberS { return &types.AttributeValueMemberS{Value: value} }
func notificationKey(customer, id string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"customer_id": av(customer), "attempt_id": av(id)}
}

func (n *Notifications) Handle(ctx context.Context, body string) error {
	var event Outcome
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&event); err != nil || dec.Decode(new(any)) != io.EOF ||
		!event.PurchaseWork.valid() || event.EventType != "attempt.terminal" || event.EventID != "attempt.terminal:"+event.AttemptID {
		return errors.New("invalid terminal outcome")
	}
	switch event.Payload.State {
	case "REJECTED", "CONFIRMED", "CANCELLED":
	default:
		return errors.New("invalid terminal state")
	}
	data, err := json.Marshal(Notification{event.EventID, AttemptView{event.AttemptID, event.SaleID, event.Payload.State, event.Payload.Reason, event.Payload.OrderID}})
	if err != nil {
		return err
	}
	item := notificationKey(event.CustomerID, event.AttemptID)
	item["notification"] = av(string(data))
	_, err = n.client.PutItem(ctx, &dynamodb.PutItemInput{TableName: n.table, Item: item, ConditionExpression: aws.String("attribute_not_exists(attempt_id)")})
	var conflict *types.ConditionalCheckFailedException
	if errors.As(err, &conflict) {
		return nil
	}
	return err
}

func decodeNotification(item map[string]types.AttributeValue) (Notification, error) {
	var result Notification
	value, ok := item["notification"].(*types.AttributeValueMemberS)
	if !ok {
		return result, errors.New("missing notification data")
	}
	err := json.Unmarshal([]byte(value.Value), &result)
	return result, err
}

func (n *Notifications) Get(ctx context.Context, customer, id string) (*Notification, error) {
	out, err := n.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: n.table, Key: notificationKey(customer, id), ConsistentRead: aws.Bool(true)})
	if err != nil || len(out.Item) == 0 {
		return nil, err
	}
	result, err := decodeNotification(out.Item)
	return &result, err
}

func (n *Notifications) list(ctx context.Context, customer string) ([]Notification, error) {
	pages := dynamodb.NewQueryPaginator(n.client, &dynamodb.QueryInput{TableName: n.table, ConsistentRead: aws.Bool(true),
		KeyConditionExpression: aws.String("customer_id=:customer"), ExpressionAttributeValues: map[string]types.AttributeValue{":customer": av(customer)}})
	result := []Notification{}
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			notification, err := decodeNotification(item)
			if err != nil {
				return nil, err
			}
			result = append(result, notification)
		}
	}
	return result, nil
}

func (n *Notifications) Handler(token, customer string, fallback http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/notifications", func(w http.ResponseWriter, r *http.Request) {
		if !customerAuthorized(w, r, token) {
			return
		}
		values, err := n.list(r.Context(), customer)
		if err != nil {
			writeError(w, r, 503, "UNAVAILABLE", "notifications unavailable")
			return
		}
		send(w, 200, values)
	})
	return serveAPI(mux, fallback)
}

func (n *Notifications) Run(ctx context.Context, queue PurchaseQueue, report func()) {
	for ctx.Err() == nil {
		poll, cancel := context.WithTimeout(ctx, 15*time.Second)
		messages, err := queue.Receive(poll)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				report()
			}
			pause(ctx, time.Second)
			continue
		}
		for _, message := range messages {
			work, done := context.WithTimeout(ctx, 4*time.Second)
			if err = n.Handle(work, message.Body); err == nil {
				err = queue.Delete(work, message.Receipt)
			}
			done()
			if err != nil && ctx.Err() == nil {
				report()
			}
		}
	}
}

type BatchFailure struct {
	ID string `json:"itemIdentifier"`
}
type BatchResponse struct {
	Failures []BatchFailure `json:"batchItemFailures"`
}

func (n *Notifications) Batch(ctx context.Context, body []byte) (BatchResponse, error) {
	return handleBatch(ctx, body, n.Handle)
}

func handleBatch(ctx context.Context, body []byte, handle func(context.Context, string) error) (BatchResponse, error) {
	var event struct {
		Records []struct {
			ID   string `json:"messageId"`
			Body string `json:"body"`
		} `json:"Records"`
	}
	if err := json.Unmarshal(body, &event); err != nil || event.Records == nil {
		return BatchResponse{}, errors.New("invalid SQS invocation")
	}
	response := BatchResponse{Failures: []BatchFailure{}}
	for _, record := range event.Records {
		if record.ID == "" {
			return BatchResponse{}, errors.New("SQS record is missing its message ID")
		}
	}
	for _, record := range event.Records {
		if err := handle(ctx, record.Body); err != nil {
			response.Failures = append(response.Failures, BatchFailure{record.ID})
		}
	}
	return response, nil
}
