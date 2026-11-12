package replica

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync/atomic"

	pb "flashkart/gen/flashkart/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type Store struct {
	pb.UnimplementedCatalogReplicationServer
	pb.UnimplementedReplicaPeerServer
	pool        *pgxpool.Pool
	id          string
	sqlFailures atomic.Uint64
}

func Open(ctx context.Context, dsn, id string) (*Store, error) {
	if !replicaID(id) || dsn == "" {
		return nil, errors.New("replica identity and database DSN are required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, errors.New("invalid replica database configuration")
	}
	return &Store{pool: pool, id: id}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ready(ctx context.Context) bool {
	var version int
	return s.pool.QueryRow(ctx, "SELECT max(version) FROM replica.schema_version").Scan(&version) == nil && version == 2
}

func (s *Store) Inspect(ctx context.Context, _ *pb.ReplicaStatusRequest) (*pb.ReplicaStatus, error) {
	result := &pb.ReplicaStatus{ReplicaId: s.id, BackendReady: s.Ready(ctx)}
	if result.BackendReady {
		err := s.pool.QueryRow(ctx, "SELECT count(*) FROM replica.hints").Scan(&result.PendingHints)
		if err != nil {
			return nil, s.storageError(err)
		}
	}
	return result, nil
}

func (s *Store) storageError(err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	var sqlErr *pgconn.PgError
	if errors.As(err, &sqlErr) && sqlErr.Code == "23505" {
		return status.Error(codes.AlreadyExists, "operation ID or version is already in use")
	}
	s.sqlFailures.Add(1)
	return status.Error(codes.Unavailable, "replica database operation failed")
}

func validKey(key *pb.DocumentKey) bool {
	return key.GetId() != "" && !strings.ContainsRune(key.GetId(), 0) &&
		(key.GetKind() == pb.DocumentKind_DOCUMENT_KIND_PRODUCT || key.GetKind() == pb.DocumentKind_DOCUMENT_KIND_PUBLISHED_SALE)
}

func validDocument(key *pb.DocumentKey, body []byte) bool {
	var doc map[string]json.RawMessage
	if json.Unmarshal(body, &doc) != nil || doc == nil {
		return false
	}
	field := "product_id"
	if key.GetKind() == pb.DocumentKind_DOCUMENT_KIND_PUBLISHED_SALE {
		field = "sale_id"
	}
	var id string
	return json.Unmarshal(doc[field], &id) == nil && id == key.GetId()
}

func lockDocument(ctx context.Context, tx pgx.Tx, key *pb.DocumentKey) (int64, error) {
	_, err := tx.Exec(ctx, "INSERT INTO replica.documents(kind, document_id) VALUES ($1,$2) ON CONFLICT DO NOTHING", int32(key.Kind), key.Id)
	if err != nil {
		return 0, err
	}
	var counter int64
	err = tx.QueryRow(ctx, "SELECT counter FROM replica.documents WHERE kind=$1 AND document_id=$2 FOR UPDATE", int32(key.Kind), key.Id).Scan(&counter)
	return counter, err
}

func lockOperation(ctx context.Context, tx pgx.Tx, operationID string) error {
	_, err := tx.Exec(ctx, "SELECT set_config('synchronous_commit','on',true), pg_advisory_xact_lock(hashtextextended($1, 0))", operationID)
	return err
}

func (s *Store) PrepareMutation(ctx context.Context, req *pb.PrepareMutationRequest) (*pb.Mutation, error) {
	key, op := req.GetKey(), req.GetCall().GetOperationId()
	if !validKey(key) || op == "" || strings.ContainsRune(op, 0) || !validContext(req.GetContext()) || !validDocument(key, req.GetDocumentJson()) {
		return nil, status.Error(codes.InvalidArgument, "invalid document, operation ID or causal context")
	}
	request := proto.CloneOf(req)
	request.Call = &pb.Call{OperationId: op}
	input, err := proto.MarshalOptions{Deterministic: true}.Marshal(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid mutation request encoding")
	}

	var result pb.Mutation
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockOperation(ctx, tx, op); err != nil {
			return err
		}
		counter, err := lockDocument(ctx, tx, key)
		if err != nil {
			return err
		}
		var recorded, envelope []byte
		err = tx.QueryRow(ctx, "SELECT request, envelope FROM replica.prepared WHERE operation_id=$1", op).Scan(&recorded, &envelope)
		if err == nil {
			if string(input) != string(recorded) {
				return status.Error(codes.AlreadyExists, "operation ID belongs to a different preparation")
			}
			return proto.Unmarshal(envelope, &result)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var applied bool
		err = tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM replica.versions WHERE operation_id=$1)", op).Scan(&applied)
		if err != nil {
			return err
		}
		if applied {
			return status.Error(codes.AlreadyExists, "operation was already applied")
		}
		for _, span := range req.GetContext().GetObserved()[s.id].GetIntervals() {
			counter = max(counter, int64(span.Last))
		}
		if counter == math.MaxInt64 {
			return status.Error(codes.ResourceExhausted, "replica counter exhausted")
		}
		counter++
		result = pb.Mutation{OperationId: op, Key: key, Version: &pb.Dot{ReplicaId: s.id, Counter: uint64(counter)},
			Context: req.GetContext(), DocumentJson: req.GetDocumentJson()}
		envelope, err = proto.Marshal(&result)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE replica.documents SET counter=$3 WHERE kind=$1 AND document_id=$2", int32(key.Kind), key.Id, counter); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "INSERT INTO replica.prepared VALUES ($1,$2,$3)", op, input, envelope)
		return err
	})
	if err != nil {
		return nil, s.storageError(err)
	}
	return &result, nil
}

func (s *Store) ApplyMutation(ctx context.Context, req *pb.ApplyMutationRequest) (*pb.Ack, error) {
	return s.merge(ctx, req.GetMutation(), nil)
}

func (s *Store) MergeVersion(ctx context.Context, req *pb.MergeRequest) (*pb.Ack, error) {
	return s.merge(ctx, req.GetMutation(), req.GetHintTargets())
}

func (s *Store) DeliverHint(ctx context.Context, req *pb.ApplyMutationRequest) (*pb.Ack, error) {
	return s.merge(ctx, req.GetMutation(), nil)
}

func validMutation(mutation *pb.Mutation) bool {
	key, dot := mutation.GetKey(), mutation.GetVersion()
	return validKey(key) && replicaID(dot.GetReplicaId()) && dot.GetCounter() > 0 && dot.GetCounter() <= math.MaxInt64 &&
		mutation.GetOperationId() != "" && !strings.ContainsRune(mutation.GetOperationId(), 0) &&
		validContext(mutation.GetContext()) && !observes(mutation.GetContext(), dot) && validDocument(key, mutation.GetDocumentJson())
}

func (s *Store) merge(ctx context.Context, mutation *pb.Mutation, targets []string) (*pb.Ack, error) {
	if !validMutation(mutation) {
		return nil, status.Error(codes.InvalidArgument, "invalid mutation envelope")
	}
	key, dot := mutation.Key, mutation.Version
	for _, target := range targets {
		if !replicaID(target) || target == s.id {
			return nil, status.Error(codes.InvalidArgument, "invalid hint target")
		}
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockOperation(ctx, tx, mutation.OperationId); err != nil {
			return err
		}
		if _, err := lockDocument(ctx, tx, key); err != nil {
			return err
		}
		args := []any{int32(key.Kind), key.Id, dot.ReplicaId, int64(dot.Counter)}
		var recorded []byte
		err := tx.QueryRow(ctx, "SELECT envelope FROM replica.versions WHERE kind=$1 AND document_id=$2 AND origin=$3 AND counter=$4", args...).Scan(&recorded)
		switch {
		case err == nil:
			var existing pb.Mutation
			if err := proto.Unmarshal(recorded, &existing); err != nil {
				return err
			}
			if !proto.Equal(&existing, mutation) {
				return status.Error(codes.AlreadyExists, "version belongs to a different envelope")
			}
		case errors.Is(err, pgx.ErrNoRows):
			var prepared []byte
			err := tx.QueryRow(ctx, "SELECT envelope FROM replica.prepared WHERE operation_id=$1", mutation.OperationId).Scan(&prepared)
			if err == nil {
				var existing pb.Mutation
				if err := proto.Unmarshal(prepared, &existing); err != nil {
					return err
				}
				if !proto.Equal(&existing, mutation) {
					return status.Error(codes.AlreadyExists, "operation ID belongs to a different preparation")
				}
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			siblings, err := readVersions(ctx, tx, key)
			if err != nil {
				return err
			}
			visible := true
			for _, sibling := range siblings {
				if observes(sibling.Context, dot) {
					visible = false
					break
				}
			}
			if visible {
				for _, sibling := range siblings {
					if observes(mutation.Context, sibling.Version) {
						_, err := tx.Exec(ctx, "UPDATE replica.versions SET visible=false WHERE kind=$1 AND document_id=$2 AND origin=$3 AND counter=$4",
							int32(key.Kind), key.Id, sibling.Version.ReplicaId, int64(sibling.Version.Counter))
						if err != nil {
							return err
						}
					}
				}
			}
			encoded, err := proto.Marshal(mutation)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, "INSERT INTO replica.versions(kind,document_id,origin,counter,operation_id,envelope,visible) VALUES ($1,$2,$3,$4,$5,$6,$7)",
				args[0], args[1], args[2], args[3], mutation.OperationId, encoded, visible)
			if err != nil {
				return err
			}
			if dot.ReplicaId == s.id {
				_, err = tx.Exec(ctx, "UPDATE replica.documents SET counter=greatest(counter,$3) WHERE kind=$1 AND document_id=$2", args[0], args[1], args[3])
				if err != nil {
					return err
				}
			}
		default:
			return err
		}
		for _, target := range targets {
			_, err := tx.Exec(ctx, "INSERT INTO replica.hints VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING", args[0], args[1], args[2], args[3], target)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, s.storageError(err)
	}
	return &pb.Ack{}, nil
}

type querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func readVersions(ctx context.Context, db querier, key *pb.DocumentKey) ([]*pb.Mutation, error) {
	rows, err := db.Query(ctx, `SELECT envelope FROM replica.versions WHERE kind=$1 AND document_id COLLATE "C"=$2 AND visible ORDER BY origin,counter`, int32(key.Kind), key.Id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (*pb.Mutation, error) {
		var envelope []byte
		if err := row.Scan(&envelope); err != nil {
			return nil, err
		}
		version := new(pb.Mutation)
		return version, proto.Unmarshal(envelope, version)
	})
}

func (s *Store) ReadVersions(ctx context.Context, req *pb.ReadDocumentRequest) (*pb.DocumentVersions, error) {
	key := req.GetKey()
	if !validKey(key) {
		return nil, status.Error(codes.InvalidArgument, "invalid document key")
	}
	versions, err := readVersions(ctx, s.pool, key)
	if err != nil {
		return nil, s.storageError(err)
	}
	return &pb.DocumentVersions{Siblings: versions, Context: combined(versions)}, nil
}

func (s *Store) ReadDocument(ctx context.Context, req *pb.ReadDocumentRequest) (*pb.DocumentVersions, error) {
	return s.ReadVersions(ctx, req)
}

func documentPageLimit(req *pb.ListDocumentsRequest) (int, error) {
	if !validKey(&pb.DocumentKey{Kind: req.GetKind(), Id: "list"}) || req.GetLimit() > 100 || strings.ContainsRune(req.GetCursor(), 0) {
		return 0, status.Error(codes.InvalidArgument, "invalid document page request")
	}
	limit := int(req.GetLimit())
	if limit == 0 {
		limit = 50
	}
	return limit, nil
}

func (s *Store) ListDocuments(ctx context.Context, req *pb.ListDocumentsRequest) (*pb.DocumentPage, error) {
	return s.ListLocalDocuments(ctx, req)
}

func (s *Store) ListLocalDocuments(ctx context.Context, req *pb.ListDocumentsRequest) (*pb.DocumentPage, error) {
	limit, err := documentPageLimit(req)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT document_id COLLATE "C" AS id FROM replica.versions WHERE kind=$1 AND visible AND document_id COLLATE "C">$2 ORDER BY id LIMIT $3`,
		int32(req.Kind), req.Cursor, limit+1)
	if err != nil {
		return nil, s.storageError(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, s.storageError(err)
	}
	page := &pb.DocumentPage{}
	if len(ids) > limit {
		ids = ids[:limit]
		page.NextCursor = ids[limit-1]
	}
	for _, id := range ids {
		page.Keys = append(page.Keys, &pb.DocumentKey{Kind: req.Kind, Id: id})
	}
	return page, nil
}
