package replica

import (
	"context"
	"encoding/base64"
	"encoding/json"

	pb "flashkart/gen/flashkart/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const hasHints = `EXISTS (SELECT 1 FROM replica.hints h WHERE
    (h.kind,h.document_id,h.origin,h.counter) = (v.kind,v.document_id,v.origin,v.counter))`

func (s *Store) readHintedVersions(ctx context.Context, key *pb.DocumentKey) ([]*pb.Mutation, error) {
	rows, err := s.pool.Query(ctx, `SELECT v.envelope FROM replica.versions v
        WHERE v.kind=$1 AND v.document_id COLLATE "C"=$2 AND `+hasHints,
		int32(key.Kind), key.Id)
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

type scanCursor struct {
	Kind    int32
	ID      string
	Origin  string
	Counter int64
}

func (s *Store) ScanVersions(ctx context.Context, req *pb.SyncRequest) (*pb.SyncPage, error) {
	if req.GetLimit() > 100 {
		return nil, status.Error(codes.InvalidArgument, "invalid synchronization page size")
	}
	limit := int(req.GetLimit())
	if limit == 0 {
		limit = 50
	}
	var cursor scanCursor
	if encoded := req.GetCursor(); encoded != "" {
		data, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || json.Unmarshal(data, &cursor) != nil ||
			!validKey(&pb.DocumentKey{Kind: pb.DocumentKind(cursor.Kind), Id: cursor.ID}) ||
			!replicaID(cursor.Origin) || cursor.Counter <= 0 {
			return nil, status.Error(codes.InvalidArgument, "invalid synchronization cursor")
		}
	}
	predicate := "(v.visible OR " + hasHints + ")"
	if req.GetHintsOnly() {
		predicate = hasHints
	}
	rows, err := s.pool.Query(ctx, `
		SELECT v.envelope, ARRAY(SELECT h.target FROM replica.hints h
		    WHERE (h.kind,h.document_id,h.origin,h.counter) = (v.kind,v.document_id,v.origin,v.counter)
		    ORDER BY h.target)
		FROM replica.versions v
		WHERE (v.kind,v.document_id,v.origin,v.counter) > ($1,$2,$3,$4)
		  AND `+predicate+`
		ORDER BY v.kind,v.document_id,v.origin,v.counter LIMIT $5`,
		cursor.Kind, cursor.ID, cursor.Origin, cursor.Counter, limit+1)
	if err != nil {
		return nil, s.storageError(err)
	}
	defer rows.Close()
	page := &pb.SyncPage{}
	totalBytes := 0
	more := false
	for rows.Next() {
		record := &pb.ReplicaRecord{Mutation: new(pb.Mutation)}
		var envelope []byte
		if err := rows.Scan(&envelope, &record.HintTargets); err != nil {
			return nil, s.storageError(err)
		}
		// Leave room for protobuf framing and ensure one large record can advance the cursor.
		if len(page.Records) == limit || len(page.Records) > 0 && totalBytes+len(envelope) > 3*1024*1024 {
			more = true
			break
		}
		if err := proto.Unmarshal(envelope, record.Mutation); err != nil {
			return nil, s.storageError(err)
		}
		page.Records = append(page.Records, record)
		totalBytes += len(envelope)
	}
	if err := rows.Err(); err != nil {
		return nil, s.storageError(err)
	}
	if more {
		last := page.Records[len(page.Records)-1].Mutation
		next := scanCursor{int32(last.Key.Kind), last.Key.Id, last.Version.ReplicaId, int64(last.Version.Counter)}
		data, err := json.Marshal(next)
		if err != nil {
			return nil, status.Error(codes.Internal, "encode synchronization cursor")
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return page, nil
}

func (s *Store) deleteHint(ctx context.Context, version *pb.Mutation, target string) error {
	key, dot := version.GetKey(), version.GetVersion()
	_, err := s.pool.Exec(ctx, "DELETE FROM replica.hints WHERE kind=$1 AND document_id=$2 AND origin=$3 AND counter=$4 AND target=$5",
		int32(key.Kind), key.Id, dot.GetReplicaId(), int64(dot.GetCounter()), target)
	if err != nil {
		return s.storageError(err)
	}
	return nil
}
