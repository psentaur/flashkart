package replica

import (
	"context"
	"io"
	"slices"
	"strings"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	QuorumUnavailable   = "QUORUM_UNAVAILABLE"
	WriteOutcomeUnknown = "WRITE_OUTCOME_UNKNOWN"
)

type Coordinator struct {
	pb.UnimplementedCatalogReplicationServer
	store       *Store
	client      *Client
	peerTimeout time.Duration
	timeout     time.Duration
	metrics     coordinatorMetrics
}

func NewCoordinator(store *Store, client *Client, peerTimeout, timeout time.Duration) *Coordinator {
	return &Coordinator{store: store, client: client, peerTimeout: peerTimeout, timeout: timeout}
}

func (c *Coordinator) PrepareMutation(ctx context.Context, req *pb.PrepareMutationRequest) (*pb.Mutation, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.store.PrepareMutation(ctx, req)
}

func quorumError(reason, message string) error {
	base := status.New(codes.Unavailable, message)
	detailed, err := base.WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: "flashkart.replication"})
	if err != nil {
		return base.Err()
	}
	return detailed.Err()
}

type result[T any] struct {
	value T
	err   error
}

func fanout[T any](ctx context.Context, c *Coordinator, operation string, ids []string,
	call func(context.Context, string) (T, error)) []T {
	responses := make(chan result[T], len(ids))
	for _, id := range ids {
		go func() {
			peerContext, cancel := context.WithTimeout(ctx, c.peerTimeout)
			defer cancel()
			value, err := call(peerContext, id)
			c.metrics.recordPeer(operation, id, err == nil)
			responses <- result[T]{value, err}
		}()
	}
	values := make([]T, 0, len(ids))
	for range ids {
		select {
		case response := <-responses:
			if response.err == nil {
				values = append(values, response.value)
			}
		case <-ctx.Done():
			return values
		}
	}
	return values
}

func (c *Coordinator) ApplyMutation(ctx context.Context, req *pb.ApplyMutationRequest) (*pb.Ack, error) {
	start := time.Now()
	defer func() { c.metrics.recordLatency("write", time.Since(start)) }()
	mutation := req.GetMutation()
	if !validMutation(mutation) {
		return nil, status.Error(codes.InvalidArgument, "invalid mutation envelope")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	request := &pb.MergeRequest{Call: req.GetCall(), Mutation: mutation}
	acks := fanout(ctx, c, "write", PreferredOwners(mutation.Key), func(ctx context.Context, id string) (*pb.Ack, error) {
		if id == c.store.id {
			return c.store.MergeVersion(ctx, request)
		}
		return pb.NewReplicaPeerClient(c.client.connections[id]).MergeVersion(ctx, request, grpc.WaitForReady(true))
	})
	if len(acks) < writeQuorum {
		c.metrics.recordQuorumFailure("write")
		return nil, quorumError(WriteOutcomeUnknown, "write quorum was not confirmed; retry the same prepared envelope")
	}
	return &pb.Ack{}, nil
}

func (c *Coordinator) ReadDocument(ctx context.Context, req *pb.ReadDocumentRequest) (*pb.DocumentVersions, error) {
	start := time.Now()
	defer func() { c.metrics.recordLatency("read", time.Since(start)) }()
	if !validKey(req.GetKey()) {
		return nil, status.Error(codes.InvalidArgument, "invalid document key")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	responses := fanout(ctx, c, "read", PreferredOwners(req.Key), func(ctx context.Context, id string) (*pb.DocumentVersions, error) {
		if id == c.store.id {
			return c.store.ReadVersions(ctx, req)
		}
		return pb.NewReplicaPeerClient(c.client.connections[id]).ReadVersions(ctx, req, grpc.WaitForReady(true))
	})
	if len(responses) < readQuorum {
		c.metrics.recordQuorumFailure("read")
		return nil, quorumError(QuorumUnavailable, "fewer than two preferred owners answered")
	}
	siblings, err := maximalVersions(responses)
	if err != nil {
		return nil, err
	}
	c.metrics.recordSiblings(len(siblings))
	return &pb.DocumentVersions{Siblings: siblings, Context: combined(siblings)}, nil
}

func maximalVersions(responses []*pb.DocumentVersions) ([]*pb.Mutation, error) {
	type dot struct {
		id      string
		counter uint64
	}
	byDot := map[dot]*pb.Mutation{}
	for _, response := range responses {
		for _, version := range response.GetSiblings() {
			key := dot{version.GetVersion().GetReplicaId(), version.GetVersion().GetCounter()}
			if prior := byDot[key]; prior != nil && !proto.Equal(prior, version) {
				return nil, status.Error(codes.DataLoss, "replicas returned different envelopes for the same version")
			}
			byDot[key] = version
		}
	}
	var maximal []*pb.Mutation
	for key, version := range byDot {
		dominated := false
		for otherKey, other := range byDot {
			if key != otherKey && observes(other.Context, version.Version) {
				dominated = true
				break
			}
		}
		if !dominated {
			maximal = append(maximal, version)
		}
	}
	slices.SortFunc(maximal, func(a, b *pb.Mutation) int {
		if order := strings.Compare(a.Version.ReplicaId, b.Version.ReplicaId); order != 0 {
			return order
		}
		switch {
		case a.Version.Counter < b.Version.Counter:
			return -1
		case a.Version.Counter > b.Version.Counter:
			return 1
		default:
			return 0
		}
	})
	return maximal, nil
}

func (c *Coordinator) ListDocuments(ctx context.Context, req *pb.ListDocumentsRequest) (*pb.DocumentPage, error) {
	start := time.Now()
	defer func() { c.metrics.recordLatency("list", time.Since(start)) }()
	limit, err := documentPageLimit(req)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	pages := fanout(ctx, c, "list", replicaIDs[:], func(ctx context.Context, id string) (*pb.DocumentPage, error) {
		if id == c.store.id {
			return c.store.ListLocalDocuments(ctx, req)
		}
		return pb.NewReplicaPeerClient(c.client.connections[id]).ListLocalDocuments(ctx, req, grpc.WaitForReady(true))
	})
	// Three of four responders intersect any successful two-owner write.
	if len(pages) < 3 {
		c.metrics.recordQuorumFailure("list")
		return nil, quorumError(QuorumUnavailable, "fewer than three replicas answered document discovery")
	}
	ids := map[string]bool{}
	more := false
	for _, page := range pages {
		for _, key := range page.Keys {
			ids[key.Id] = true
		}
		more = more || page.NextCursor != ""
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	slices.Sort(sorted)
	if len(sorted) > limit {
		sorted, more = sorted[:limit], true
	}
	page := &pb.DocumentPage{}
	for _, id := range sorted {
		page.Keys = append(page.Keys, &pb.DocumentKey{Kind: req.Kind, Id: id})
	}
	if more && len(sorted) > 0 {
		page.NextCursor = sorted[len(sorted)-1]
	}
	return page, nil
}

func (c *Coordinator) WriteMetrics(w io.Writer) {
	c.metrics.write(w, c.store.sqlFailures.Load())
}
