package value

import (
	"encoding/json"
	"flash-sale/contracts"
	"strings"
	"testing"
)

func TestConfigurationDigestCanonicalTimesAndCap(t *testing.T) {
	raw, e := contracts.Assets.ReadFile("examples.json")
	if e != nil {
		t.Fatal(e)
	}
	var examples struct {
		Events []struct {
			Type string
			Data json.RawMessage
		}
	}
	if e = json.Unmarshal(raw, &examples); e != nil {
		t.Fatal(e)
	}
	var original []byte
	for _, event := range examples.Events {
		if event.Type == "SaleConfigured" {
			original = event.Data
			break
		}
	}
	expected, e := ConfigurationDigest(original)
	if e != nil {
		t.Fatal(e)
	}
	var data map[string]any
	if e = json.Unmarshal(original, &data); e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"starts_at", "ends_at"} {
		data[key] = strings.Replace(data[key].(string), "Z", ".000000000Z", 1)
	}
	data["sale_id"] = strings.ToUpper(data["sale_id"].(string))
	data["status"] = "ENDED"
	data["ready_participants"] = []string{"inventory"}
	normalized, _ := json.Marshal(data)
	if actual, e := ConfigurationDigest(normalized); e != nil || actual != expected {
		t.Fatal("equivalent configuration changed digest", actual, e)
	}
	data["per_shopper_unit_cap"] = 2
	changed, _ := json.Marshal(data)
	if actual, e := ConfigurationDigest(changed); e != nil || actual == expected {
		t.Fatal("cap not in digest", actual, e)
	}
	delete(data, "per_shopper_unit_cap")
	missing, _ := json.Marshal(data)
	if _, e := ConfigurationDigest(missing); e == nil {
		t.Fatal("missing cap accepted")
	}
}
