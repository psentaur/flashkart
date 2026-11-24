package market

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/http"
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
	client *s3.Client
	signer *s3.PresignClient
}

// MaxImageBytes caps an uploaded product image.
const MaxImageBytes = 5 << 20

var imageExtensions = map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp", "image/gif": "gif"}

// Upload stores an image in the configured bucket and returns its s3:// reference.
// The content type is detected from the bytes, so a mislabeled upload is rejected.
func (i *Images) Upload(ctx context.Context, productID string, data []byte) (string, error) {
	if i == nil {
		return "", errImagesDisabled
	}
	kind := http.DetectContentType(data)
	ext, ok := imageExtensions[kind]
	if !ok || len(data) == 0 || len(data) > MaxImageBytes {
		return "", errInvalidImage
	}
	key := "products/" + keySegment(productID) + "/" + strings.ToLower(rand.Text()) + "." + ext
	_, err := i.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(i.bucket), Key: aws.String(key),
		Body: bytes.NewReader(data), ContentType: aws.String(kind), CacheControl: aws.String("max-age=31536000, immutable")})
	if err != nil {
		return "", err
	}
	return "s3://" + i.bucket + "/" + key, nil
}

// Open returns the stored object for an s3:// reference in the configured bucket.
func (i *Images) Open(ctx context.Context, ref string) (io.ReadCloser, string, error) {
	if i == nil {
		return nil, "", errImagesDisabled
	}
	u, err := url.Parse(ref)
	if err != nil || u.Scheme != "s3" || u.Host != i.bucket || strings.TrimPrefix(u.Path, "/") == "" {
		return nil, "", errInvalidImage
	}
	out, err := i.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(i.bucket), Key: aws.String(strings.TrimPrefix(u.Path, "/"))})
	if err != nil {
		return nil, "", err
	}
	return out.Body, aws.ToString(out.ContentType), nil
}

// keySegment maps a product ID onto characters that survive the s3:// reference unescaped,
// so the stored key and the parsed reference always match.
func keySegment(id string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, id)
}

var (
	errImagesDisabled = errors.New("product image storage is not configured")
	errInvalidImage   = errors.New("upload a JPEG, PNG, WebP or GIF image of at most 5 MB")
)

func NewImages(ctx context.Context, bucket, region, endpoint, credentialsFile string, local bool) (*Images, error) {
	if bucket == "" {
		return nil, nil
	}
	sdk, err := awsConfig(ctx, region, endpoint, credentialsFile, local)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(sdk, func(o *s3.Options) {
		if local {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		}
	})
	return &Images{bucket, client, s3.NewPresignClient(client)}, nil
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

func awsConfig(ctx context.Context, region, endpoint, credentialsFile string, local bool) (aws.Config, error) {
	if region == "" {
		return aws.Config{}, errors.New("AWS services require a region")
	}
	var sdk aws.Config
	if local {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" ||
			!net.ParseIP(u.Hostname()).IsLoopback() || credentialsFile == "" {
			return aws.Config{}, errors.New("local AWS services require a loopback HTTPS endpoint and emulator credentials file")
		}
		profile, err := config.LoadSharedConfigProfile(ctx, "default", func(o *config.LoadSharedConfigOptions) {
			o.ConfigFiles = []string{}
			o.CredentialsFiles = []string{credentialsFile}
		})
		if err != nil || !profile.Credentials.HasKeys() {
			return aws.Config{}, errors.New("could not read dedicated emulator credentials")
		}
		sdk = aws.Config{Region: region, Credentials: credentials.NewStaticCredentialsProvider(
			profile.Credentials.AccessKeyID, profile.Credentials.SecretAccessKey, profile.Credentials.SessionToken)}
	} else {
		var err error
		sdk, err = config.LoadDefaultConfig(ctx, config.WithRegion(region))
		if err != nil {
			return aws.Config{}, errors.New("could not load AWS configuration")
		}
	}
	return sdk, nil
}
