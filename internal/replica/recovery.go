package replica

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"google.golang.org/grpc"
)

func fallbackReplica(owners []string) string {
	for _, id := range replicaIDs {
		if !slices.Contains(owners, id) {
			return id
		}
	}
	return ""
}

func (c *Coordinator) mergeAt(ctx context.Context, id string, req *pb.MergeRequest) error {
	if id == c.store.id {
		_, err := c.store.MergeVersion(ctx, req)
		return err
	}
	_, err := pb.NewReplicaPeerClient(c.client.connections[id]).MergeVersion(ctx, req, grpc.WaitForReady(true))
	return err
}

// RunRecovery owns the periodic workers until the service shuts down.
func (c *Coordinator) RunRecovery(ctx context.Context) {
	var workers sync.WaitGroup
	for _, mode := range []struct {
		interval time.Duration
		hints    bool
	}{{time.Second, true}, {10 * time.Second, false}} {
		workers.Go(func() {
			ticker := time.NewTicker(mode.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					c.recoverPass(ctx, mode.hints)
				}
			}
		})
	}
	workers.Wait()
}

func (c *Coordinator) recoverPass(ctx context.Context, hintsOnly bool) {
	for cursor := ""; ctx.Err() == nil; {
		scanCtx, cancel := context.WithTimeout(ctx, c.timeout)
		page, err := c.store.ScanVersions(scanCtx, &pb.SyncRequest{Cursor: cursor, Limit: 50, HintsOnly: hintsOnly})
		cancel()
		if err != nil {
			return
		}
		for _, record := range page.Records {
			if ctx.Err() != nil {
				return
			}
			if hintsOnly {
				c.deliverRecord(ctx, record)
			} else {
				c.syncRecord(ctx, record)
			}
		}
		cursor = page.NextCursor
		if cursor == "" {
			if !hintsOnly {
				c.store.lastSync.Store(time.Now().Unix())
			}
			return
		}
	}
}

func (c *Coordinator) deliverRecord(ctx context.Context, record *pb.ReplicaRecord) {
	fanout(ctx, c, "handoff", record.HintTargets, func(ctx context.Context, id string) (struct{}, error) {
		_, err := pb.NewReplicaPeerClient(c.client.connections[id]).DeliverHint(ctx,
			&pb.ApplyMutationRequest{Mutation: record.Mutation}, grpc.WaitForReady(true))
		if err != nil {
			return struct{}{}, err
		}
		if err := c.store.deleteHint(ctx, record.Mutation, id); err != nil {
			return struct{}{}, err
		}
		c.store.handoffs.Add(1)
		return struct{}{}, nil
	})
}

func (c *Coordinator) syncRecord(ctx context.Context, record *pb.ReplicaRecord) {
	c.store.syncRecords.Add(1)
	owners := PreferredOwners(record.Mutation.Key)
	if !slices.Contains(owners, c.store.id) && len(record.HintTargets) == 0 {
		return
	}
	targets := slices.DeleteFunc(owners, func(id string) bool { return id == c.store.id })
	fanout(ctx, c, "sync", targets, func(ctx context.Context, id string) (struct{}, error) {
		return struct{}{}, c.mergeAt(ctx, id, &pb.MergeRequest{Mutation: record.Mutation})
	})
}

func (c *Coordinator) writeRecoveryMetrics(w io.Writer) {
	fmt.Fprintf(w, "# TYPE flashkart_replica_handoffs_total counter\nflashkart_replica_handoffs_total %d\n", c.store.handoffs.Load())
	fmt.Fprintf(w, "# TYPE flashkart_replica_sync_records_total counter\nflashkart_replica_sync_records_total %d\n", c.store.syncRecords.Load())
	fmt.Fprintf(w, "# TYPE flashkart_replica_last_sync_completed_timestamp_seconds gauge\nflashkart_replica_last_sync_completed_timestamp_seconds %d\n", c.store.lastSync.Load())
}
