package market

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"flashkart/internal/identity"
)

func TestRoleEnforcement(t *testing.T) {
	a := &identity.Auth{Mode: "demo", DemoAdmin: "admin", DemoCustomer: "customer", DemoCustomerID: "user_customer"}
	for _, test := range []struct {
		token  string
		status int
	}{{"", 401}, {"customer", 403}, {"admin", 204}, {"forged", 401}} {
		r := httptest.NewRequest("GET", "/api/admin/test", nil)
		if test.token != "" {
			r.Header.Set("Authorization", "Bearer "+test.token)
		}
		w := httptest.NewRecorder()
		a.Middleware(admin("", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("token %q: status %d, want %d", test.token, w.Code, test.status)
		}
	}
	for _, token := range []string{"", "customer"} {
		r := httptest.NewRequest("GET", "/api/analytics/sales/test", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		a.Middleware((&Analytics{}).Handler("", http.NotFoundHandler())).ServeHTTP(w, r)
		if w.Code != 401 && w.Code != 403 {
			t.Fatalf("analytics exposed: %d", w.Code)
		}
	}
}
