package events

import (
	"encoding/json"
	"flash-sale/contracts"
	"testing"
)

func TestExamplesAndQuarantine(t *testing.T) {
	raw, _ := contracts.Assets.ReadFile("examples.json")
	var fixtures struct {
		Events []json.RawMessage `json:"events"`
	}
	_ = json.Unmarshal(raw, &fixtures)
	for _, raw := range fixtures.Events {
		var v map[string]any
		_ = json.Unmarshal(raw, &v)
		raw2 := append([]byte(nil), raw...)
		var e Envelope
		_ = json.Unmarshal(raw, &e)
		topic, key := Destination(e)
		if e.Type == "AttemptRecorded" {
			var p AttemptRecorded
			_ = json.Unmarshal(e.Data, &p)
			if p.AttemptID != e.AggregateID {
				if _, err := Decode(topic, key, raw); err == nil {
					t.Fatal("mismatched fixture accepted")
				}
				continue
			}
		}
		if _, err := Decode(topic, key, raw2); err != nil {
			t.Fatalf("%s: %v", e.Type, err)
		}
		v["optional_future_field"] = true
		extra, _ := json.Marshal(v)
		if _, err := Decode(topic, key, extra); err != nil {
			t.Fatal("optional field", err)
		}
		v["schema_version"] = 2
		bad, _ := json.Marshal(v)
		if _, err := Decode(topic, key, bad); err == nil {
			t.Fatal("unknown major")
		}
		if _, err := Decode("payment.events.v1", key, raw); err == nil && topic != "payment.events.v1" {
			t.Fatal("wrong topic")
		}
	}
}

func TestRequiredCapAndResponseOutcome(t *testing.T) {
	raw, _ := contracts.Assets.ReadFile("examples.json")
	var f struct {
		Events []map[string]any `json:"events"`
	}
	_ = json.Unmarshal(raw, &f)
	for _, v := range f.Events {
		kind := v["type"]
		data := v["data"].(map[string]any)
		if kind == "SaleConfigured" || kind == "SaleActivated" {
			delete(data, "per_shopper_unit_cap")
		} else if kind == "AttemptRecorded" && data["response_status"].(float64) >= 400 {
			v["aggregate_id"] = data["attempt_id"]
			delete(data, "response_error_code")
		} else {
			continue
		}
		bad, _ := json.Marshal(v)
		var en Envelope
		_ = json.Unmarshal(bad, &en)
		topic, key := Destination(en)
		if _, e := Decode(topic, key, bad); e == nil {
			t.Fatal("required field accepted", kind)
		}
	}
}

func TestRevenueDictionaryKeepsConstraints(t *testing.T) {
	raw, _ := contracts.Assets.ReadFile("examples.json")
	var fixtures struct {
		Events []map[string]any `json:"events"`
	}
	if e := json.Unmarshal(raw, &fixtures); e != nil {
		t.Fatal(e)
	}
	for _, v := range fixtures.Events {
		if v["type"] != "WindowSummaryUpdated" {
			continue
		}
		data := v["data"].(map[string]any)
		for _, revenue := range []map[string]any{{"usd": "wrong"}, {"USD": -1}, {"USD": 9007199254740992.0}, {"USD": 1.5}} {
			data["revenue_by_currency"] = revenue
			b, _ := json.Marshal(v)
			var en Envelope
			_ = json.Unmarshal(b, &en)
			topic, key := Destination(en)
			if _, e := Decode(topic, key, b); e == nil {
				t.Fatalf("invalid currency dictionary accepted: %v", revenue)
			}
		}
	}
}
