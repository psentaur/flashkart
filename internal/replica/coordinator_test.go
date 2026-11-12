package replica

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestPreferredOwners(t *testing.T) {
	firstOwners := map[string]bool{}
	for i := range 100 {
		key := &pb.DocumentKey{Kind: pb.DocumentKind_DOCUMENT_KIND_PRODUCT, Id: fmt.Sprint(i)}
		owners := PreferredOwners(key)
		if len(owners) != 3 || owners[0] == owners[1] || owners[0] == owners[2] || owners[1] == owners[2] ||
			!slices.Equal(owners, PreferredOwners(proto.CloneOf(key))) {
			t.Fatalf("invalid placement for %v: %v", key, owners)
		}
		firstOwners[owners[0]] = true
	}
	if len(firstOwners) != 4 {
		t.Fatalf("keys did not distribute across all four owners: %v", firstOwners)
	}
}

func TestReadReconciliation(t *testing.T) {
	old := &pb.Mutation{Version: &pb.Dot{ReplicaId: "a", Counter: 1}}
	unseen := &pb.Mutation{Version: &pb.Dot{ReplicaId: "a", Counter: 2}}
	newer := &pb.Mutation{Version: &pb.Dot{ReplicaId: "b", Counter: 1}, Context: combined([]*pb.Mutation{old})}
	result, err := maximalVersions([]*pb.DocumentVersions{
		{Siblings: []*pb.Mutation{old, unseen}}, {Siblings: []*pb.Mutation{old, newer}},
	})
	if err != nil || len(result) != 2 || result[0] != unseen || result[1] != newer {
		t.Fatalf("expected unseen sibling and causal successor: %v %v", result, err)
	}
	collision := proto.CloneOf(old)
	collision.OperationId = "different"
	_, err = maximalVersions([]*pb.DocumentVersions{{Siblings: []*pb.Mutation{old, collision}}})
	if status.Code(err) != codes.DataLoss {
		t.Fatalf("conflicting envelopes for one dot returned %v", err)
	}
}

func TestReplicationCore(t *testing.T) {
	if os.Getenv("FLASHKART_TEST_CLUSTER") != "1" {
		t.Skip("run make test-replication-core against four PostgreSQL instances")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	addresses := map[string]string{}
	listeners := map[string]net.Listener{}
	stores := map[string]*Store{}
	servers := map[string]*grpc.Server{}
	coordinators := map[string]*Coordinator{}
	for _, id := range replicaIDs {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
		addresses[id], listeners[id] = listener.Addr().String(), listener
		store, err := Open(ctx, os.Getenv("FLASHKART_REPLICA_"+strings.ToUpper(id)+"_DSN"), id)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(store.Close)
		if !store.Ready(ctx) {
			t.Fatalf("replica %s database is unavailable", id)
		}
		stores[id] = store
	}
	client, err := NewClient(addresses, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	for _, id := range replicaIDs {
		coordinator := NewCoordinator(stores[id], client, 500*time.Millisecond, time.Second)
		server := grpc.NewServer()
		pb.RegisterCatalogReplicationServer(server, coordinator)
		pb.RegisterReplicaPeerServer(server, stores[id])
		servers[id], coordinators[id] = server, coordinator
		t.Cleanup(server.Stop)
		go server.Serve(listeners[id])
	}

	key := &pb.DocumentKey{Kind: pb.DocumentKind_DOCUMENT_KIND_PRODUCT, Id: rand.Text()}
	owners := PreferredOwners(key)
	outside := ""
	for _, id := range replicaIDs {
		if !slices.Contains(owners, id) {
			outside = id
		}
	}
	newRequest := func(label string, observed *pb.Context) *pb.PrepareMutationRequest {
		return &pb.PrepareMutationRequest{Key: key, Context: observed,
			Call:         &pb.Call{OperationId: key.Id + "-" + label},
			DocumentJson: []byte(fmt.Sprintf(`{"product_id":%q,"name":%q}`, key.Id, label))}
	}
	prepare := func(origin string, req *pb.PrepareMutationRequest) *pb.Mutation {
		t.Helper()
		m, err := client.Prepare(ctx, origin, req)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	apply := func(coordinator string, m *pb.Mutation) {
		t.Helper()
		if _, err := client.Apply(ctx, coordinator, &pb.ApplyMutationRequest{Mutation: m}); err != nil {
			t.Fatal(err)
		}
	}
	readRequest := &pb.ReadDocumentRequest{Key: key}
	read := func(coordinator string) *pb.DocumentVersions {
		t.Helper()
		out, err := client.Read(ctx, coordinator, readRequest)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	physical := func(id string) []*pb.Mutation {
		t.Helper()
		out, err := stores[id].ReadVersions(ctx, readRequest)
		if err != nil {
			t.Fatal(err)
		}
		return out.Siblings
	}

	req := newRequest("first", nil)
	first := prepare(outside, req)
	if !proto.Equal(first, prepare(outside, req)) || len(read(owners[0]).Siblings) != 0 {
		t.Fatal("prepare retry changed the envelope or exposed an uncommitted document")
	}
	apply(outside, first)
	for _, id := range owners {
		copies := physical(id)
		if len(copies) != 1 || !proto.Equal(copies[0], first) {
			t.Fatalf("owner %s did not durably store the version", id)
		}
	}
	if len(physical(outside)) != 0 {
		t.Fatal("coordinator outside the owner set stored a visible version")
	}
	if versions := read(owners[1]); len(versions.Siblings) != 1 || !proto.Equal(versions.Siblings[0], first) {
		t.Fatalf("cross-coordinator read: %v", versions)
	}

	stale := prepare(outside, newRequest("stale", nil))
	cross := prepare(owners[0], newRequest("cross", nil))
	apply(owners[2], stale)
	apply(owners[1], cross)
	siblings := read(owners[0])
	if len(siblings.Siblings) != 3 {
		t.Fatalf("expected three siblings, got %v", siblings)
	}
	resolved := prepare(outside, newRequest("resolved", siblings.Context))
	apply(owners[2], resolved)
	if versions := read(outside); len(versions.Siblings) != 1 || !proto.Equal(versions.Siblings[0], resolved) {
		t.Fatalf("resolution did not converge: %v", versions)
	}

	info, err := client.Inspect(ctx, owners[1], &pb.ReplicaStatusRequest{})
	if err != nil || !info.GetBackendReady() || info.GetReplicaId() != owners[1] {
		t.Fatalf("incorrect diagnostics: %v %v", info, err)
	}
	page, err := client.List(ctx, outside, &pb.ListDocumentsRequest{Kind: key.Kind, Limit: 100, Cursor: key.Id[:len(key.Id)-1]})
	if err != nil || !slices.ContainsFunc(page.GetKeys(), func(found *pb.DocumentKey) bool { return found.Id == key.Id }) {
		t.Fatalf("discovery omitted the document: %v %v", page, err)
	}

	servers[owners[0]].Stop()
	oneDown := prepare(outside, newRequest("one-down", read(outside).Context))
	apply(outside, oneDown)
	if versions := read(owners[2]); len(versions.Siblings) != 1 || !proto.Equal(versions.Siblings[0], oneDown) {
		t.Fatalf("quorum read with one owner down: %v", versions)
	}
	if _, err := client.Prepare(ctx, owners[0], newRequest("origin-down", nil)); status.Code(err) != codes.Unavailable {
		t.Fatalf("unreachable preparation origin returned %v", err)
	}

	servers[owners[1]].Stop()
	partial := prepare(outside, newRequest("partial", combined([]*pb.Mutation{oneDown})))
	ack, err := client.Apply(ctx, outside, &pb.ApplyMutationRequest{Mutation: partial})
	if ack != nil {
		t.Fatal("partial write returned an acknowledgement")
	}
	requireReason(t, err, WriteOutcomeUnknown)
	copies := physical(owners[2])
	if len(copies) != 1 || !proto.Equal(copies[0], partial) || len(physical(outside)) != 0 {
		t.Fatal("partial write was lost or placed on the fallback")
	}
	_, err = client.Read(ctx, outside, readRequest)
	requireReason(t, err, QuorumUnavailable)

	var metrics bytes.Buffer
	coordinators[outside].WriteMetrics(&metrics)
	for _, want := range []string{
		`flashkart_replica_quorum_failures_total{operation="write"} 1`,
		`flashkart_replica_quorum_failures_total{operation="read"} 1`,
		`flashkart_replica_peer_requests_total{operation="write",replica="` + owners[0] + `",result="error"} 2`,
	} {
		if !strings.Contains(metrics.String(), want) {
			t.Errorf("missing metric %s", want)
		}
	}
	t.Logf("owners %v; non-owner %s coordinated writes; one outage passed, two produced an unknown write outcome with one durable copy", owners, outside)
}

func requireReason(t *testing.T, err error, reason string) {
	t.Helper()
	s := status.Convert(err)
	if s.Code() == codes.Unavailable {
		for _, detail := range s.Details() {
			if info, ok := detail.(*errdetails.ErrorInfo); ok && info.Reason == reason {
				return
			}
		}
	}
	t.Fatalf("expected %s with UNAVAILABLE, got %v", reason, err)
}
