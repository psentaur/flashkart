package market

import (
	"errors"
	"strings"
	"time"

	pb "flashkart/gen/flashkart/v1"
)

type Terms struct {
	ProductID  string `json:"product_id"`
	PriceMinor *int64 `json:"price_minor"`
	Currency   string `json:"currency"`
	Stock      uint32 `json:"stock"`
	StartsAt   string `json:"starts_at"`
	EndsAt     string `json:"ends_at"`
}

type Sale struct {
	ID    string `json:"sale_id"`
	Terms Terms  `json:"terms"`
}

func validID(id string) bool {
	return strings.TrimSpace(id) != "" && !strings.ContainsRune(id, 0) && len(id) <= 200
}

func (t *Terms) Normalize() error {
	if !validID(t.ProductID) || t.PriceMinor == nil || *t.PriceMinor < 0 || t.Stock == 0 ||
		len(t.Currency) != 3 || strings.IndexFunc(t.Currency, func(r rune) bool { return r < 'A' || r > 'Z' }) >= 0 {
		return errors.New("product, price, currency, and stock are required")
	}
	start, err1 := time.Parse(time.RFC3339Nano, t.StartsAt)
	end, err2 := time.Parse(time.RFC3339Nano, t.EndsAt)
	if err1 != nil || err2 != nil || !end.After(start) {
		return errors.New("sale end must follow its start")
	}
	t.StartsAt = start.UTC().Format(time.RFC3339Nano)
	t.EndsAt = end.UTC().Format(time.RFC3339Nano)
	return nil
}

func (s Sale) protobuf() *pb.SaleTerms {
	return &pb.SaleTerms{SaleId: s.ID, ProductId: s.Terms.ProductID, PriceMinor: *s.Terms.PriceMinor,
		Currency: s.Terms.Currency, Stock: s.Terms.Stock, StartsAt: s.Terms.StartsAt, EndsAt: s.Terms.EndsAt}
}
