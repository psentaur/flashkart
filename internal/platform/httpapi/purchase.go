package httpapi

import (
	"encoding/json"
	"flash-sale/internal/platform/value"
	"io"
	"mime"
	"net/http"
	"strings"
)

// Purchase contains only reviewed client terms. A shopper cannot set allowance or payment mode.
type Purchase struct {
	SaleID                 string `json:"sale_id"`
	ProductID              string `json:"product_id"`
	Quantity               int64  `json:"quantity"`
	ExpectedOfferVersion   int64  `json:"expected_offer_version"`
	ExpectedUnitPriceMinor int64  `json:"expected_unit_price_minor"`
	Currency               string `json:"currency"`
}

func ReadPurchase(w http.ResponseWriter, r *http.Request) (Purchase, error) {
	var p Purchase
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(r.Header.Values("Idempotency-Key")) != 1 || value.RequestKey(r.Header.Get("Idempotency-Key")) != nil || len(r.URL.Query()) != 0 {
		return p, io.ErrUnexpectedEOF
	}
	err = Decode(w, r, "Purchase", &p)
	p.SaleID = strings.ToLower(p.SaleID)
	p.ProductID = strings.ToLower(p.ProductID)
	if err == nil {
		_, err = value.Total(p.ExpectedUnitPriceMinor, p.Quantity)
	}
	return p, err
}

type Result struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
	Header http.Header     `json:"header"`
}

func ErrorResult(r *http.Request, code string, offer json.RawMessage) Result {
	w := &buffer{header: make(http.Header)}
	WriteError(w, r, code, strings.ReplaceAll(strings.ToLower(code), "_", " "), offer)
	return Result{w.status, w.body, w.header}
}

type buffer struct {
	header http.Header
	status int
	body   []byte
}

func (w *buffer) Header() http.Header         { return w.header }
func (w *buffer) WriteHeader(s int)           { w.status = s }
func (w *buffer) Write(b []byte) (int, error) { w.body = append(w.body, b...); return len(b), nil }
func (r Result) Write(w http.ResponseWriter) {
	for _, k := range []string{"Location", "X-Attempt-ID", "Retry-After"} {
		if v := r.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(r.Status)
	_, _ = w.Write(r.Body)
}
func (r Result) ErrorCode() string { var e Error; _ = json.Unmarshal(r.Body, &e); return e.Code }
func ReadResult(resp *http.Response) (Result, error) {
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return Result{}, err
	}
	schema := "Error"
	if resp.StatusCode == 200 || resp.StatusCode == 202 {
		schema = "Order"
	}
	if len(b) > 1<<20 || value.Validate(schema, b) != nil {
		return Result{}, io.ErrUnexpectedEOF
	}
	switch resp.StatusCode {
	case 200, 202, 400, 401, 403, 404, 409, 429, 503:
	default:
		return Result{}, io.ErrUnexpectedEOF
	}
	return Result{resp.StatusCode, b, resp.Header.Clone()}, nil
}
