package market

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestImageAccess(t *testing.T) {
	bucket := os.Getenv("FLASHKART_TEST_IMAGE_BUCKET")
	if bucket == "" {
		t.Skip("set the local emulator image fixture to verify signed downloads")
	}
	images, err := NewImages(t.Context(), bucket, "us-east-1", os.Getenv("FLASHKART_TEST_IMAGE_ENDPOINT"),
		os.Getenv("FLASHKART_TEST_IMAGE_CREDENTIALS"), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := images.Resolve(t.Context(), "s3://other-bucket/probe"); err == nil {
		t.Fatal("signed a request for another bucket")
	}
	imageURL, err := images.Resolve(t.Context(), "s3://"+bucket+"/probe")
	if err != nil {
		t.Fatal(err)
	}
	pem, err := os.ReadFile(os.Getenv("FLASHKART_TEST_IMAGE_CA"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatal("invalid local emulator CA")
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, imageURL, nil)
	if err != nil {
		t.Fatal("could not build signed GET request")
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal("could not fetch the signed image")
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || string(content) != "phase5-image-probe" {
		t.Fatalf("unexpected image response: HTTP %d, %v", response.StatusCode, err)
	}
}
