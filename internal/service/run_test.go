package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"flashkart/internal/replica"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type logChannel chan []byte

func (logs logChannel) Write(p []byte) (int, error) {
	logs <- append([]byte(nil), p...)
	return len(p), nil
}

func startTestGRPC(t *testing.T, store *replica.Store) (context.Context, *grpc.ClientConn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	logs := make(logChannel, 8)
	done := make(chan struct{})
	var serverError error
	go func() {
		defer close(done)
		serverError = serve(ctx, "catalog-replica", config{HTTP: "127.0.0.1:0", GRPC: "127.0.0.1:0"},
			slog.New(slog.NewJSONHandler(logs, nil)), store)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			if serverError != nil {
				t.Errorf("server stopped with %v", serverError)
			}
		case <-time.After(6 * time.Second):
			t.Error("server shutdown timed out")
		}
	})

	var endpoint string
	for endpoint == "" {
		select {
		case line := <-logs:
			var entry struct {
				Message string `json:"msg"`
				Address string `json:"address"`
			}
			if err := json.Unmarshal(line, &entry); err != nil {
				t.Fatal(err)
			}
			if entry.Message == "gRPC listening" {
				endpoint = entry.Address
			}
		case <-done:
			t.Fatalf("server stopped during startup: %v", serverError)
		case <-ctx.Done():
			t.Fatal("gRPC startup timed out")
		}
	}
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return ctx, conn
}

func TestGRPCHealthAndPendingBusinessCall(t *testing.T) {
	ctx, conn := startTestGRPC(t, nil)
	response, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}, grpc.WaitForReady(true))
	if err != nil || response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("gRPC health check = %v, %v", response, err)
	}
	_, err = pb.NewCatalogReplicationClient(conn).ReadDocument(ctx, &pb.ReadDocumentRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("pending RPC returned %v", err)
	}
}

func TestPostgresReplicaGRPC(t *testing.T) {
	if os.Getenv("FLASHKART_TEST_POSTGRES") != "1" {
		t.Skip("run make test-persistence against the local PostgreSQL stack")
	}
	dsn := os.Getenv("FLASHKART_REPLICA_A_DSN")
	if dsn == "" {
		t.Fatal("missing test database DSN")
	}
	store, err := replica.Open(t.Context(), dsn, "a")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	ctx, conn := startTestGRPC(t, store)
	health, err := healthpb.NewHealthClient(conn).Watch(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for {
		update, err := health.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if update.Status == healthpb.HealthCheckResponse_SERVING {
			break
		}
	}

	id := rand.Text()
	key := &pb.DocumentKey{Kind: pb.DocumentKind_DOCUMENT_KIND_PRODUCT, Id: id}
	client := pb.NewCatalogReplicationClient(conn)
	mutation, err := client.PrepareMutation(ctx, &pb.PrepareMutationRequest{
		Call: &pb.Call{OperationId: id},
		Key:  key, DocumentJson: []byte(`{"product_id":"` + id + `","name":"Test"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := client.ReadDocument(ctx, &pb.ReadDocumentRequest{Key: key})
	if err != nil || len(before.GetSiblings()) != 0 {
		t.Fatalf("read before apply: %v %v", before, err)
	}
	if _, err := client.ApplyMutation(ctx, &pb.ApplyMutationRequest{Mutation: mutation}); err != nil {
		t.Fatal(err)
	}
	peer := pb.NewReplicaPeerClient(conn)
	result, err := peer.ReadVersions(ctx, &pb.ReadDocumentRequest{Key: key})
	if err != nil || len(result.GetSiblings()) != 1 || !proto.Equal(result.Siblings[0], mutation) {
		t.Fatalf("read after apply: %v %v", result, err)
	}
}
