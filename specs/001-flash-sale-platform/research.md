# Research Decisions: Flash Sale Platform

**Date**: 2026-10-25 | **Status**: Complete for Phase 1 design
**Inputs**: [spec.md](spec.md), the source discussion, Constitution v1.0.0, and the user's
subsequent Goka/Sarama and ownership-scope instructions, plus the three accepted clarifications.

Independent documentation searches covered Mukthish's stack, messaging, recovery, shared
interfaces and validation. No agent-delegation tool is available; research was performed directly.
The decisions below are proposals for implementation, not evidence of running software.

## R1. Scope concrete stack choices to Mukthish

**Decision**: Select Go 1.27.3 only for your Admission, Flash Sale, Order, Analytics, gateway and
frontend server. Use Goka v1.1.17 for Analytics and IBM Sarama v1.46.3 for your Kafka communication;
use Go standard HTTP/slog/html/template, grpc-go/protobuf for your Inventory client, pgx v5 and
PostgreSQL 17 for your transactional state. Serve the frontend with Go templates and small plain
JavaScript modules; no mandatory separate Node/Java toolchain. Pin modules and image digests.

**Rationale**: This follows the user's requested Go-based scope and avoids choosing implementation
stacks for the other two members. Go frontend serving is a concrete default for your supporting
work, not a shared framework mandate. Goka uses Sarama to communicate with Kafka.

**Alternatives considered**: A mandatory React/TypeScript or Java stack adds runtimes the user did
not request. Applying Go/PostgreSQL versions to Inventory, Payment, Catalog or Notification would
exceed the requested ownership scope.

**Sources**: [Go releases](https://go.dev/doc/devel/release),
[Goka releases](https://github.com/lovoo/goka/releases),
[Goka dependency manifest](https://github.com/lovoo/goka/blob/v1.1.17/go.mod),
[Sarama](https://github.com/IBM/sarama).

## R2. Distinguish framework, client, and broker

**Decision**: Goka replaces Flink; Sarama replaces the previously proposed Go Kafka client.
Kafka remains the Message Queue/broker, with separate raw analytics, result and compacted group
state topics. Use a Kafka-compatible protocol configuration supported by Sarama and verify it
against the chosen broker during integration; use the same Sarama version through your Go modules.

**Rationale**: Goka is a stateful Go library over Kafka and Sarama is a Go Kafka client. Neither is
an alternative broker. This is the user's explicit amendment to the source's earlier Flink and
managed stream-runtime requirement; do not silently claim those execution choices still apply.

**Alternatives considered**: Removing Kafka prevents Goka from providing its documented state and
rebalancing model. Retaining Flink/Java alongside Goka adds a second analytics implementation.

**Sources**: [Goka architecture](https://github.com/lovoo/goka),
[Sarama client](https://github.com/IBM/sarama).

## R3. Shared architecture without teammate-stack mandates

**Decision**: Keep REST, Inventory gRPC, versioned events, owned persistent state, managed
Kubernetes, serverless Notifications, infrastructure-as-code/GitOps, Prometheus/Grafana, and three
storage-product categories. Your Analytics runs as a recoverable Goka workload on the managed
Kubernetes platform, alongside the original six application services. Teammate 2 chooses Inventory,
Payment and platform technologies; teammate 3 chooses Catalog, Notification and custom-store
technologies. Your Kafka broker and client configuration are coordinated with the platform owner.

**Rationale**: Shared interface/reliability constraints enable independent implementation without
choosing other people's languages, database engines, function platform, cloud provider or IaC tools.
These delegated choices are not blockers to Mukthish's design and do not waive acceptance gates.

**Alternatives considered**: A fixed AWS/EKS/RDS/Lambda/Terraform prescription would select much of
the other members' stack. Concrete provider deployment recipes belong to their owned planning work.

## R4. Mukthish's transactional state and cross-service recovery

**Decision**: Use separate PostgreSQL databases for your Flash Sale, Admission and Order state,
with conditional updates/row locks, unique keys, durable inbox/outbox records, and short transactions.
Use SQL quota rows and expiring leases for shared Admission limits at the initial workload. Inventory
must provide transactional reservation guarantees through its contract; its engine is its owner's
choice. Order owns the saga and queries Inventory after ambiguous transition responses.

**Rationale**: Durable request identity plus stable downstream IDs prevents duplicate reservations
or charges across crashes. A lost successful confirmation must be recovered, not blindly refunded.
Shared SQL admission avoids adding a cache to your stack until measured contention warrants it.

**Alternatives considered**: A cross-service shared database or a global transaction couples teams.
In-memory replica counters cannot enforce shared limits. A cache is a later optimization requiring
its own correctness and recovery measurements.

**Sources**: [PostgreSQL locking](https://www.postgresql.org/docs/17/sql-select.html),
[transactions](https://www.postgresql.org/docs/17/tutorial-transactions.html).

## R5. Sarama delivery and application idempotency

**Decision**: Use Sarama producers with WaitForAll acknowledgements and idempotence, appropriate
in-flight settings for the pinned client, and consumers that mark offsets only after durable work.
Persist business inbox/outbox deduplication regardless of producer settings. Each subscriber has
its own consumer group; analytics consumes only analytics copies of domain records. Keep six
partitions and seven-day raw retention; precreate topics with the broker owner, including compacted
Goka tables and matching partition/hash configuration. Do not copy another client's config names.

**Rationale**: Kafka log delivery and SQL side effects are different durability boundaries. Outbox
delivery receipts track domain and analytics destinations separately; replay preserves event IDs.

**Alternatives considered**: One shared group loses fan-out; auto-committing before database work
can lose effects; producer idempotence alone cannot deduplicate a charge or an outbox replay.

**Sources**: [Sarama configuration](https://github.com/IBM/sarama/blob/v1.46.3/config.go),
[Kafka semantics](https://kafka.apache.org/documentation/#semantics).

## R6. Goka state, event-time cutoffs, and recoverable output

**Decision**: Key the analytics input and Goka group table by sale/product/minute window and use
six matching partitions with the same key hash. Persist aggregates, seen IDs, finalization,
monotonic revisions, and deterministic output identities in the compacted group table. The
processor updates state only. A Goka View observes durable group-table records; a results relay
within the Analytics service emits those stored revisions through Sarama and retries from the
view after restart. It does not publish results directly from an uncommitted processing callback.

Use occurrence time to select a window and Kafka LogAppendTime on the analytics input as stable
arrival time. An Analytics clock emitter discovers scheduled windows from Flash Sale and sends
per-window heartbeat records every second, through the same partitioner/input topic. These create
zero-activity keys and finalize a window once a heartbeat's broker timestamp reaches end+60s.
Records ingested at/after the cutoff are late even if a heartbeat is delayed. Replay therefore
uses recorded arrival/tick ordering rather than the restarted process's current wall clock.

**Rationale**: Goka processing is at least once. State-before-output through the view plus
repeat-safe result upserts repairs a crash between state persistence and result publication.
The relay scans/requeues view entries on startup and periodically; duplicate output retains the
same summary/revision/event identity. Late outputs are retained in state and relayed similarly.
A one-second tick, one-second relay scheduling budget, and three-second processing/projection
budget target five-second final visibility. Readiness requires group-table/view recovery. Retain
dedup/finalization state for eight days, beyond seven-day raw replay, and bound state size per key.

**Alternatives considered**: Flink checkpoint/transaction assumptions do not apply to Goka.
Emitting before durable state can expose unrecoverable partial output. Skipping duplicate input
without repairing stored output can lose a result. Wall-clock timers alone can finalize before
backlog replay. Adding every result revision instead of replacing it double-counts totals.

**Sources**: [Goka processing/state model](https://pkg.go.dev/github.com/lovoo/goka),
[processor API](https://github.com/lovoo/goka/blob/master/processor.go),
[callback API](https://github.com/lovoo/goka/blob/master/context.go),
[Kafka topic timestamps](https://kafka.apache.org/documentation/#topicconfigs_message.timestamp.type).
The window and result-relay rules are this project's design, not claimed library guarantees.

## R7. Teammate-owned behavior contracts

**Decision**: Specify Inventory transitions, stable simulated Payment results/refunds, Notification
canonical notices and retry/replay behavior, and leaderless Catalog quorum/conflict/repair behavior
as language-neutral contracts. Do not select libraries, engine versions, local storage formats,
cloud functions, or build/test frameworks for those services. Require each owner to supply its
adapter, readiness, start/test commands and acceptance evidence before joint integration.

**Rationale**: Consumers need observable guarantees, not knowledge of implementation internals.
Leaderless metadata retains N=3/R=2/W=2, causal version contexts, preserved conflicts and independently
durable replicas; its owner chooses how to implement the agreed original concept.

**Alternatives considered**: Providing only mocks would not prove final integration; prescribing
Go/SQLite or a particular function provider would violate the requested stack-selection scope.

**Source**: [Dynamo design](https://www.allthingsdistributed.com/files/amazon-dynamo-sosp2007.pdf).

## R8. Quality, deployment handoff and cost scope

**Decision**: Define Go formatting, vet/static analysis, race tests, Goka tester plus Kafka restart/
rebalance tests, schema/contract checks and browser/load scenarios for your work. Shared CI invokes
other owners' declared checks without imposing their unit frameworks. Platform deployment must
supply resource-based HPA for Admission/Order, private internal operations, public HTTPS, structured
logs/metrics and rollback evidence. No provider-specific resource version or paid service is selected.

**Rationale**: The platform owner can satisfy the assignment constraints using their preferred
stack. Infrastructure, storage and broker capacity carry costs dependent on that selection; local
runs precede any separately reviewed cloud provisioning. The current command generates designs only.

**Alternatives considered**: Claiming cloud tests, deployments or teammate approvals without their
implementation/evidence would misrepresent the state of the project.

**Sources**: [Kubernetes HPA](https://kubernetes.io/docs/concepts/workloads/autoscaling/horizontal-pod-autoscale/),
[Goka tester API](https://pkg.go.dev/github.com/lovoo/goka/tester).

## R9. Prevent overlapping same-product sales

**Decision**: In Mukthish's Flash Sale PostgreSQL 17 database, use a half-open UTC `tstzrange`
and a GiST exclusion constraint combining `product_id WITH =` and the window `WITH &&`, with
predicate `cancelled_at IS NULL`. Enable the bundled `btree_gist` extension for UUID equality.
Keep valid start/end checks and version/freeze checks. Map an exclusion violation to HTTP 409
`SALE_WINDOW_CONFLICT`; creation, replacement, cancellation and required outbox changes are atomic.

**Rationale**: PostgreSQL documents exclusion constraints for non-overlapping ranges and
`btree_gist` UUID operator classes. Applying them to this product/window/cancellation rule is the
project's design choice. Database arbitration covers simultaneous create/update requests across
instances; `[start,end)` permits adjacency. Cancelling a future sale releases its interval.

**Alternatives considered**: A query followed by an unprotected insert can admit both writers.
A process mutex cannot coordinate multiple service instances. Independent/shared overlapping
stock pools contradict the accepted clarification.

**Sources**: [PostgreSQL range constraints](https://www.postgresql.org/docs/17/rangetypes.html#RANGETYPES-CONSTRAINT),
[btree_gist operator classes](https://www.postgresql.org/docs/17/btree-gist.html).

## R10. Enforce cumulative shopper allowance at reservation

**Decision**: Add `per_shopper_unit_cap`, default one, to normalized sale configuration, offers,
events and the readiness digest. Freeze it at start. Inventory must protect stock and the logical
`(sale_id, shopper_id)` allowance in one transaction-equivalent decision; its owner chooses the
engine. Count unexpired HELD and CONFIRMED units. Replay a matching operation before fresh checks;
new operations cannot exceed the cap. Released/expired holds free allowance once; late payment
cannot restore them. Return `PURCHASE_LIMIT_EXCEEDED` to Order for a stable HTTP 409 business rejection.

**Rationale**: Admission rate limits measure frequency rather than cumulative purchased units.
Enforcing the cap with the authoritative reservation prevents distinct-key and cross-instance
races. Evaluating expiry at the protected Inventory clock frees allowance without waiting for an
asynchronous projection or cleanup worker. Confirmation preserves consumed allowance.

**Alternatives considered**: Gateway/Admission counters alone can race or reset after a restart.
Counting orders alone misses holds whose Order was never committed. Counting only confirmed
purchases allows one shopper to occupy multiple holds. A mandatory database choice for Inventory
would exceed the user's stack-selection scope.

**Authority**: [Accepted clarification and FR-006/013/014](spec.md#clarifications),
[Inventory contract](contracts/inventory.proto). This is a project invariant, not a library guarantee.

## R11. Define sale analytics eligibility

**Decision**: Admit to sale analytics only authenticated, schema/header-valid purchase submissions
for a known sale and its matching product. Include explicit retries, known inactive/not-ready sales,
and otherwise eligible business rejections. Excluded unauthorized, malformed and unknown/mismatched
traffic contributes only bounded operational reason counters at the first rejecting boundary.
Do not emit AttemptRecorded with invented identities. Internal retries preserve attempt ID.

Keep the admission decision independent of Order's later outcome: stock/cap rejection after
forwarding is still `ADMITTED/NONE`, with final `response_status` and `response_error_code` on
AttemptRecorded. Count its attempt/latency once; do not inflate admission rejections. The emitter
also ticks recovered nonfinal View keys outside scheduled sale windows, so eligible before/after-
window requests finalize normally even with no subsequent business activity.

**Rationale**: This is the user's selected metric boundary. It keeps denominators tied to
identifiable activity and avoids dummy UUIDs. Keeping the two outcomes distinct preserves the
specified admission rejection-rate meaning while retaining business rejection evidence.

**Alternatives considered**: Counting every HTTP submission requires unknown-identity buckets
and changes the accepted denominator. Dropping business rejections understates eligible demand.
Labeling a downstream stock/cap failure as admission rejection confuses separate decision stages.

**Authority**: [FR-020/022/031 and metric meanings](spec.md#functional-requirements).

## R12. Coordinate the amended draft contracts

**Decision**: Record these files as `1.0.0-draft.2`, retaining the planned `/v1`, protobuf v1 and
`.v1` topics because this repository has no released application or deployed contract consumer.
All draft consumers must regenerate together before implementation/integration. Required output
cap fields, readiness-digest semantics and new error results are explicitly breaking relative to
the previous draft. They are not advertised as backward-compatible optional changes.

**Rationale**: A draft revision identifies the updated review baseline without suggesting a live
migration has occurred. [The compatibility gate](contracts/README.md#compatibility-and-review-gate)
provides the adoption order and a new-major path if an owner already has a released consumer.

**Alternatives considered**: Silently treating absent cap as unlimited violates the default.
Assuming producer idempotence makes old/new consumers compatible ignores business semantics.
Inventing a completed production rollout would misstate a planning-only repository.

## Resolution summary

All decisions within your requested stack scope are resolved. Teammate technology choices are
explicitly delegated to their owners, not open questions requiring the user to choose now. Interface
reviews, coordinated draft adoption and actual environment credentials/capacity remain future
integration/deployment inputs. Mukthish's T001–T100 backlog was refreshed against R9–R12 on 2026-10-25. The selected Go release and
Goka-tag Sarama dependency were rechecked against the primary sources linked in R1.
