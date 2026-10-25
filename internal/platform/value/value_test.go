package value

import (
	"strings"
	"testing"
)

func TestValueBoundaries(t *testing.T) {
	for _, s := range []string{"", "not-a-uuid", "11111111-1111-1111-1111-11111111111g"} {
		if UUID(s) == nil {
			t.Fatalf("invalid UUID %q", s)
		}
	}
	if UUID("11111111-1111-4111-8111-111111111111") != nil {
		t.Fatal("UUID")
	}
	for _, s := range []string{"2026-10-25T00:00:00+00:00", "2026-10-25", "2026-02-30T00:00:00Z"} {
		if UTC(s) == nil {
			t.Fatalf("invalid UTC %q", s)
		}
	}
	if UTC("2026-10-25T00:00:00.123Z") != nil {
		t.Fatal("UTC")
	}
	for _, s := range []string{"", "a\n", "é", string(make([]byte, 129))} {
		if RequestKey(s) == nil {
			t.Fatal("key accepted")
		}
	}
	if RequestKey(" !~") == nil {
	} else {
		t.Fatal("printable ASCII")
	}
	if _, e := Total(MaxSafeInteger, 2); e == nil {
		t.Fatal("overflow")
	}
	if _, e := Total(1, 101); e == nil {
		t.Fatal("quantity")
	}
	if n, e := Total(MaxSafeInteger, 1); e != nil || n != MaxSafeInteger {
		t.Fatal(n, e)
	}
	if _, e := Add(Money{1, "INR"}, Money{1, "USD"}); e == nil {
		t.Fatal("currency")
	}
	if _, e := Add(Money{MaxSafeInteger, "INR"}, Money{1, "INR"}); e == nil {
		t.Fatal("addition overflow")
	}
}
func TestReviewedRequiredAndOptionalFields(t *testing.T) {
	body := []byte(`{"sale_id":"11111111-1111-4111-8111-111111111111","product_id":"11111111-1111-4111-8111-111111111111","quantity":1,"expected_offer_version":1,"expected_unit_price_minor":1,"currency":"INR"}`)
	if err := Validate("Purchase", body); err != nil {
		t.Fatal(err)
	}
	if err := Validate("Purchase", []byte(`{}`)); err == nil {
		t.Fatal("missing required fields")
	}
	if err := Validate("Purchase", append(body, []byte(` {}`)...)); err == nil {
		t.Fatal("trailing JSON")
	}
}

func TestExactWireBounds(t *testing.T) {
	if UUID("111111--1111-4111-8111-111111111111") == nil {
		t.Fatal("short UUID accepted")
	}
	if RequestKey(strings.Repeat("~", 128)) != nil {
		t.Fatal("128-character request key rejected")
	}
	for _, m := range []Money{{-1, "INR"}, {MaxSafeInteger + 1, "INR"}, {1, "inr"}, {1, "IN"}} {
		if m.Validate() == nil {
			t.Fatal("invalid money", m)
		}
	}
	if _, e := Total(0, 1); e == nil {
		t.Fatal("free sale price")
	}
}
