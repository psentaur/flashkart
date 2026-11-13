package market

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Images struct {
	bucket string
	signer *s3.PresignClient
}

func NewImages(ctx context.Context, bucket, region, endpoint, credentialsFile string, local bool) (*Images, error) {
	if bucket == "" {
		return nil, nil
	}
	if region == "" {
		return nil, errors.New("product images require an AWS region")
	}
	var sdk aws.Config
	if local {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" ||
			!net.ParseIP(u.Hostname()).IsLoopback() || credentialsFile == "" {
			return nil, errors.New("local product images require a loopback HTTPS S3 endpoint and emulator credentials file")
		}
		profile, err := config.LoadSharedConfigProfile(ctx, "default", func(o *config.LoadSharedConfigOptions) {
			o.ConfigFiles = []string{}
			o.CredentialsFiles = []string{credentialsFile}
		})
		if err != nil || !profile.Credentials.HasKeys() {
			return nil, errors.New("could not read dedicated emulator credentials")
		}
		sdk = aws.Config{Region: region, Credentials: credentials.NewStaticCredentialsProvider(
			profile.Credentials.AccessKeyID, profile.Credentials.SecretAccessKey, profile.Credentials.SessionToken)}
	} else {
		var err error
		sdk, err = config.LoadDefaultConfig(ctx, config.WithRegion(region))
		if err != nil {
			return nil, errors.New("could not load AWS image configuration")
		}
	}
	client := s3.NewFromConfig(sdk, func(o *s3.Options) {
		if local {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		}
	})
	return &Images{bucket, s3.NewPresignClient(client)}, nil
}

func (i *Images) Validate(value string) error {
	if value == "" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.Host == "" {
		return errors.New("invalid product image URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "s3" && i != nil && u.Host == i.bucket && strings.TrimPrefix(u.Path, "/") != "" &&
		u.RawQuery == "" && u.Fragment == "" {
		return nil
	}
	return errors.New("image must use HTTPS or the configured S3 bucket")
}

func (i *Images) Resolve(ctx context.Context, value string) (string, error) {
	if err := i.Validate(value); err != nil {
		return "", err
	}
	if value == "" {
		return value, nil
	}
	u, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	if u.Scheme != "s3" {
		return value, nil
	}
	request, err := i.signer.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(i.bucket), Key: aws.String(strings.TrimPrefix(u.Path, "/")),
	}, func(o *s3.PresignOptions) { o.Expires = 5 * time.Minute })
	if err != nil {
		return "", err
	}
	return request.URL, nil
}
