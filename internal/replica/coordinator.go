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
	id    string
	value T
	err   error
}

func collectReplies[T any](ctx context.Context, c *Coordinator, operation string, ids []string,
	call func(context.Context, string) (T, error)) []result[T] {
	responses := make(chan result[T], len(ids))
	for _, id := range ids {
		go func() {
			peerContext, cancel := context.WithTimeout(ctx, c.peerTimeout)
			defer cancel()
			value, err := call(peerContext, id)
			c.metrics.recordPeer(operation, id, err == nil)
			responses <- result[T]{id, value, err}
		}()
	}
	// Every call inherits a deadline or service cancellation. Collecting each
	// result preserves completed acknowledgements even when the deadline fires.
	replies := make([]result[T], 0, len(ids))
	for range ids {
		replies = append(replies, <-responses)
	}
	return replies
}

func fanout[T any](ctx context.Context, c *Coordinator, operation string, ids []string,
	call func(context.Context, string) (T, error)) []T {
	var values []T
	for _, reply := range collectReplies(ctx, c, operation, ids, call) {
		if reply.err == nil {
			values = append(values, reply.value)
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
	owners := PreferredOwners(mutation.Key)
	request := &pb.MergeRequest{Call: req.GetCall(), Mutation: mutation}
	deadline, _ := ctx.Deadline()
	preferredCtx, stopPreferred := context.WithTimeout(ctx, min(c.peerTimeout, time.Until(deadline)/2))
	replies := collectReplies(preferredCtx, c, "write", owners, func(ctx context.Context, id string) (struct{}, error) {
		return struct{}{}, c.mergeAt(ctx, id, request)
	})
	stopPreferred()
	acks := 0
	var missing []string
	var rejection error
	for _, reply := range replies {
		switch status.Code(reply.err) {
		case codes.OK:
			acks++
		case codes.InvalidArgument, codes.AlreadyExists, codes.DataLoss:
			rejection = reply.err
		default:
			missing = append(missing, reply.id)
		}
	}
	if rejection != nil {
		c.metrics.recordQuorumFailure("write")
		return nil, rejection
	}
	if len(missing) > 0 && ctx.Err() == nil {
		fallback := fallbackReplica(owners)
		fallbackCtx, stopFallback := context.WithTimeout(ctx, c.peerTimeout)
		defer stopFallback()
		err := c.mergeAt(fallbackCtx, fallback, &pb.MergeRequest{Call: req.GetCall(), Mutation: mutation, HintTargets: missing})
		c.metrics.recordPeer("write", fallback, err == nil)
		if err == nil {
			acks++
		}
	}
	if acks < writeQuorum {
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
	owners := PreferredOwners(req.Key)
	type replicaRead struct {
		id       string
		versions *pb.DocumentVersions
	}
	replies := fanout(ctx, c, "read", replicaIDs[:], func(ctx context.Context, id string) (replicaRead, error) {
		request := &pb.ReadDocumentRequest{Call: req.GetCall(), Key: req.Key, HintsOnly: !slices.Contains(owners, id)}
		var versions *pb.DocumentVersions
		var err error
		if id == c.store.id {
			versions, err = c.store.ReadVersions(ctx, request)
		} else {
			versions, err = pb.NewReplicaPeerClient(c.client.connections[id]).ReadVersions(ctx, request, grpc.WaitForReady(true))
		}
		return replicaRead{id, versions}, err
	})
	if len(replies) < readQuorum {
		c.metrics.recordQuorumFailure("read")
		return nil, quorumError(QuorumUnavailable, "fewer than two replicas answered")
	}
	responses := make([]*pb.DocumentVersions, 0, len(replies))
	for _, reply := range replies {
		responses = append(responses, reply.versions)
	}
	siblings, err := maximalVersions(responses)
	if err != nil {
		return nil, err
	}
	// Use the same bounded operation budget for best-effort repairs.
	repairTargets := map[string][]*pb.Mutation{}
	for _, reply := range replies {
		if !slices.Contains(owners, reply.id) {
			continue
		}
		for _, sibling := range siblings {
			if !slices.ContainsFunc(reply.versions.GetSiblings(), func(existing *pb.Mutation) bool { return proto.Equal(existing, sibling) }) {
				repairTargets[reply.id] = append(repairTargets[reply.id], sibling)
			}
		}
	}
	if len(repairTargets) > 0 {
		ids := make([]string, 0, len(repairTargets))
		for id := range repairTargets {
			ids = append(ids, id)
		}
		fanout(ctx, c, "repair", ids, func(ctx context.Context, id string) (struct{}, error) {
			for _, sibling := range repairTargets[id] {
				if err := c.mergeAt(ctx, id, &pb.MergeRequest{Call: req.GetCall(), Mutation: sibling}); err != nil {
					return struct{}{}, err
				}
			}
			return struct{}{}, nil
		})
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
	c.writeRecoveryMetrics(w)
}
