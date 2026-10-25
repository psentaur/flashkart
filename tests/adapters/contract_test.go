package adapters

import (
	"errors"
	"testing"
)

func TestUnavailableAndAllowance(t *testing.T) {
	a := Adapter{Name: "inventory", Mode: "fixture", URL: "http://127.0.0.1:8092", HealthPath: "/health", FaultPath: "/fault", VerifyPath: "/verify"}
	var unavailable *Unavailable
	if !errors.As(a.RequireReal(), &unavailable) {
		t.Fatal("fixture accepted as real")
	}
	s := Snapshot{Allocated: 100, Available: 99, Held: 1, Allowances: []Allowance{{ShopperID: "11111111-1111-4111-8111-111111111111", SaleID: "22222222-2222-4222-8222-222222222222", UnexpiredHeld: 1, Cap: 1}}, ReserveResults: []byte(`[]`)}
	if e := s.Verify(); e != nil {
		t.Fatal(e)
	}
	s.Allowances[0].Confirmed = 1
	if s.Verify() == nil {
		t.Fatal("cap exceeded")
	}
}
