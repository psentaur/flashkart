package market

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Inventory struct {
	pb.UnimplementedInventoryServer
	DB           *pgxpool.Pool
	HoldDuration time.Duration
	// AdminToken authorizes ending a sale early. Without it the endpoint refuses every request.
	AdminToken string
}

func (i Inventory) PrepareSale(ctx context.Context, req *pb.PrepareSaleRequest) (*pb.Ack, error) {
	t, op := req.GetTerms(), req.GetCall().GetOperationId()
	if !validID(t.GetSaleId()) || !validID(op) {
		return nil, status.Error(codes.InvalidArgument, "sale and operation IDs are required")
	}
	amount := t.PriceMinor
	terms := Terms{ProductID: t.ProductId, PriceMinor: &amount, Currency: t.Currency, Stock: t.Stock,
		StartsAt: t.StartsAt, EndsAt: t.EndsAt}
	if err := terms.Normalize(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	data, err := json.Marshal(terms)
	if err != nil {
		return nil, status.Error(codes.Internal, "encode sale terms")
	}
	err = pgx.BeginFunc(ctx, i.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO inventory.sales(sale_id,operation_id,terms,available_stock)
			VALUES ($1,$2,$3::jsonb,$4) ON CONFLICT DO NOTHING`, t.SaleId, op, data, terms.Stock); err != nil {
			return err
		}
		var matches bool
		err := tx.QueryRow(ctx, `SELECT operation_id=$2 AND terms=$3::jsonb FROM inventory.sales WHERE sale_id=$1`,
			t.SaleId, op, data).Scan(&matches)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && !matches {
			return status.Error(codes.AlreadyExists, "sale or operation already has different immutable terms")
		}
		return err
	})
	if err != nil {
		if s, ok := status.FromError(err); ok {
			return nil, s.Err()
		}
		return nil, status.Error(codes.Unavailable, "inventory preparation could not be confirmed")
	}
	return &pb.Ack{}, nil
}

func (i Inventory) ActivateSale(ctx context.Context, req *pb.ActivateSaleRequest) (*pb.Ack, error) {
	op := req.GetCall().GetOperationId()
	if !validID(req.GetSaleId()) || !validID(op) {
		return nil, status.Error(codes.InvalidArgument, "sale and operation IDs are required")
	}
	tag, err := i.DB.Exec(ctx, `UPDATE inventory.sales SET active=true WHERE sale_id=$1 AND operation_id=$2`,
		req.SaleId, op)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "inventory activation could not be confirmed")
	}
	if tag.RowsAffected() != 1 {
		return nil, status.Error(codes.FailedPrecondition, "matching prepared sale is required")
	}
	return &pb.Ack{}, nil
}

// Handler serves public stock levels: GET /api/inventory/sales and GET /api/inventory/sales/{sale_id}.
// Counts come from the authoritative reservation tables, so the storefront shows what is left to reserve.
func (i Inventory) Handler(fallback http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/inventory/sales", func(w http.ResponseWriter, r *http.Request) {
		levels, err := i.stockLevels(r.Context(), "")
		if err != nil {
			writeError(w, r, 503, "UNAVAILABLE", "stock levels unavailable")
			return
		}
		send(w, 200, levels)
	})
	mux.HandleFunc("POST /api/inventory/sales/{sale_id}/end", admin(i.AdminToken, i.endSale))
	mux.HandleFunc("GET /api/inventory/sales/{sale_id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("sale_id")
		if !validID(id) {
			writeError(w, r, 400, "INVALID_ARGUMENT", "invalid sale ID")
			return
		}
		levels, err := i.stockLevels(r.Context(), id)
		if err != nil {
			writeError(w, r, 503, "UNAVAILABLE", "stock levels unavailable")
			return
		}
		if len(levels) == 0 {
			writeError(w, r, 404, "NOT_FOUND", "sale not found in inventory")
			return
		}
		send(w, 200, levels[0])
	})
	return serveAPI(mux, fallback)
}

// endSale stops a published sale early. It locks the sale row exactly as reservations do, so a
// reservation either completes before the sale ends or is rejected after it; stock is never oversold.
// Reservations already held finish normally. Published terms stay frozen: to change the price or stock,
// end the sale and publish a new one.
func (i Inventory) endSale(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("sale_id")
	if !validID(id) {
		writeError(w, r, 400, "INVALID_ARGUMENT", "invalid sale ID")
		return
	}
	ctx := r.Context()
	var found, active bool
	err := pgx.BeginFunc(ctx, i.DB, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, "SELECT active FROM inventory.sales WHERE sale_id=$1 FOR UPDATE", id).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		if !active {
			return nil
		}
		_, err = tx.Exec(ctx, "UPDATE inventory.sales SET active=false WHERE sale_id=$1", id)
		return err
	})
	switch {
	case err != nil:
		writeError(w, r, 503, "UNAVAILABLE", "the sale could not be ended")
	case !found:
		writeError(w, r, 404, "NOT_FOUND", "sale not found in inventory")
	case !active:
		writeError(w, r, 409, "SALE_NOT_ACTIVE", "the sale is not selling: it has already ended or is still being published")
	default:
		levels, err := i.stockLevels(ctx, id)
		if err != nil || len(levels) == 0 {
			writeError(w, r, 503, "UNAVAILABLE", "stock levels unavailable")
			return
		}
		send(w, 200, levels[0])
	}
}

type stockLevel struct {
	SaleID    string `json:"sale_id"`
	Stock     int64  `json:"stock"`
	Available int64  `json:"available"`
	Held      int64  `json:"held"`
	Sold      int64  `json:"sold"`
	Active    bool   `json:"active"`
}

func (i Inventory) stockLevels(ctx context.Context, saleID string) ([]stockLevel, error) {
	rows, err := i.DB.Query(ctx, `SELECT s.sale_id,(s.terms->>'stock')::bigint,s.available_stock,s.active,
 count(r.*) FILTER (WHERE r.state='HELD'),count(r.*) FILTER (WHERE r.state='COMMITTED')
 FROM inventory.sales s LEFT JOIN inventory.reservations r ON r.sale_id=s.sale_id
 WHERE $1='' OR s.sale_id=$1 GROUP BY s.sale_id ORDER BY s.sale_id`, saleID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (stockLevel, error) {
		var l stockLevel
		err := row.Scan(&l.SaleID, &l.Stock, &l.Available, &l.Active, &l.Held, &l.Sold)
		return l, err
	})
}
