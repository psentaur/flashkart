package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"flashkart/internal/replica"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := seed(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Replica A sample product applied.")
}

func seed(ctx context.Context) error {
	dsn := os.Getenv("FLASHKART_REPLICA_A_DSN")
	if dsn == "" {
		return errors.New("FLASHKART_REPLICA_A_DSN is required")
	}
	store, err := replica.Open(ctx, dsn, "a")
	if err != nil {
		return err
	}
	defer store.Close()
	call := &pb.Call{OperationId: "phase-2-seed-product-v1"}
	mutation, err := store.PrepareMutation(ctx, &pb.PrepareMutationRequest{
		Call: call, Key: &pb.DocumentKey{Kind: pb.DocumentKind_DOCUMENT_KIND_PRODUCT, Id: "demo-product"},
		DocumentJson: []byte(`{"product_id":"demo-product","name":"Demo product"}`),
	})
	if err != nil {
		return err
	}
	_, err = store.ApplyMutation(ctx, &pb.ApplyMutationRequest{Call: call, Mutation: mutation})
	return err
}
