package doubles

import (
	"context"
	"encoding/json"
	"flash-sale/contracts/gen/inventory/v1"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/messaging"
	"fmt"
	"github.com/IBM/sarama"
)

type Fixtures struct {
	Reserves []struct {
		Request *inventoryv1.ReserveRequest  `json:"request"`
		Result  *inventoryv1.OperationResult `json:"result"`
	} `json:"reserves"`
	Catalog []struct {
		Path   string          `json:"path"`
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
	} `json:"catalog"`
	Notices []struct {
		Shopper string          `json:"shopper"`
		Body    json.RawMessage `json:"body"`
	} `json:"notices"`
	Payments []struct {
		Command events.Envelope `json:"command"`
		Result  events.Envelope `json:"result"`
	} `json:"payments"`
}

func (f Fixtures) Apply(i *Inventory, r *REST, p *Payment) error {
	for _, v := range f.Reserves {
		if v.Request == nil || v.Result == nil {
			return fmt.Errorf("missing Reserve script")
		}
		i.Script(v.Request, v.Result)
	}
	for _, v := range f.Catalog {
		if e := r.Script(v.Path, v.Status, v.Body); e != nil {
			return e
		}
	}
	for _, v := range f.Notices {
		if e := r.Notice(v.Shopper, v.Body); e != nil {
			return e
		}
	}
	for _, v := range f.Payments {
		if e := p.Script(v.Command, v.Result); e != nil {
			return e
		}
	}
	return nil
}
func (p *Payment) Run(ctx context.Context, brokers []string, groupID string) error {
	group, e := messaging.Group(brokers, groupID)
	if e != nil {
		return e
	}
	defer group.Close()
	producer, e := messaging.Producer(brokers, groupID)
	if e != nil {
		return e
	}
	defer producer.Close()
	return messaging.Consume(ctx, group, []string{"payment.requests.v1"}, func(ctx context.Context, m *sarama.ConsumerMessage) error {
		command, e := events.Decode(m.Topic, string(m.Key), m.Value)
		if e != nil {
			return e
		}
		result, e := p.Deliver(ctx, command)
		if e != nil {
			return e
		}
		raw, e := events.Encode(result)
		if e != nil {
			return e
		}
		topic, key := events.Destination(result)
		if _, _, e = producer.SendMessage(&sarama.ProducerMessage{Topic: topic, Key: sarama.StringEncoder(key), Value: sarama.ByteEncoder(raw)}); e != nil {
			return e
		}
		_, key = events.DestinationFor(result, "analytics.activity.v1")
		_, _, e = producer.SendMessage(&sarama.ProducerMessage{Topic: "analytics.activity.v1", Key: sarama.StringEncoder(key), Value: sarama.ByteEncoder(raw)})
		return e
	})
}
