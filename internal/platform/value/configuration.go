package value

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ConfigurationDigest implements the shared readiness wire algorithm. Maps make
// encoding/json sort nested keys too; timestamps use the shortest UTC RFC3339 form.
func ConfigurationDigest(raw []byte) (string, error) {
	doc, e := JSON(raw)
	if e != nil {
		return "", e
	}
	source, ok := doc.(map[string]any)
	if !ok {
		return "", fmt.Errorf("configuration object required")
	}
	terms := map[string]any{}
	for _, k := range []string{"sale_id", "product_id", "version", "unit_price_minor", "currency", "allocated_units", "per_shopper_unit_cap", "starts_at", "ends_at", "admission_limits"} {
		v, ok := source[k]
		if !ok {
			return "", fmt.Errorf("missing configuration field: %s", k)
		}
		terms[k] = v
	}
	for _, k := range []string{"sale_id", "product_id"} {
		s, ok := terms[k].(string)
		if !ok || UUID(s) != nil {
			return "", fmt.Errorf("invalid configuration ID")
		}
		terms[k] = strings.ToLower(s)
	}
	for _, k := range []string{"starts_at", "ends_at"} {
		s, ok := terms[k].(string)
		if !ok || UTC(s) != nil {
			return "", fmt.Errorf("invalid configuration time")
		}
		t, _ := time.Parse(time.RFC3339Nano, s)
		terms[k] = t.UTC().Format(time.RFC3339Nano)
	}
	b, e := json.Marshal(terms)
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
