package market

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

type PurchaseMessage struct {
	Body, Receipt string
}

type PurchaseQueue interface {
	Send(context.Context, string) error
	Receive(context.Context) ([]PurchaseMessage, error)
	Delete(context.Context, string) error
}

type sqsQueue struct {
	client *sqs.Client
	url    *string
}

func NewPurchaseQueue(ctx context.Context, queueURL, region, endpoint, credentialsFile, caFile string, local bool) (PurchaseQueue, error) {
	u, err := url.Parse(queueURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path == "" || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(u.Path, ".fifo") {
		return nil, errors.New("purchase queue requires a standard HTTPS SQS queue URL")
	}
	if local {
		name := strings.TrimPrefix(u.Path, "/000000000000/")
		if name == u.Path || name == "" || strings.Contains(name, "/") {
			return nil, errors.New("local queue must belong to emulator account 000000000000")
		}
	}
	sdk, err := awsConfig(ctx, region, endpoint, credentialsFile, local)
	if err != nil {
		return nil, err
	}
	if local {
		sdk.HTTPClient, err = localAWSHTTP(caFile)
		if err != nil {
			return nil, err
		}
	}
	return sqsQueue{sqs.NewFromConfig(sdk, func(o *sqs.Options) {
		if local {
			o.BaseEndpoint = aws.String(endpoint)
		}
	}), aws.String(queueURL)}, nil
}

func (q sqsQueue) Send(ctx context.Context, body string) error {
	_, err := q.client.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: q.url, MessageBody: aws.String(body)})
	return err
}

func (q sqsQueue) Receive(ctx context.Context) ([]PurchaseMessage, error) {
	response, err := q.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{QueueUrl: q.url, WaitTimeSeconds: 10, MaxNumberOfMessages: 1, VisibilityTimeout: 30})
	if err != nil {
		return nil, err
	}
	messages := make([]PurchaseMessage, 0, len(response.Messages))
	for _, m := range response.Messages {
		messages = append(messages, PurchaseMessage{aws.ToString(m.Body), aws.ToString(m.ReceiptHandle)})
	}
	return messages, nil
}

func (q sqsQueue) Delete(ctx context.Context, receipt string) error {
	_, err := q.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{QueueUrl: q.url, ReceiptHandle: aws.String(receipt)})
	return err
}

func localAWSHTTP(caFile string) (*http.Client, error) {
	pem, err := os.ReadFile(caFile)
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("local AWS service requires the emulator TLS CA")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	return &http.Client{Transport: transport}, nil
}
