package replica

import (
	"fmt"
	"io"
	"maps"
	"sync"
	"time"
)

type peerMetric struct {
	operation string
	id        string
	success   bool
}

type operationMetric struct {
	count   uint64
	elapsed time.Duration
	failed  uint64
}

type coordinatorMetrics struct {
	mu         sync.Mutex
	peers      map[peerMetric]uint64
	operations map[string]operationMetric
	siblings   uint64
	conflicts  uint64
}

func (m *coordinatorMetrics) recordPeer(operation, id string, success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.peers == nil {
		m.peers = map[peerMetric]uint64{}
	}
	m.peers[peerMetric{operation, id, success}]++
}

func (m *coordinatorMetrics) recordLatency(operation string, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.operations == nil {
		m.operations = map[string]operationMetric{}
	}
	metric := m.operations[operation]
	metric.count++
	metric.elapsed += duration
	m.operations[operation] = metric
}

func (m *coordinatorMetrics) recordQuorumFailure(operation string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.operations == nil {
		m.operations = map[string]operationMetric{}
	}
	metric := m.operations[operation]
	metric.failed++
	m.operations[operation] = metric
}

func (m *coordinatorMetrics) recordSiblings(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.siblings += uint64(count)
	if count > 1 {
		m.conflicts++
	}
}

func (m *coordinatorMetrics) write(w io.Writer, sqlFailures uint64) {
	m.mu.Lock()
	peers := maps.Clone(m.peers)
	operations := maps.Clone(m.operations)
	siblings, conflicts := m.siblings, m.conflicts
	m.mu.Unlock()
	fmt.Fprintln(w, "# TYPE flashkart_replica_peer_requests_total counter")
	for _, operation := range []string{"read", "write", "list", "repair", "handoff", "sync"} {
		for _, id := range replicaIDs {
			fmt.Fprintf(w, "flashkart_replica_peer_requests_total{operation=%q,replica=%q,result=\"ok\"} %d\n",
				operation, id, peers[peerMetric{operation, id, true}])
			fmt.Fprintf(w, "flashkart_replica_peer_requests_total{operation=%q,replica=%q,result=\"error\"} %d\n",
				operation, id, peers[peerMetric{operation, id, false}])
		}
	}
	fmt.Fprintln(w, "# TYPE flashkart_replica_quorum_failures_total counter")
	for _, operation := range []string{"read", "write", "list"} {
		fmt.Fprintf(w, "flashkart_replica_quorum_failures_total{operation=%q} %d\n", operation, operations[operation].failed)
	}
	fmt.Fprintln(w, "# TYPE flashkart_replica_operation_duration_seconds summary")
	for _, operation := range []string{"read", "write", "list"} {
		metric := operations[operation]
		fmt.Fprintf(w, "flashkart_replica_operation_duration_seconds_count{operation=%q} %d\n", operation, metric.count)
		fmt.Fprintf(w, "flashkart_replica_operation_duration_seconds_sum{operation=%q} %g\n", operation, metric.elapsed.Seconds())
	}
	fmt.Fprintf(w, "# TYPE flashkart_replica_sql_failures_total counter\nflashkart_replica_sql_failures_total %d\n", sqlFailures)
	fmt.Fprintf(w, "# TYPE flashkart_replica_siblings_total counter\nflashkart_replica_siblings_total %d\n", siblings)
	fmt.Fprintf(w, "# TYPE flashkart_replica_conflicts_total counter\nflashkart_replica_conflicts_total %d\n", conflicts)
}
