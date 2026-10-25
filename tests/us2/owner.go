package us2

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flash-sale/internal/platform/value"
	"flash-sale/tests/adapters"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OwnerExpectation identifies a complete isolated E1 dataset. Call after each
// controlled transition; neither canned rows nor fixture mode certify an owner.
type OwnerExpectation struct {
	RunID, SaleID, ProductID string
	Allocated, Cap           int64
	ShopperIDs, OrderIDs     []string
}
type reservationEvidence struct {
	ReservationID string `json:"reservation_id"`
	OrderID       string `json:"order_id"`
	ShopperID     string `json:"shopper_id"`
	SaleID        string `json:"sale_id"`
	ProductID     string `json:"product_id"`
	Quantity      int64  `json:"quantity"`
	State         string `json:"state"`
	ExpiresAt     string `json:"expires_at"`
}
type reserveEvidence struct {
	OrderID       string `json:"order_id"`
	PayloadHash   string `json:"payload_hash"`
	ResultCode    string `json:"result_code"`
	ReservationID string `json:"reservation_id"`
}

// VerifyOwner requires live E1 and complete matching ledgers, then checks stock,
// current held/confirmed allowance and coverage of rejected and successful keys.
func VerifyOwner(ctx context.Context, a adapters.Adapter, client *http.Client, want OwnerExpectation) error {
	if a.Name != "inventory" {
		return &adapters.Unavailable{Dependency: "inventory", Reason: "Inventory owner adapter required"}
	}
	if err := a.RequireReal(); err != nil {
		return err
	}
	insufficient := func(reason string) error {
		return &adapters.Unavailable{Dependency: "inventory", Reason: "insufficient evidence: " + reason}
	}
	if want.RunID == "" || value.UUID(want.SaleID) != nil || value.UUID(want.ProductID) != nil || want.Allocated < 1 || want.Cap < 1 || len(want.ShopperIDs) == 0 || len(want.OrderIDs) == 0 {
		return insufficient("explicit run, sale, shoppers and proposed orders required")
	}
	resp, err := a.Request(ctx, client, "GET", a.VerifyPath)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var evidence struct {
		adapters.Snapshot
		RunID        string                `json:"run_id"`
		SaleID       string                `json:"sale_id"`
		ProductID    string                `json:"product_id"`
		SampledAt    string                `json:"sampled_at"`
		Reservations []reservationEvidence `json:"reservations"`
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if readErr != nil || len(raw) > 4<<20 {
		return insufficient("snapshot exceeds response bound")
	}
	if err = json.Unmarshal(raw, &evidence); err != nil {
		return insufficient("malformed snapshot")
	}
	if evidence.RunID != want.RunID || evidence.SaleID != want.SaleID || evidence.ProductID != want.ProductID || value.UTC(evidence.SampledAt) != nil || evidence.Allocated != want.Allocated || evidence.Reservations == nil {
		return insufficient("missing or mismatched dataset/authoritative time/reservations")
	}
	if err = evidence.Snapshot.Verify(); err != nil {
		return insufficient(err.Error())
	}
	now, _ := time.Parse(time.RFC3339Nano, evidence.SampledAt)
	held, sold := int64(0), int64(0)
	byShopper := map[string][2]int64{}
	reservations := map[string]string{}
	orders := map[string]bool{}
	for _, r := range evidence.Reservations {
		if value.UUID(r.ReservationID) != nil || value.UUID(r.OrderID) != nil || value.UUID(r.ShopperID) != nil || r.SaleID != want.SaleID || r.ProductID != want.ProductID || r.Quantity < 1 || r.Quantity > 100 || value.UTC(r.ExpiresAt) != nil || reservations[r.ReservationID] != "" || orders[r.OrderID] {
			return insufficient("invalid or repeated reservation")
		}
		reservations[r.ReservationID] = r.OrderID
		orders[r.OrderID] = true
		expiry, _ := time.Parse(time.RFC3339Nano, r.ExpiresAt)
		totals := byShopper[r.ShopperID]
		switch r.State {
		case "HELD":
			held += r.Quantity
			if now.Before(expiry) {
				totals[0] += r.Quantity
			}
		case "CONFIRMED":
			sold += r.Quantity
			totals[1] += r.Quantity
		case "RELEASED", "EXPIRED":
		default:
			return insufficient("unknown reservation state")
		}
		byShopper[r.ShopperID] = totals
	}
	if held != evidence.Held || sold != evidence.Sold {
		return insufficient("reservation ledger does not cover stock totals")
	}
	rows := map[string]bool{}
	for _, r := range evidence.Allowances {
		totals := byShopper[r.ShopperID]
		if r.SaleID != want.SaleID || r.Cap != want.Cap || r.UnexpiredHeld != totals[0] || r.Confirmed != totals[1] {
			return insufficient("allowance ledger differs from reservations/cap")
		}
		rows[r.ShopperID] = true
	}
	for shopper := range byShopper {
		if !rows[shopper] {
			return insufficient("reservation shopper absent from allowance ledger")
		}
	}
	for _, shopper := range want.ShopperIDs {
		if !rows[shopper] {
			return insufficient("tested shopper absent from allowance ledger")
		}
	}
	var outcomes []reserveEvidence
	if json.Unmarshal(evidence.ReserveResults, &outcomes) != nil || outcomes == nil {
		return insufficient("structured stable Reserve outcomes required")
	}
	seen := map[string]bool{}
	for _, o := range outcomes {
		_, hashErr := hex.DecodeString(o.PayloadHash)
		if value.UUID(o.OrderID) != nil || len(o.PayloadHash) != 64 || hashErr != nil || seen[o.OrderID] {
			return insufficient("invalid Reserve identity/hash")
		}
		seen[o.OrderID] = true
		switch o.ResultCode {
		case "APPLIED", "REPLAYED":
			if reservations[o.ReservationID] != o.OrderID {
				return insufficient("successful outcome has no matching reservation")
			}
		case "PURCHASE_LIMIT_EXCEEDED", "SOLD_OUT", "INSUFFICIENT_STOCK", "SALE_INACTIVE", "SALE_NOT_READY", "OFFER_VERSION_MISMATCH":
			if o.ReservationID != "" || orders[o.OrderID] {
				return insufficient("rejection contains a reservation")
			}
		default:
			return insufficient("unknown stable Reserve code")
		}
	}
	for id := range orders {
		if !seen[id] {
			return insufficient("reservation has no stable Reserve outcome")
		}
	}
	for _, id := range want.OrderIDs {
		if !seen[id] {
			return insufficient(fmt.Sprintf("tested proposed order %s absent", id))
		}
	}
	return nil
}
