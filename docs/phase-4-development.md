# Phase 4 replica recovery

Apply migration 003 before starting the agents. It records hint creation times for diagnostics. Earlier hints receive the migration timestamp.

```sh
make dev-up migrate
make run-replicas
```

Use a second terminal for `make test-replication-recovery`. The suite starts four gRPC agents backed by the four independent PostgreSQL databases. Its fault controls live in the test harness. They stop and reopen an agent, block its SQL connections, partition peer RPCs, and drop a handoff reply after the target commits.

To exercise a real database outage against the running local agents, use `make replica-db-fault REPLICA=b FAULT=stop` and recover it with `FAULT=start`. Use `FAULT=restart` to check that the same volume retains committed data. Pass `COMPOSE='podman compose'` for Podman. Inspect the selected replica's `/readyz` and the peer `Inspect` RPC during the fault.

## Quorum and recovery behavior

- Writes try the three preferred owners first. The remaining replica can commit the version with one hint per unavailable owner. The fallback counts once toward W=2. The coordinator reserves part of the overall deadline for fallback.
- Reads contact all four replicas and require two database-backed responses. The fallback supplies only versions with outstanding hints for that key. Retiring a hint removes its eligibility for fallback reads while preserving the immutable envelope for idempotency.
- A successful read repairs reachable preferred owners that lack a returned sibling. Repairs use the same bounded request deadline and causal merge as writes.
- Handoff runs every second. The sender deletes an individual target's hint after the target acknowledges its durable merge. A lost reply or interruption leaves the hint eligible for replay.
- Background synchronization scans visible versions and outstanding hints in pages every ten seconds. It sends each eligible envelope to its preferred owners. The worker sends records through the same peer merge path as normal writes.

A 2–2 partition can serve writes on both sides. Sloppy read and write quorums can be disjoint, so reads may be stale or temporarily empty. Concurrent versions remain siblings after healing until a caller resolves them with the combined causal context.

The peer `Inspect` RPC reports SQL readiness, pending hints, oldest hint age, scanned synchronization records, and the last completed scan time. The scan timestamp reports progress through the local records. Peer error counters and handoff completion counters show delivery outcomes separately.
