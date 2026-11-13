package replica

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "flashkart/gen/flashkart/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type recoveryCluster struct {
	ctx          context.Context
	addresses    map[string]string
	stores       map[string]*Store
	coordinators map[string]*Coordinator
	servers      map[string]*grpc.Server
	clients      map[string]*Client
	driver       *Client
	partition    atomic.Bool
	right        map[string]bool
	dropReply    atomic.Pointer[pb.Mutation]
	lostTarget   string
	sqlBlocked   map[string]*atomic.Bool
}

func startRecoveryCluster(t *testing.T, right map[string]bool, lostTarget string) *recoveryCluster {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	cluster := &recoveryCluster{ctx: ctx, addresses: map[string]string{}, stores: map[string]*Store{},
		coordinators: map[string]*Coordinator{}, servers: map[string]*grpc.Server{}, clients: map[string]*Client{},
		right: right, lostTarget: lostTarget, sqlBlocked: map[string]*atomic.Bool{}}
	listeners := map[string]net.Listener{}
	for _, id := range replicaIDs {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { l.Close() })
		cluster.addresses[id], listeners[id] = l.Addr().String(), l
	}
	for _, id := range replicaIDs {
		client := &Client{connections: map[string]*grpc.ClientConn{}, timeout: time.Second}
		t.Cleanup(client.Close)
		for _, target := range replicaIDs {
			conn, err := grpc.NewClient("passthrough:///"+cluster.addresses[target],
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
					if cluster.partition.Load() && cluster.right[id] != cluster.right[target] {
						return status.Error(codes.Unavailable, "test peer partition")
					}
					err := invoke(ctx, method, req, reply, conn, opts...)
					if err == nil && target == cluster.lostTarget && method == pb.ReplicaPeer_DeliverHint_FullMethodName {
						expected := cluster.dropReply.Load()
						if expected != nil && proto.Equal(req.(*pb.ApplyMutationRequest).GetMutation(), expected) &&
							cluster.dropReply.CompareAndSwap(expected, nil) {
							return status.Error(codes.Unavailable, "test lost reply after destination commit")
						}
					}
					return err
				}))
			if err != nil {
				t.Fatal(err)
			}
			client.connections[target] = conn
		}
		cluster.clients[id] = client
		cluster.sqlBlocked[id] = new(atomic.Bool)
		cluster.start(t, id, listeners[id])
	}
	driver, err := NewClient(cluster.addresses, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(driver.Close)
	cluster.driver = driver
	return cluster
}

func (c *recoveryCluster) start(t *testing.T, id string, l net.Listener) {
	t.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("FLASHKART_REPLICA_" + strings.ToUpper(id) + "_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	gate := c.sqlBlocked[id]
	config.BeforeConnect = func(context.Context, *pgx.ConnConfig) error {
		if gate.Load() {
			return errors.New("test SQL connection blocked")
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(c.ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	store := &Store{pool: pool, id: id}
	t.Cleanup(store.Close)
	if !store.Ready(c.ctx) {
		t.Fatalf("replica %s database is unavailable", id)
	}
	coordinator := NewCoordinator(store, c.clients[id], 500*time.Millisecond, 1500*time.Millisecond)
	server := grpc.NewServer()
	pb.RegisterReplicaPeerServer(server, store)
	pb.RegisterCatalogReplicationServer(server, coordinator)
	c.stores[id], c.coordinators[id], c.servers[id] = store, coordinator, server
	t.Cleanup(server.Stop)
	go server.Serve(l)
}

func TestReplicationRecovery(t *testing.T) {
	if os.Getenv("FLASHKART_TEST_CLUSTER") != "1" {
		t.Skip("run make test-replication-recovery")
	}
	key := &pb.DocumentKey{Kind: pb.DocumentKind_DOCUMENT_KIND_PRODUCT, Id: rand.Text()}
	owners := PreferredOwners(key)
	left, right, fallback := owners[0], owners[2], fallbackReplica(owners)
	cluster := startRecoveryCluster(t, map[string]bool{right: true, fallback: true}, left)
	ctx := cluster.ctx
	prepare := func(id string, key *pb.DocumentKey, label string, context *pb.Context) *pb.Mutation {
		t.Helper()
		value, err := cluster.driver.Prepare(ctx, id, &pb.PrepareMutationRequest{
			Key: key, Context: context, Call: &pb.Call{OperationId: key.Id + "-" + label},
			DocumentJson: []byte(fmt.Sprintf(`{"product_id":%q,"name":%q}`, key.Id, label)),
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	apply := func(id string, value *pb.Mutation) {
		t.Helper()
		if _, err := cluster.driver.Apply(ctx, id, &pb.ApplyMutationRequest{Mutation: value}); err != nil {
			t.Fatal(err)
		}
	}
	read := func(id string, key *pb.DocumentKey) *pb.DocumentVersions {
		t.Helper()
		result, err := cluster.driver.Read(ctx, id, &pb.ReadDocumentRequest{Key: key})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	hints := func(id string, key *pb.DocumentKey) []string {
		t.Helper()
		var targets []string
		err := cluster.stores[id].pool.QueryRow(ctx, "SELECT ARRAY(SELECT target FROM replica.hints WHERE kind=$1 AND document_id=$2 ORDER BY target)", int32(key.Kind), key.Id).Scan(&targets)
		if err != nil {
			t.Fatal(err)
		}
		return targets
	}
	physical := func(id string, value *pb.Mutation) bool {
		t.Helper()
		result, err := cluster.stores[id].ReadVersions(ctx, &pb.ReadDocumentRequest{Key: value.Key})
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(result.Siblings, func(m *pb.Mutation) bool { return proto.Equal(m, value) })
	}

	cluster.partition.Store(true)
	leftValue := prepare(left, key, "left", nil)
	apply(left, leftValue)
	if versions := read(right, key); len(versions.Siblings) != 0 {
		t.Fatalf("right partition unexpectedly saw left write: %v", versions)
	}
	rightValue := prepare(right, key, "right", nil)
	apply(right, rightValue)
	hinted, err := cluster.stores[fallback].ReadVersions(ctx, &pb.ReadDocumentRequest{Key: key, HintsOnly: true})
	if err != nil || len(hinted.GetSiblings()) != 1 || !proto.Equal(hinted.Siblings[0], rightValue) {
		t.Fatalf("hinted read: %v %v", hinted, err)
	}
	if len(hints(fallback, key)) != 2 || !physical(fallback, rightValue) {
		t.Fatal("fallback did not durably hold both target hints")
	}
	if versions := read(right, key); len(versions.Siblings) != 1 || !proto.Equal(versions.Siblings[0], rightValue) {
		t.Fatalf("right partition could not read its write: %v", versions)
	}

	restartFallback := func() {
		t.Helper()
		cluster.servers[fallback].Stop()
		cluster.stores[fallback].Close()
		listener, err := net.Listen("tcp", cluster.addresses[fallback])
		if err != nil {
			t.Fatal(err)
		}
		cluster.start(t, fallback, listener)
		peer := pb.NewReplicaPeerClient(cluster.driver.connections[fallback])
		if _, err := peer.Inspect(ctx, &pb.ReplicaStatusRequest{}, grpc.WaitForReady(true)); err != nil {
			t.Fatalf("restarted fallback did not answer: %v", err)
		}
	}
	// Restart while both intended owners are unreachable.
	restartFallback()
	cluster.coordinators[fallback].recoverPass(ctx, true)
	if len(hints(fallback, key)) != 2 {
		t.Fatal("restart or unreachable targets lost hints")
	}
	inspection, err := cluster.driver.Inspect(ctx, fallback, &pb.ReplicaStatusRequest{})
	if err != nil || inspection.PendingHints < 2 {
		t.Fatalf("hint diagnostics: %v %v", inspection, err)
	}

	cluster.partition.Store(false)
	cluster.dropReply.Store(rightValue)
	cluster.coordinators[fallback].recoverPass(ctx, true)
	if cluster.dropReply.Load() != nil || !physical(left, rightValue) || !slices.Contains(hints(fallback, key), left) {
		t.Fatal("destination commit with lost reply did not preserve the source hint")
	}
	restartFallback()
	cluster.coordinators[fallback].recoverPass(ctx, true)
	if len(hints(fallback, key)) != 0 {
		t.Fatal("committed delivery replay did not clear hints")
	}
	hinted, err = cluster.stores[fallback].ReadVersions(ctx, &pb.ReadDocumentRequest{Key: key, HintsOnly: true})
	if err != nil || len(hinted.GetSiblings()) != 0 {
		t.Fatalf("retired hint remained readable: %v %v", hinted, err)
	}
	versions := read(left, key)
	if len(versions.Siblings) != 2 {
		t.Fatalf("healed partition did not expose siblings: %v", versions)
	}
	resolved := prepare(left, key, "resolved", versions.Context)
	apply(right, resolved)
	for _, owner := range owners {
		if !physical(owner, resolved) || physical(owner, leftValue) || physical(owner, rightValue) {
			t.Fatalf("owner %s did not converge after resolution", owner)
		}
	}

	// Seed one preferred owner by peer merge to isolate the read repair path.
	repairKey := &pb.DocumentKey{Kind: key.Kind, Id: rand.Text()}
	repairOwners := PreferredOwners(repairKey)
	repair := prepare(repairOwners[0], repairKey, "repair", nil)
	if _, err := cluster.stores[repairOwners[0]].ApplyMutation(ctx, &pb.ApplyMutationRequest{Mutation: repair}); err != nil {
		t.Fatal(err)
	}
	if physical(repairOwners[1], repair) {
		t.Fatal("read repair fixture was already replicated")
	}
	if got := read(left, repairKey); len(got.Siblings) != 1 || !physical(repairOwners[1], repair) {
		t.Fatalf("read did not repair reachable stale owner: %v", got)
	}

	// Block SQL on one still-running agent. It must answer diagnostics but
	// cannot contribute a successful empty read to quorum.
	cluster.sqlBlocked[right].Store(true)
	cluster.stores[right].pool.Reset()
	diagnostic, err := cluster.driver.Inspect(ctx, right, &pb.ReplicaStatusRequest{})
	if err != nil || diagnostic.GetBackendReady() {
		t.Fatalf("SQL outage diagnostics: %v %v", diagnostic, err)
	}
	_, err = pb.NewReplicaPeerClient(cluster.driver.connections[right]).ReadVersions(ctx, &pb.ReadDocumentRequest{Key: key})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("blocked SQL read returned %v", err)
	}
	if got := read(left, key); len(got.Siblings) != 1 || !proto.Equal(got.Siblings[0], resolved) {
		t.Fatalf("read during SQL outage: %v", got)
	}
	cluster.sqlBlocked[right].Store(false)

	// With only the fallback online, multiple target hints still count as one acknowledgement.
	for _, owner := range owners {
		cluster.servers[owner].Stop()
	}
	alone := prepare(fallback, key, "alone", combined([]*pb.Mutation{resolved}))
	_, err = cluster.driver.Apply(ctx, fallback, &pb.ApplyMutationRequest{Mutation: alone})
	requireReason(t, err, WriteOutcomeUnknown)
	if len(hints(fallback, key)) != 3 || !physical(fallback, alone) {
		t.Fatal("unknown outcome did not retain its partial write")
	}
	_, err = cluster.driver.Read(ctx, fallback, &pb.ReadDocumentRequest{Key: key})
	requireReason(t, err, QuorumUnavailable)
}

func TestReplicationBackgroundSync(t *testing.T) {
	if os.Getenv("FLASHKART_TEST_CLUSTER") != "1" {
		t.Skip("run make test-replication-recovery")
	}
	cluster := startRecoveryCluster(t, nil, "")
	key := &pb.DocumentKey{Kind: pb.DocumentKind_DOCUMENT_KIND_PRODUCT, Id: rand.Text()}
	owners := PreferredOwners(key)
	source := owners[0]
	ctx := cluster.ctx
	value, err := cluster.driver.Prepare(ctx, source, &pb.PrepareMutationRequest{
		Key: key, Call: &pb.Call{OperationId: key.Id},
		DocumentJson: []byte(`{"product_id":"` + key.Id + `","name":"untouched"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cluster.stores[source].ApplyMutation(ctx, &pb.ApplyMutationRequest{Mutation: value}); err != nil {
		t.Fatal(err)
	}
	page, err := cluster.stores[source].ScanVersions(ctx, &pb.SyncRequest{Limit: 1})
	if err != nil || len(page.GetRecords()) != 1 {
		t.Fatalf("first scan page: %v %v", page, err)
	}
	if page.NextCursor != "" {
		next, err := cluster.stores[source].ScanVersions(ctx, &pb.SyncRequest{Cursor: page.NextCursor, Limit: 1})
		if err != nil || len(next.GetRecords()) != 1 || proto.Equal(next.Records[0], page.Records[0]) {
			t.Fatalf("scan cursor did not advance: %v %v", next, err)
		}
	}

	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		cluster.coordinators[source].RunRecovery(workerCtx)
	}()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		complete := true
		for _, owner := range owners[1:] {
			reply, err := cluster.stores[owner].ReadVersions(ctx, &pb.ReadDocumentRequest{Key: key})
			if err != nil {
				t.Fatal(err)
			}
			complete = complete && len(reply.Siblings) == 1 && proto.Equal(reply.Siblings[0], value)
		}
		if complete {
			info, err := cluster.driver.Inspect(ctx, source, &pb.ReplicaStatusRequest{})
			if err == nil && info.SyncRecordsScanned > 0 && info.LastSyncCompletedUnix > 0 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("untouched document did not converge through periodic synchronization")
}
