package market

import (
	"context"
	"crypto/sha256"

	pb "flashkart/gen/flashkart/v1"
	"flashkart/internal/replica"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const replicaOrder = "abcd"

func originFor(operation string) string {
	sum := sha256.Sum256([]byte(operation))
	return string(replicaOrder[int(sum[0])%len(replicaOrder)])
}

func across[T any](ctx context.Context, first string, call func(string) (T, error)) (T, error) {
	var result T
	var last error
	for i := range len(replicaOrder) {
		id := string(replicaOrder[(int(first[0]-'a')+i)%len(replicaOrder)])
		result, last = call(id)
		code := status.Code(last)
		if ctx.Err() != nil || code != codes.Unavailable && code != codes.DeadlineExceeded {
			return result, last
		}
	}
	return result, last
}

func readDocument(ctx context.Context, client *replica.Client, kind pb.DocumentKind, id, correlation string) (*pb.DocumentVersions, error) {
	req := &pb.ReadDocumentRequest{Key: &pb.DocumentKey{Kind: kind, Id: id}, Call: &pb.Call{CorrelationId: correlation}}
	return across(ctx, originFor(id), func(coordinator string) (*pb.DocumentVersions, error) {
		return client.Read(ctx, coordinator, req)
	})
}

func applyDocument(ctx context.Context, client *replica.Client, envelope *pb.Mutation, correlation string) error {
	_, err := across(ctx, envelope.Version.ReplicaId, func(coordinator string) (*pb.Ack, error) {
		return client.Apply(ctx, coordinator, &pb.ApplyMutationRequest{
			Call: &pb.Call{OperationId: envelope.OperationId, CorrelationId: correlation}, Mutation: envelope,
		})
	})
	return err
}
