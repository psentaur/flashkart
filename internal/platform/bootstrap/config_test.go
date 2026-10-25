package bootstrap

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMissingCredentialsFailWithoutLeakingURL(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("ADMISSION_DATABASE_URL", "postgres://admission:secret@example.test/")
	t.Setenv("KAFKA_BROKERS", "localhost:19092")
	_, err := Load("admission")
	if err == nil {
		t.Fatal("empty database path accepted")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatal("credential leaked")
	}
}

func TestInvalidBrokerAndProductionSettingsFailClosed(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("KAFKA_BROKERS", "localhost:not-a-port")
	if _, err := Load("analytics"); err == nil {
		t.Fatal("invalid broker accepted")
	}
	t.Setenv("APP_ENV", "production")
	if _, err := Load("analytics"); err == nil {
		t.Fatal("unfinished production runtime accepted")
	}
}

func TestGatewayRejectsEmbeddedCredentials(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	for _, key := range []string{"ADMISSION_URL", "FLASH_SALE_URL", "ORDER_URL", "CATALOG_URL", "NOTIFICATION_URL"} {
		t.Setenv(key, "http://localhost:8090")
	}
	t.Setenv("CATALOG_URL", "http://shopper:secret@localhost:8090")
	if _, err := Load("gateway"); err == nil {
		t.Fatal("credential-bearing peer URL accepted")
	}
}

func TestScaffoldIsLiveButNeverApplicationReady(t *testing.T) {
	handler := Handler("analytics", nil)
	for path, want := range map[string]int{"/health/live": http.StatusOK, "/health/ready": http.StatusServiceUnavailable, "/v1/purchases": http.StatusNotFound} {
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, httptest.NewRequest(http.MethodGet, path, nil))
		if result.Code != want {
			t.Errorf("%s: got %d, want %d", path, result.Code, want)
		}
	}
}
