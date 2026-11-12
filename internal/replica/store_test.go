package replica

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestContextKeepsUnobservedGaps(t *testing.T) {
	v1 := &pb.Mutation{Version: &pb.Dot{ReplicaId: "a", Counter: 1}}
	v2 := &pb.Mutation{Version: &pb.Dot{ReplicaId: "a", Counter: 2}}
	v3 := &pb.Mutation{Version: &pb.Dot{ReplicaId: "a", Counter: 3}, Context: combined([]*pb.Mutation{v2})}
	ctx := combined([]*pb.Mutation{v3, v3})
	if observes(ctx, v1.Version) || !observes(ctx, v2.Version) || !observes(ctx, v3.Version) {
		t.Fatalf("unexpected context: %v", ctx)
	}
	span := ctx.Observed["a"].Intervals
	if len(span) != 1 || span[0].First != 2 || span[0].Last != 3 {
		t.Fatalf("unexpected interval union: %v", span)
	}
}

func TestPostgresReplica(t *testing.T) {
	if os.Getenv("FLASHKART_TEST_POSTGRES") != "1" {
		t.Skip("run make test-persistence against the local PostgreSQL stack")
	}
	dsn := os.Getenv("FLASHKART_REPLICA_A_DSN")
	if dsn == "" {
		t.Fatal("missing test database DSN")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := Open(ctx, dsn, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if !store.Ready(ctx) {
		t.Fatal("replica database is unavailable or has the wrong schema")
	}
	prefix := rand.Text()
	key := &pb.DocumentKey{Kind: pb.DocumentKind_DOCUMENT_KIND_PRODUCT, Id: prefix + "-main"}
	read := func(s *Store, key *pb.DocumentKey) *pb.DocumentVersions {
		t.Helper()
		result, err := s.ReadDocument(ctx, &pb.ReadDocumentRequest{Key: key})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	input := func(key *pb.DocumentKey, suffix string) *pb.PrepareMutationRequest {
		return &pb.PrepareMutationRequest{Call: &pb.Call{OperationId: prefix + "-" + suffix}, Key: key,
			DocumentJson: []byte(fmt.Sprintf(`{"product_id":%q,"name":%q}`, key.Id, suffix))}
	}
	prepare := func(s *Store, request *pb.PrepareMutationRequest) *pb.Mutation {
		t.Helper()
		result, err := s.PrepareMutation(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	apply := func(v *pb.Mutation) {
		t.Helper()
		if _, err := store.ApplyMutation(ctx, &pb.ApplyMutationRequest{Mutation: v}); err != nil {
			t.Fatal(err)
		}
	}
	firstRequest := input(key, "first")
	first := prepare(store, firstRequest)
	if len(read(store, key).Siblings) != 0 {
		t.Fatal("prepared mutation became visible")
	}
	other, err := Open(ctx, dsn, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	firstRequest.Call.CorrelationId = "retry-trace"
	if !proto.Equal(first, prepare(other, firstRequest)) {
		t.Fatal("reopened store allocated a new version on retry")
	}
	bad := proto.CloneOf(firstRequest)
	bad.DocumentJson = []byte(fmt.Sprintf(`{"product_id":%q,"name":"different"}`, key.Id))
	if _, err := store.PrepareMutation(ctx, bad); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("conflicting preparation: %v", err)
	}
	second := prepare(store, input(key, "second"))
	if second.Version.Counter <= first.Version.Counter {
		t.Fatal("allocator did not advance")
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, mutation := range []*pb.Mutation{first, second} {
		wg.Go(func() {
			_, err := store.ApplyMutation(ctx, &pb.ApplyMutationRequest{Mutation: mutation})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	apply(first)
	visible := read(store, key)
	if len(visible.Siblings) != 2 {
		t.Fatalf("concurrent stale writes did not survive: %v", visible.Siblings)
	}

	otherKey := &pb.DocumentKey{Kind: key.Kind, Id: prefix + "-other"}
	foreign := &pb.Mutation{OperationId: prefix + "-remote", Key: otherKey,
		Version: &pb.Dot{ReplicaId: "b", Counter: 1}, DocumentJson: input(otherKey, "other").DocumentJson}
	apply(foreign)
	conflict := proto.CloneOf(foreign)
	conflict.Key = key
	conflict.Context = visible.Context
	conflict.DocumentJson = input(key, "rollback").DocumentJson
	ack, err := store.ApplyMutation(ctx, &pb.ApplyMutationRequest{Mutation: conflict})
	if ack != nil || status.Code(err) != codes.AlreadyExists || len(read(store, key).Siblings) != 2 {
		t.Fatalf("failed transaction altered visible versions or acknowledged: %v %v", ack, err)
	}

	if _, err := store.MergeVersion(ctx, &pb.MergeRequest{Mutation: first, HintTargets: []string{"b"}}); err != nil {
		t.Fatal(err)
	}
	resolve := input(key, "resolve")
	resolve.Context = visible.Context
	resolved := prepare(store, resolve)
	apply(resolved)
	apply(first)
	final := read(other, key)
	if len(final.Siblings) != 1 || !proto.Equal(final.Siblings[0], resolved) {
		t.Fatalf("expected only resolved version, got %v", final.Siblings)
	}
	var hints int
	if err := other.pool.QueryRow(ctx, "SELECT count(*) FROM replica.hints WHERE document_id=$1", key.Id).Scan(&hints); err != nil || hints != 1 {
		t.Fatalf("superseded hinted version was discarded: %d %v", hints, err)
	}
	page, err := store.ListDocuments(ctx, &pb.ListDocumentsRequest{Kind: key.Kind, Limit: 1, Cursor: prefix})
	if err != nil || len(page.GetKeys()) != 1 || page.GetKeys()[0].Id != key.Id || page.GetNextCursor() != key.Id {
		t.Fatalf("unexpected page: %v %v", page, err)
	}
	other.Close()
	if ack, err := other.ApplyMutation(ctx, &pb.ApplyMutationRequest{Mutation: first}); ack != nil || status.Code(err) != codes.Unavailable {
		t.Fatalf("closed database acknowledged write: %v %v", ack, err)
	}
}
