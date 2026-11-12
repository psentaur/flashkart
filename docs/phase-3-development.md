# Phase 3 replica coordination

Phase 4 extends this baseline with sloppy quorums and automatic recovery. [Phase 4 development](phase-4-development.md)

```sh
make dev-up migrate
make run-replicas
```

Run the replication checks in a second terminal with `make test-replication-core`. The tests use separate loopback gRPC listeners and the four real PostgreSQL backends. Press Ctrl+C in the launcher to stop the agents.

| Replica | gRPC | HTTP health and metrics | PostgreSQL |
| --- | --- | --- | --- |
| A | 7001 | 8101 | 5541 |
| B | 7002 | 8102 | 5542 |
| C | 7003 | 8103 | 5543 |
| D | 7004 | 8104 | 5544 |

All ports bind to loopback. Each agent process receives its own database credential. Use `/readyz` to check the backend and `/metrics` to see peer participation, quorum failures, SQL failures, operation latency, sibling counts, and conflicts.

## Coordinator contract

The shared Go client takes an explicit origin for preparation. Retain the origin and operation ID for preparation retries. After preparation returns an immutable envelope, supply that envelope to any coordinator for application or retry. The document can also be read through any coordinator. Read-only diagnostics query a specific replica.

The ring hashes the document kind and ID with SHA-256 and uses 128 virtual nodes per replica. All agents independently compute the same three preferred owners. The coordinator sends RPCs concurrently and collects available responses up to the peer deadline. Defaults are 250 ms per peer and one second per operation. Configure them with `peer_timeout_ms` and `operation_timeout_ms` in the agent JSON.

- Apply requires two durable acknowledgements from the preferred owners. If fewer are confirmed, gRPC returns `UNAVAILABLE` with the `WRITE_OUTCOME_UNKNOWN` error reason. A partial write may already exist. Retry the same envelope.
- Read requires two successful preferred-owner responses. A successful empty read counts toward the quorum. An insufficient quorum returns `UNAVAILABLE` with `QUORUM_UNAVAILABLE`. Concurrent siblings appear together with their exact combined context. To resolve them, prepare a replacement using that context and apply it.
- List merges sorted pages from the four replicas. It requires three successful responses so the responding set intersects any two-owner write. Listing is eventually consistent across pages.
- Phase 3 uses preferred owners for document reads and writes. Fallback writes, hinted reads and repair belong to Phase 4.

`make seed` retains the Phase 2 local-storage fixture on replica A. The Phase 3 integration test creates its own documents via the quorum API and inspects the expected physical copies.
