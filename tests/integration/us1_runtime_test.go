//go:build runtime

package integration

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flash-sale/internal/platform/clock"
	"flash-sale/internal/platform/events"
	"flash-sale/internal/platform/messaging"
	"flash-sale/internal/platform/value"
	"github.com/IBM/sarama"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUS1LiveRuntimeReadinessAndPrivateOffer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw, e := os.ReadFile(os.Getenv("SEEDED_TOKENS_FILE"))
	if e != nil {
		t.Fatal(e)
	}
	var manifest struct {
		Identities []struct{ Role, Token string }
	}
	if e = json.Unmarshal(raw, &manifest); e != nil {
		t.Fatal(e)
	}
	token := ""
	for _, p := range manifest.Identities {
		if p.Role == "OPERATOR" {
			token = p.Token
		}
	}
	base := os.Getenv("GATEWAY_URL")
	client := &http.Client{Timeout: 3 * time.Second}
	call := func(method, path string, body any) map[string]any {
		t.Helper()
		var input []byte
		if body != nil {
			input, e = json.Marshal(body)
			if e != nil {
				t.Fatal(e)
			}
		}
		r, e := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(input))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		response, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		var doc map[string]any
		if e = json.NewDecoder(response.Body).Decode(&doc); e != nil {
			t.Fatal(e)
		}
		if response.StatusCode >= 400 {
			t.Fatal(response.StatusCode, doc)
		}
		return doc
	}
	now := time.Now().UTC().Truncate(time.Second)
	saleID, productID := clock.UUID(), clock.UUID()
	config := map[string]any{"sale_id": saleID, "product_id": productID, "expected_version": 0, "unit_price_minor": 2500, "currency": "INR", "allocated_units": 100, "starts_at": now.Add(6 * time.Second).Format(time.RFC3339), "ends_at": now.Add(12 * time.Second).Format(time.RFC3339), "admission_limits": map[string]int{"shopper_per_second": 5, "sale_per_second": 100, "max_in_flight": 50}}
	s := call("POST", "/v1/admin/sales", config)
	b, _ := json.Marshal(s)
	d, e := value.ConfigurationDigest(b)
	if e != nil {
		t.Fatal(e)
	}
	producer, e := messaging.Producer(strings.Split(os.Getenv("KAFKA_BROKERS"), ","), "us1-runtime-fixture")
	if e != nil {
		t.Fatal(e)
	}
	defer producer.Close()
	for _, participant := range []string{"catalog", "inventory"} {
		data, _ := json.Marshal(events.SaleReady{SaleID: saleID, ProductID: productID, Version: 1, Participant: participant, AllocationDigest: d, Status: "READY"})
		en := events.Envelope{EventID: clock.UUID(), SchemaVersion: 1, Type: "SaleReady", OccurredAt: time.Now().UTC().Format(time.RFC3339Nano), Producer: participant, AggregateID: saleID, AggregateVersion: 1, CorrelationID: clock.UUID(), Data: data}
		raw, e := events.Encode(en)
		if e != nil {
			t.Fatal(e)
		}
		if _, _, e = producer.SendMessage(&sarama.ProducerMessage{Topic: "sale.ready.v1", Key: sarama.StringEncoder(saleID), Value: sarama.ByteEncoder(raw)}); e != nil {
			t.Fatal(e)
		}
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	active := false
	for {
		s = call("GET", "/v1/admin/sales/"+saleID, nil)
		if s["status"] == "ACTIVE" {
			active = true
		}
		if s["status"] == "ENDED" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("runtime sale did not progress", s)
		case <-ticker.C:
		}
	}
	if !active {
		t.Fatal("runtime never activated")
	}
	for _, path := range []string{"/", "/operator/sales", "/assets/sales.js"} {
		r, e := client.Get(base + path)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 || len(b) == 0 {
			t.Fatal("frontend asset unavailable", path)
		}
	}
	dir := filepath.Dir(os.Getenv("JWT_PUBLIC_KEY_FILE"))
	ca, e := os.ReadFile(filepath.Join(dir, "service-ca.pem"))
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid CA")
	}
	for _, name := range []string{"order", "gateway"} {
		cert, e := tls.LoadX509KeyPair(filepath.Join(dir, name+".pem"), filepath.Join(dir, name+"-key.pem"))
		if e != nil {
			t.Fatal(e)
		}
		transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{cert}}}
		private := &http.Client{Transport: transport, Timeout: 3 * time.Second}
		url := os.Getenv("FLASH_SALE_INTERNAL_URL")
		if url == "" {
			url = "https://127.0.0.1:8442"
		}
		r, e := private.Get(url + "/internal/offers/" + saleID)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		transport.CloseIdleConnections()
		want := 403
		if name == "order" {
			want = 200
		}
		if r.StatusCode != want {
			t.Fatal(name, r.StatusCode)
		}
	}
}
