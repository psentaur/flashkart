package market

import (
	"context"
	"crypto/tls"
	"errors"

	"github.com/aws/aws-msk-iam-sasl-signer-go/signer"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/oauth"
)

func NewEventProducer(brokers []string, auth, region string) (*kgo.Client, error) {
	return newKafkaClient(brokers, auth, region, kgo.DefaultProduceTopic("purchase-events"))
}

func newKafkaClient(brokers []string, auth, region string, extra ...kgo.Opt) (*kgo.Client, error) {
	if len(brokers) == 0 {
		return nil, errors.New("Kafka brokers are required")
	}
	options := []kgo.Opt{kgo.SeedBrokers(brokers...), kgo.RequiredAcks(kgo.AllISRAcks())}
	switch auth {
	case "", "plaintext":
	case "iam":
		if region == "" {
			return nil, errors.New("MSK IAM requires a region")
		}
		options = append(options, kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}), kgo.SASL(oauth.Oauth(func(ctx context.Context) (oauth.Auth, error) {
			token, _, err := signer.GenerateAuthToken(ctx, region)
			return oauth.Auth{Token: token}, err
		})))
	default:
		return nil, errors.New("unknown Kafka authentication")
	}
	return kgo.NewClient(append(options, extra...)...)
}
