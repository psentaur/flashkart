package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type logChannel chan []byte

func (logs logChannel) Write(p []byte) (int, error) {
	logs <- append([]byte(nil), p...)
	return len(p), nil
}

func TestGRPCHealthAndPendingBusinessCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	logs := make(logChannel, 8)
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, "catalog-replica", config{HTTP: "127.0.0.1:0", GRPC: "127.0.0.1:0"},
			slog.New(slog.NewJSONHandler(logs, nil)))
	}()

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
		case err := <-done:
			t.Fatalf("server stopped during startup: %v", err)
		case <-ctx.Done():
			t.Fatal("gRPC startup timed out")
		}
	}
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	response, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}, grpc.WaitForReady(true))
	if err != nil || response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("gRPC health check = %v, %v", response, err)
	}
	err = conn.Invoke(ctx, "/flashkart.v1.CatalogReplication/ReadDocument", &healthpb.HealthCheckRequest{}, &healthpb.HealthCheckResponse{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("pending RPC returned %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("server shutdown timed out")
	}
}
