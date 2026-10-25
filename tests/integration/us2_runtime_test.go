//go:build runtime

package integration

import (
	"bytes"
	"encoding/json"
	"flash-sale/internal/platform/auth"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUS2PrivatePurchaseServiceIdentities(t *testing.T) {
	dir := filepath.Dir(os.Getenv("JWT_PUBLIC_KEY_FILE"))
	admissionURL, orderURL := os.Getenv("ADMISSION_INTERNAL_URL"), os.Getenv("ORDER_INTERNAL_URL")
	if admissionURL == "" {
		admissionURL = "https://127.0.0.1:8441"
	}
	if orderURL == "" {
		orderURL = "https://127.0.0.1:8443"
	}
	for _, tc := range []struct {
		caller, base, path string
		want               int
	}{
		{"gateway", admissionURL, "/v1/purchases", 401}, // mTLS accepted; missing shopper context denied.
		{"order", admissionURL, "/v1/purchases", 401},
		{"admission", orderURL, "/internal/orders", 400},
		{"gateway", orderURL, "/internal/orders", 401},
	} {
		tls, err := auth.ClientTLS(dir, tc.caller)
		if err != nil {
			t.Fatal(err)
		}
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: tls}, Timeout: 2 * time.Second}
		resp, err := client.Post(tc.base+tc.path, "application/json", bytes.NewBufferString(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		client.CloseIdleConnections()
		if err != nil || resp.StatusCode != tc.want {
			t.Fatalf("%s %s got %d %v %v", tc.caller, tc.path, resp.StatusCode, body, err)
		}
	}
}
