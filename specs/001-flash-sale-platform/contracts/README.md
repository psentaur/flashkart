# Service Contracts: Flash Sale Platform

**Version**: 1.0.0-draft.2 | **Date**: 2026-10-25 | **Status**: Draft for producer/consumer review

These are Phase 1 design contracts. They describe intended behavior; no endpoint is implemented
by generating these files. They remain feature-local until implementation introduces the root
contract release and generated bindings. [Data ownership and transitions](../data-model.md) are
normative alongside the wire schemas. Changes require the owner and affected consumers to review.

## Contract inventory and review ownership

| Artifact | Boundary / owner | Required reviewers |
| --- | --- | --- |
| [public.openapi.json](public.openapi.json) | Gateway to shopper/operator; Mukthish coordinates | Members 2/3 for their returned data |
| [internal.openapi.json](internal.openapi.json) | Admission -> Order; Order -> Flash Sale; local notice adapter | Mukthish; Member 3 for notices |
| [inventory.proto](inventory.proto) | Order -> Inventory; Member 2 owns Inventory | Mukthish + Member 2 |
| [catalog-store.openapi.json](catalog-store.openapi.json) | Catalog -> coordinator -> replicas; Member 3 | Mukthish for offer separation |
| [events.schema.json](events.schema.json) | Producers and independent subscribers | All affected producers/consumers |
| [examples.json](examples.json) | Independent valid request/response and event-shape fixtures | All members |

## Shared conventions

- REST is JSON over HTTP, HTTPS in cloud; public prefix `/v1`, internal calls `/internal`.
  UUID identifiers, UTC timestamps ending `Z`, integer minor-unit money and explicit currency
  follow the data model. Reject arithmetic overflow and quantities outside 1..100.
- The JSON Schema event contract is draft 2020-12. REST files are OpenAPI 3.1; JSON is used to
  make parsing unambiguous. Examples are shape fixtures, not a single sequential event history.
- Gateway validates JWT subject/role, strips user-supplied internal identity headers, and forwards
  verified context only over authorized service transport. Owning services enforce access too.
  Local calls use seeded credentials; cloud calls use authenticated service transport. Member 3
  supplies the serverless invocation adapter without a platform being selected for that service.
- UUID correlation IDs are generated if absent and propagated across REST, gRPC metadata, Kafka,
  structured logs and notice processing. Do not use shopper IDs as metric labels.
- The public edge forwards Authorization, Idempotency-Key and correlation headers and never caches
  dynamic/authenticated responses. The gateway is an explicit route allowlist, not an unrestricted
  reverse proxy. It exposes no `/internal`, gRPC, stock transition, or payment-operation endpoint.
- An overall purchase response budget is two seconds. Admission leases last two seconds; its
  Order forwarding deadline is 1.5 seconds and downstream calls share that remaining deadline.
  A gRPC call may use at most the remaining budget, never an additional two seconds. Background
  reconciliation uses separate bounded calls and stable IDs. Timeouts mean unknown outcomes.
- HTTP 400 is invalid input; 401 missing/invalid identity; 403 denied role; 404 absent or not owned;
  409 business/version/key conflict; 429 admission rejection with Retry-After; 503 retryable
  dependency/quorum unavailability. All JSON error bodies use Error with a correlation ID.

## Sale and purchase semantics

- Operator PUT of product metadata creates or updates the supplied product UUID. Empty context
  creates a first version; update/resolution supplies merged observed contexts from the operator
  versions endpoint. Ordinary product browsing returns conflict/unavailable instead of selecting
  an arbitrary sibling. Admin version inspection can expose all siblings for explicit resolution.
- Sale creation uses a caller-supplied UUID and `expected_version=0`; update/cancel requires the
  current version. Body/path sale IDs must match. Repeating an identical creation returns the
  existing configuration; the same ID with different creation content conflicts. Mutation is
  prohibited once the initial start time is reached; pre-start replacement increments version.
- Same-product non-cancelled `[start,end)` schedules cannot overlap. Concurrent create/replace
  conflicts return 409 SALE_WINDOW_CONFLICT; adjacent windows and different products are allowed.
  Cancellation before start frees the interval. An endpoint-level precheck is insufficient.
- `per_shopper_unit_cap` is a positive integer, initially one. Omitted SaleWrite input normalizes
  to one (creation and replacement); canonical Sale/Offer and configuration events always include
  it. The operator form starts at one. The cap is frozen at sale start and is part of the sale
  version/readiness agreement; the shopper cannot override it in Purchase.
- Flash Sale emits SaleConfigured. Catalog, Admission and Inventory apply it and emit SaleReady
  with the same version/allocation digest. Despite its retained name, `allocation_digest` covers
  normalized sale/product IDs, version, price/currency, allocation, cap, schedule and admission
  limits using sorted-key compact UTF-8 JSON (no whitespace), lowercase UUIDs, normalized UTC timestamps
  and SHA-256; exclude dynamic
  status/readiness. A digest mismatch cannot activate a sale. Flash Sale emits SaleActivated only when all three are
  ready and the sale is within its window. It persists activation intent durably and resumes it
  after restart. Applied activation plus local schedule/version checks authorize new reservations.
  An early activation event is buffered/reconciled until its configuration exists; obsolete
  activation/readiness/cancellation events cannot regress a newer configuration.
- Shopper GET offer returns authoritative sale terms. Purchase includes expected version, unit
  price and currency. A stale offer returns OFFER_CHANGED/current_offer before reservation; the
  frontend asks the shopper to confirm the new terms before submitting a new logical purchase.
- Idempotency-Key is mandatory on public and internal purchase submission. Scope is authenticated
  shopper + key; canonical payload includes all Purchase fields. Same payload shares one durable
  intent/order; changed payload is IDEMPOTENCY_CONFLICT. An identical retry of an accepted order
  returns 202 while pending or 200 once terminal. The request key must survive client timeouts.
- A pre-acceptance intent under recovery returns 409 REQUEST_IN_PROGRESS, retryable=true and
  retry_after_seconds=1. It never fabricates an accepted order without a hold. Admission 429 or
  temporary 503 can be retried with the same key; durable stock/offer/window rejections are
  replayed as recorded, including PURCHASE_LIMIT_EXCEEDED (409, retryable=false, no Retry-After).
  After a failed/expired hold frees allowance, an explicit new purchase uses a new key; a rejected
  original key does not turn into an accepted purchase. Confirmed units continue consuming the cap.
- Internal Order POST is callable only by Admission. It receives the verified shopper and unique
  attempt ID, uses the same request key, fetches the authoritative offer, reserves stock, commits
  pending Order + PaymentRequested, and only then returns acceptance. Gateway has no saga logic.

## Inventory gRPC and ambiguity handling

- `Reserve` uses `order_id` as a stable operation key. A duplicate with matching payload returns
  the same reservation, including its terminal state; a mismatched payload returns PAYLOAD_MISMATCH.
- A fresh `Reserve` must atomically enforce stock and the applied `(sale_id, shopper_id)` cap.
  Sum unexpired HELD and CONFIRMED units across all order IDs; an orphan hold still counts.
  Exceeding allowance returns PURCHASE_LIMIT_EXCEEDED with no reservation. Matching existing
  outcomes replay before new eligibility checks, including a durably rejected proposed order ID.
  A successful replay returns the current reservation state, not the original HELD snapshot.
  Member 2 chooses the transactional engine and protects equivalent logical accounting.
- Expired holds stop consuming allowance at authoritative expiry even if cleanup is delayed.
  Confirm retains consumed allowance; Release/Expire frees it once. Concurrent distinct keys,
  other instances or restart cannot reset/bypass it. Late payment cannot restore an expired hold.
- `Confirm` succeeds only for a live hold; Inventory samples its clock after acquiring the lock.
  At/after expiry it atomically expires the hold, returns RESERVATION_EXPIRED, and emits expiry.
  Repeated confirmation of CONFIRMED is REPLAYED. Release cannot undo CONFIRMED.
- `Release` before expiry releases; at/after expiry it expires. A terminal repeat returns the
  existing state without moving quantities. Only Inventory decides terminal state.
- `GetReservation` accepts exactly one lookup. An expired HELD read may lazily perform the same
  atomic expiry transition before returning. NOT_FOUND after Reserve timeout is not permission
  to choose a different order ID; retry Reserve with the same one.
- gRPC INVALID_ARGUMENT covers malformed UUID/quantity, UNAUTHENTICATED/PERMISSION_DENIED cover
  caller identity, NOT_FOUND covers unknown lookup, and UNAVAILABLE/DEADLINE_EXCEEDED are unknown
  transient outcomes. Sale/stock/terminal conflicts are OperationResult codes, not transport errors.
- After a lost Confirm response, Order reads current reservation state. CONFIRMED finalizes the
  order; EXPIRED/RELEASED creates a refund for a recorded successful charge. An unreachable
  Inventory leaves Order reconciling; it does not justify a refund or new reservation.

## Kafka topics, keys and independent consumers

Six partitions and seven-day raw retention are initial settings; precreate topics before producers
start. The broker deployment remains your responsibility with the platform owner. Goka group-table
cleanup is compact-only, with the same six partitions/hash as its input; raw input uses deletion
and LogAppendTime. Local one-broker Kafka does not prove broker availability. Never infer
cross-topic ordering. Any partition-count migration requires repartition/rebuild and review.

| Topic | Allowed kinds / producer | Key | Independent consumer groups |
| --- | --- | --- | --- |
| `sale.config.v1` | SaleConfigured, SaleActivated, SaleCancelled / Flash Sale | sale_id | catalog-config-v1, admission-config-v1, inventory-config-v1 |
| `sale.ready.v1` | SaleReady / Catalog, Admission, Inventory | sale_id | flash-sale-readiness-v1 |
| `payment.requests.v1` | PaymentRequested, RefundRequested / Order | order_id | payment-commands-v1 |
| `payment.events.v1` | PaymentSucceeded, PaymentFailed, RefundCompleted / Payment | order_id | order-payment-v1; notification-outcomes-v1 for refund |
| `inventory.events.v1` | ReservationHeld/Confirmed/Released/Expired / Inventory | order_id | order-inventory-v1; notification-outcomes-v1 for expiry |
| `order.events.v1` | OrderPending/Confirmed/Failed/Expired, RefundPending / Order | order_id | notification-outcomes-v1 |
| `analytics.activity.v1` | Analytics copies of domain/attempt events; AnalyticsHeartbeat / Analytics clock emitter | sale_id:product_id:window_start | analytics-input-v1 (Goka) |
| Goka group table for `analytics-input-v1` | AnalyticsWindowState / Goka processor | same window key and partition count | Analytics state recovery / View / result relay |
| `analytics.results.v1` | WindowSummaryUpdated / Analytics | sale_id:product_id:window_start | flash-sale-results-v1 |
| `analytics.late.v1` | LateActivityObserved / Analytics | original_event_id | flash-sale-late-v1 |
| `<consumer>.retry.v1` / `<consumer>.dlq.v1` | Original envelope plus failure metadata / affected consumer | original business key | that consumer's retry/repair worker |

Notification uses its own group for each subscribed topic mapping; the same group label identifies
the logical subscriber, while each Kafka topic/partition offset remains independent. Replicas of
one logical subscriber share its group. Analytics consumes only the analytics copies, preventing
double counting from simultaneously reading the original domain topic.

Every envelope requires event_id, schema_version, type, occurred_at, producer, aggregate_id,
aggregate_version, correlation_id and a type-specific data object. Domain occurrence time is the
owner's commit/decision time; aggregate version is monotonic per owner/entity. Topic key and
data IDs must agree. Sale kinds use sale ID as aggregate; reservation kinds use reservation ID;
order/payment/refund kinds use their corresponding owned entity; heartbeats use a stable window
UUID and increasing clock-record version. Topic copies retain the original domain producer and
event ID. AnalyticsHeartbeat is produced only by Analytics. Enforce producer/topic/type and
key/partition combinations in contract tests.

## Delivery, retries and notification invocation

- Outbox entries track domain and analytics destinations separately. Use acks=all/idempotent
  producers; acknowledge consumer offsets only after local inbox/business/outbox commit. Do not
  claim transport exactly-once for SQL or simulated payment side effects.
- Individual transient failures use five delayed retries at 1/2/4/8/16 seconds, then an inspectable
  dead-letter record. Dependency-wide outages pause intake and retain durable intents; they do
  not become thousands of permanently rejected purchases. A malformed payload is quarantined
  with original bytes/topic/partition/offset and error; unrelated keys continue processing.
- Application Kafka retry workers persist original identity, retry schedule and dispatch intent
  before acknowledging. Replay preserves event identity; corrected payloads are separately
  audited. Operator replay requires an authorized identity and a reason. Reconciliation continues
  to own accepted orders and refund obligations even when a delivery has been quarantined.
- The serverless Notification owner supplies event invocation, finite retry/quarantine and replay
  configuration that meets the shared delivery targets. No function provider, language, database
  or Kafka client is mandated here. Preserve original bytes, identity and failure context so a
  retry cannot produce another logical notice or lose an accepted outcome.
- The gateway uses the owner's authenticated query adapter with verified subject, limit and
  cursor. The return payload is the public notice-page shape. Only the trusted gateway/service
  identity may supply a subject; a public caller cannot forge another shopper's identity. The
  local internal REST contract is the adapter behavior, not a requirement to run a permanent
  HTTP server as the cloud Notification implementation.
- Canonical notice key is `(shopper_id, order_id, outcome)`. ReservationExpired and OrderExpired
  use the same EXPIRED outcome; refunds have distinct pending/completed outcomes. No ordinary
  shopper endpoint accepts payment simulation modes, refund commands or transport offsets.

## Analytics state and result contract

- UTC minute windows are `[start,end)`. Every analytics input key includes sale ID, product ID and
  occurrence-time window start. The input and Goka group table have six matching partitions and
  the same hash. Your Sarama partitioner must match Goka's hasher; test golden keys against both.
- AttemptRecorded is produced only for authenticated SHOPPER submissions with valid Purchase
  and required-header structure and a known matching sale/product. Known inactive/cancelled/not-ready
  targets and otherwise valid business rejections count; explicit retries get new attempt IDs.
  Unauthorized/malformed/unknown/mismatched requests produce only bounded operational exclusion
  counters at the first rejecting boundary; no synthetic identity or AttemptRecorded is allowed.
- `decision`/`reason` are Admission's decision. After successful admission, an Order stock/cap
  rejection remains ADMITTED/NONE; `response_status` and `response_error_code` retain the final
  Error outcome. Admission rejection rate uses eligible rejected decisions / eligible decisions,
  not all non-2xx responses. Duplicate transport preserves attempt/event identities.
- AttemptRecorded alone contributes attempts/admission/response latency; PaymentSucceeded/Failed
  contributes payment outcomes; OrderConfirmed contributes sold units/revenue. Exact seen-ID sets
  prevent duplicate contribution. Histogram bounds are 50/100/250/500/1000/2000/5000ms plus overflow;
  counts are noncumulative. A result revision replaces, rather than adds to, earlier totals.
- Analytics uses Goka's persisted group state; the wire state format is versioned separately from
  public results. Local caches rebuild from the compacted table. Readiness waits for processor/View
  recovery. Eight-day seen/finalization retention exceeds the seven-day raw replay interval.
- The clock emitter queries `/internal/analytics/windows` in Flash Sale and appends heartbeat
  records per unfinished scheduled window once per second. It also ticks nonfinal keys from its
  recovered View outside scheduled sale intervals, including eligible inactive-sale attempts.
  Heartbeats initialize empty windows and share data partitioning. Arrival is the input record's broker LogAppendTime, not replay wall
  clock. At/after end+60s, new domain activity is late; an ordered heartbeat at/after that instant
  persists an explicit FINAL revision. This prevents finalization from overtaking earlier backlog.
- Persist aggregate, seen IDs, summary revision/final flag and late-output identities together.
  A Goka View observes durable group records; the results relay emits only these stored outputs
  through Sarama, retries failures and requeues from the View after restart. It also periodically
  rescans entries so an acknowledged input followed by a lost result still recovers. Result/late
  event IDs are deterministic for their stored revision/original event. Goka at-least-once delivery
  is expected; do not claim a Flink checkpoint or transactional exactly-once output guarantee.
- A one-second clock and relay cadence leaves three seconds for processing and projection under
  the five-second final-visibility target. Higher result revisions alone update Flash Sale's
  projection; FINAL cannot regress to provisional. Late records update a separate deduplicated
  late count and never alter finalized financial totals. Report delayed when expected active
  clock/relay work lags, not merely because a historical FINAL row is old.
- Sarama clients for your services use `Producer.RequiredAcks=WaitForAll`,
  `Producer.Idempotent=true`, `Net.MaxOpenRequests=1` and return producer successes/errors for
  checked delivery. Apply the corresponding configuration through Goka's documented builders.
  Consumer offsets follow durable application work; your pinned Sarama/Goka integration tests
  verify the exact configuration. Other members choose compatible client libraries themselves.

## Leaderless metadata protocol

- Coordinator endpoints are internal to Catalog. Query at least two fixed replicas; only durable
  local commit counts toward W=2. Read results combine nondominated siblings from R=2 responses
  and initiate read repair. Quorum failure returns 503 QUORUM_UNAVAILABLE, never an empty product.
- FOUND has one sibling; CONFLICT has two or more; NOT_FOUND has none and requires two explicit
  absent responses. Internal read can return NOT_FOUND as a 200 quorum result; Catalog maps that
  to public 404. A conflict is 409 for ordinary readers and inspectable via the operator endpoint.
- PUT carries operation ID, observed vector-clock context and value. An uncertain partial write
  is not rolled back. Query quorum receipts before retry through another node; if no committed
  receipt is discoverable, a retry can expose a sibling rather than claim linearizable exactly-once
  metadata writes. The original operation ID and payload remain inspectable.
- Manual resolution merges all observed contexts then creates a successor. A concurrent unseen
  edit remains a sibling; a timestamp must never silently choose a winner. Merge repairs union
  version sets, preserve incomparable versions/receipts, and reject a repeated operation ID with
  a different payload hash. Each node's counter, assigned versions and receipts are durable.

## Compatibility and review gate

### Draft revision 2 adoption

This repository has planning files and no released runtime. `1.0.0-draft.2` amends the prior draft
before its initial release; `/v1`, protobuf v1 and `.v1` topics remain the intended first release.
Required canonical cap fields, the cap-aware readiness digest and new conflict results change
semantics and are **breaking relative to the previous draft**. They are not a compatible rollout.

1. Mukthish and affected owners review the same revised schemas, examples, cap/expiry rules and
   digest input. No teammate approval is implied. Refresh only Mukthish's task backlog next.
2. Regenerate all producer/consumer bindings and fixture projections together before integration.
   Creation/replacement input omitted cap normalizes to one; canonical offers/events missing it
   are invalid and must not activate a sale. Never interpret a missing cap as unlimited.
3. Seed clean disposable fixtures or migrate a prior draft dataset by assigning cap=1 and
   incrementing the configuration version/readiness round before activation. If historic held+
   confirmed totals already exceed the proposed cap, stop for explicit reconciliation; do not
   delete accepted work or silently raise/default the cap to make it pass.
4. Require overlap/cap/expiry/mixed-traffic real-adapter checks and regenerated-client drift checks
   before declaring the draft adopted. A rollback cannot restore cap-unaware reservation behavior
   once new purchases use this contract.
5. If an owner has already released/deployed the prior draft, stop in-place adoption and publish a
   reviewed major `/v2` / protobuf-package v2 / `.v2` event/topic contract with explicit consumer
   migration and rollback. The planning-only status here is not evidence about an external deployment.

### Released contract evolution

Additive optional fields are compatible; semantic changes, required-field changes, removals and
enum reinterpretation need a new contract major/topic version and consumer migration. Protobuf
field numbers are never reused; removed numbers/names are reserved. Consumers validate known
major versions and ignore harmless optional additions. Examples and generated-client drift checks
run with every contract change. Reviews are still required before implementation/merge; no teammate
approval is implied by this generated design.
