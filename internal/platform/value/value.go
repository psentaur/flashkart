// Package value implements the shared wire-value rules, without domain state.
package value

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flash-sale/contracts"
	"fmt"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
)

const MaxSafeInteger int64 = 9007199254740991

var currency = regexp.MustCompile(`^[A-Z]{3}$`)

// UUID enforces "IDs are UUID strings".
func UUID(s string) error {
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return fmt.Errorf("invalid UUID")
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err == nil && len(decoded) != 16 {
		return fmt.Errorf("invalid UUID length")
	}
	return err
}

// UTC RFC 3339 timestamps on the wire must explicitly end in Z.
func UTC(s string) error {
	if !strings.HasSuffix(s, "Z") {
		return fmt.Errorf("UTC timestamp required")
	}
	_, e := time.Parse(time.RFC3339Nano, s)
	return e
}

// RequestKey enforces "Request keys are 1-128 printable ASCII characters."
func RequestKey(s string) error {
	if len(s) < 1 || len(s) > 128 {
		return fmt.Errorf("invalid request key length")
	}
	for _, b := range []byte(s) {
		if b < 32 || b > 126 {
			return fmt.Errorf("invalid request key character")
		}
	}
	return nil
}

type Money struct {
	Minor    int64
	Currency string
}

// Validate requires money to be between 0 and 9,007,199,254,740,991.
func (m Money) Validate() error {
	if m.Minor < 0 || m.Minor > MaxSafeInteger || !currency.MatchString(m.Currency) {
		return fmt.Errorf("invalid money")
	}
	return nil
}

// Total enforces "Sale unit prices are positive." Quantity is an integer from 1 to 100 in this release.
func Total(price, quantity int64) (int64, error) {
	if price < 1 || price > MaxSafeInteger || quantity < 1 || quantity > 100 || price > MaxSafeInteger/quantity {
		return 0, fmt.Errorf("invalid price, quantity or total overflow")
	}
	return price * quantity, nil
}

// Add enforces "No cross-currency addition is permitted."
func Add(a, b Money) (Money, error) {
	if a.Validate() != nil || b.Validate() != nil || a.Currency != b.Currency || a.Minor > MaxSafeInteger-b.Minor {
		return Money{}, fmt.Errorf("invalid currency or sum overflow")
	}
	return Money{a.Minor + b.Minor, a.Currency}, nil
}

var schemas sync.Map

// Schema compiles the embedded reviewed schema; additive event fields are tolerated
// only on known event majors, while REST inputs keep additionalProperties=false.
func Schema(document, fragment string, additive bool) (*jsonschema.Schema, error) {
	key := fmt.Sprintf("%s#%s:%t", document, fragment, additive)
	if s, ok := schemas.Load(key); ok {
		return s.(*jsonschema.Schema), nil
	}
	raw, e := contracts.Assets.ReadFile(document)
	if e != nil {
		return nil, e
	}
	doc, e := JSON(raw)
	if e != nil {
		return nil, e
	}
	if additive {
		allowAdditions(doc)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	compiler.RegisterFormat(&jsonschema.Format{Name: "date-time", Validate: func(v any) error {
		if s, ok := v.(string); ok {
			return UTC(s)
		}
		return nil
	}})
	if e = compiler.AddResource("https://flash-sale.example/contracts/"+document, doc); e != nil {
		return nil, e
	}
	s, e := compiler.Compile("https://flash-sale.example/contracts/" + document + "#" + fragment)
	if e != nil {
		return nil, e
	}
	schemas.Store(key, s)
	return s, nil
}
func allowAdditions(v any) {
	switch x := v.(type) {
	case map[string]any:
		_, record := x["properties"]
		if x["additionalProperties"] == false && record && x["patternProperties"] == nil && x["propertyNames"] == nil {
			x["additionalProperties"] = true
		}
		for _, v := range x {
			allowAdditions(v)
		}
	case []any:
		for _, v := range x {
			allowAdditions(v)
		}
	}
}

// JSON rejects multiple documents and keeps integer precision through json.Number.
func JSON(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if e := d.Decode(&v); e != nil {
		return nil, e
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	return v, nil
}
func Validate(name string, raw []byte) error {
	s, e := Schema("public.openapi.json", "/components/schemas/"+name, false)
	if e != nil {
		return e
	}
	v, e := JSON(raw)
	if e != nil {
		return e
	}
	return s.Validate(v)
}
