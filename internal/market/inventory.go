package market

import (
	"context"
	"encoding/json"
	"errors"

	pb "flashkart/gen/flashkart/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Inventory struct {
	pb.UnimplementedInventoryServer
	DB *pgxpool.Pool
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
