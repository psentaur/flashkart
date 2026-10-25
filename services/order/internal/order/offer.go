package order

import (
	"context"
	"encoding/json"
	"errors"
	"flash-sale/internal/platform/value"
	"io"
	"net/http"
	"strings"
)

var ErrOfferUnavailable = errors.New("authoritative offer unavailable")

func OfferClient(client *http.Client, base string) func(context.Context, string, string) (Offer, error) {
	return func(ctx context.Context, id, correlation string) (Offer, error) {
		var o Offer
		r, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/internal/offers/"+id, nil)
		if err != nil {
			return o, err
		}
		r.Header.Set("X-Correlation-ID", correlation)
		if d, ok := ctx.Deadline(); ok {
			r.Header.Set("X-Request-Deadline", d.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
		}
		resp, err := client.Do(r)
		if err != nil {
			return o, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		if err != nil || resp.StatusCode != 200 || len(b) > 1<<20 || value.Validate("Offer", b) != nil {
			return o, ErrOfferUnavailable
		}
		err = json.Unmarshal(b, &o)
		return o, err
	}
}
