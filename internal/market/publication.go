package market

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"flashkart/internal/replica"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type Publisher struct {
	DB        *pgxpool.Pool
	Replicas  *replica.Client
	Inventory pb.InventoryClient
	Log       *slog.Logger
}

const (
	stepInventory = iota
	stepPrepare
	stepApply
	stepActivate
	stepPublished
)

type draft struct {
	Sale
	State string `json:"state"`
}

type publication struct {
	Operation string `json:"operation_id"`
	SaleID    string `json:"sale_id"`
	State     string `json:"state"`
}

type job struct {
	Sale
	Operation   string
	Origin      string
	Step        int
	Envelope    []byte
	Correlation string
}

var errImmutable = errors.New("sale terms are frozen")
var errOperation = errors.New("publication operation conflicts with an existing sale")

func (p Publisher) Handler(token string, fallback http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/admin/sale-drafts", admin(token, p.saveDraft(false)))
	mux.HandleFunc("PATCH /api/admin/sale-drafts/{sale_id}", admin(token, p.saveDraft(true)))
	mux.HandleFunc("POST /api/admin/sale-drafts/{sale_id}/publish", admin(token, p.publish))
	mux.HandleFunc("GET /api/admin/publications/{operation_id}", admin(token, p.getPublication))
	return serveAPI(mux, fallback)
}

func publicationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, r, 404, "NOT_FOUND", "sale or publication not found")
	case errors.Is(err, errImmutable):
		writeError(w, r, 409, "SALE_IMMUTABLE", errImmutable.Error())
	case errors.Is(err, errOperation):
		writeError(w, r, 409, "IDEMPOTENCY_CONFLICT", errOperation.Error())
	default:
		writeError(w, r, 503, "UNAVAILABLE", "publication database operation could not be confirmed")
	}
}

func (p Publisher) saveDraft(update bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var terms Terms
		if !decode(w, r, &terms) {
			return
		}
		if err := terms.Normalize(); err != nil {
			writeError(w, r, 400, "INVALID_ARGUMENT", err.Error())
			return
		}
		id, code := rand.Text(), 201
		data, err := json.Marshal(terms)
		if err != nil {
			serviceError(w, r, err)
			return
		}
		if update {
			id, code = r.PathValue("sale_id"), 200
			err = pgx.BeginFunc(r.Context(), p.DB, func(tx pgx.Tx) error {
				var state string
				if err := tx.QueryRow(r.Context(), `SELECT state FROM flash_sale.drafts WHERE sale_id=$1 FOR UPDATE`, id).Scan(&state); err != nil {
					return err
				}
				if state != "DRAFT" {
					return errImmutable
				}
				_, err := tx.Exec(r.Context(), `UPDATE flash_sale.drafts SET terms=$2::jsonb WHERE sale_id=$1`, id, data)
				return err
			})
		} else {
			_, err = p.DB.Exec(r.Context(), `INSERT INTO flash_sale.drafts(sale_id,terms) VALUES ($1,$2::jsonb)`, id, data)
		}
		if err != nil {
			publicationError(w, r, err)
			return
		}
		send(w, code, draft{Sale{id, terms}, "DRAFT"})
	}
}

func (p Publisher) publish(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Operation string `json:"operation_id"`
	}
	if !decode(w, r, &input) {
		return
	}
	if !validID(input.Operation) {
		writeError(w, r, 400, "INVALID_ARGUMENT", "operation ID is required")
		return
	}
	ctx := r.Context()
	result := publication{Operation: input.Operation, SaleID: r.PathValue("sale_id")}
	err := pgx.BeginFunc(ctx, p.DB, func(tx pgx.Tx) error {
		var existing string
		err := tx.QueryRow(ctx, `SELECT state,coalesce(operation_id,'') FROM flash_sale.drafts WHERE sale_id=$1 FOR UPDATE`,
			result.SaleID).Scan(&result.State, &existing)
		if err != nil {
			return err
		}
		if result.State != "DRAFT" {
			if existing != result.Operation {
				return errOperation
			}
			return nil
		}
		// The operation and preparation origin commit before any external work.
		_, err = tx.Exec(ctx, `UPDATE flash_sale.drafts SET state='PENDING',operation_id=$2,origin=$3,
			correlation_id=$4,next_attempt_at=clock_timestamp() WHERE sale_id=$1`, result.SaleID, result.Operation,
			originFor("sale:"+result.Operation), r.Header.Get("X-Correlation-ID"))
		result.State = "PENDING"
		return err
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			err = errOperation
		}
		publicationError(w, r, err)
		return
	}
	send(w, 202, result)
}

func (p Publisher) getPublication(w http.ResponseWriter, r *http.Request) {
	result := publication{Operation: r.PathValue("operation_id")}
	err := p.DB.QueryRow(r.Context(), `SELECT sale_id,state FROM flash_sale.drafts WHERE operation_id=$1`,
		result.Operation).Scan(&result.SaleID, &result.State)
	if err != nil {
		publicationError(w, r, err)
		return
	}
	send(w, 200, result)
}

func (p Publisher) Run(ctx context.Context) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for ctx.Err() == nil {
		if err := p.processDue(ctx); err != nil && ctx.Err() == nil {
			p.Log.Warn("publication queue could not be read")
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (p Publisher) processDue(ctx context.Context) error {
	rows, err := p.DB.Query(ctx, `SELECT sale_id,terms,operation_id,origin,step,envelope,correlation_id
		FROM flash_sale.drafts WHERE state='PENDING' AND next_attempt_at<=clock_timestamp()
		ORDER BY next_attempt_at,sale_id LIMIT 8`)
	if err != nil {
		return err
	}
	defer rows.Close()
	jobs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (job, error) {
		var j job
		err := row.Scan(&j.ID, &j.Terms, &j.Operation, &j.Origin, &j.Step, &j.Envelope, &j.Correlation)
		return j, err
	})
	if err != nil {
		return err
	}
	for _, j := range jobs {
		work, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := p.advance(work, j)
		cancel()
		if err != nil && ctx.Err() == nil {
			p.Log.Warn("publication step will retry", "operation_id", j.Operation, "step", j.Step, "code", status.Code(err).String())
			if _, err := p.DB.Exec(ctx, `UPDATE flash_sale.drafts SET next_attempt_at=clock_timestamp()+interval '1 second'
				WHERE sale_id=$1 AND step=$2`, j.ID, j.Step); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p Publisher) advance(ctx context.Context, j job) error {
	call := &pb.Call{OperationId: j.Operation, CorrelationId: j.Correlation}
	var envelope []byte
	var err error
	switch j.Step {
	case stepInventory:
		_, err = p.Inventory.PrepareSale(ctx, &pb.PrepareSaleRequest{Call: call, Terms: j.Sale.protobuf()})
	case stepPrepare:
		var body []byte
		body, err = json.Marshal(j.Sale)
		if err != nil {
			return err
		}
		var prepared *pb.Mutation
		prepared, err = p.Replicas.Prepare(ctx, j.Origin, &pb.PrepareMutationRequest{Call: &pb.Call{OperationId: "sale:" + j.Operation, CorrelationId: j.Correlation},
			Key: &pb.DocumentKey{Kind: saleKind, Id: j.ID}, DocumentJson: body})
		if err == nil {
			envelope, err = proto.Marshal(prepared)
		}
	case stepApply:
		var prepared pb.Mutation
		if err := proto.Unmarshal(j.Envelope, &prepared); err != nil {
			return err
		}
		err = applyDocument(ctx, p.Replicas, &prepared, j.Correlation)
	case stepActivate:
		_, err = p.Inventory.ActivateSale(ctx, &pb.ActivateSaleRequest{Call: call, SaleId: j.ID})
	default:
		return errors.New("invalid publication step")
	}
	if err != nil {
		return err
	}
	next := j.Step + 1
	state := "PENDING"
	if next == stepPublished {
		state = "PUBLISHED"
	}
	_, err = p.DB.Exec(ctx, `UPDATE flash_sale.drafts SET step=$3,state=$4,
		envelope=coalesce($5::bytea,envelope),next_attempt_at=clock_timestamp()
		WHERE sale_id=$1 AND step=$2 AND state='PENDING'`, j.ID, j.Step, next, state, envelope)
	return err
}
