# Data Model: Flash Sale Platform

**Date**: 2026-10-25 | **Authority**: [spec.md](spec.md), [shared architecture](../../docs/architecture.md)
Contracts under [contract index](contracts/README.md) define wire shapes. This document defines ownership,
validation, uniqueness, and transitions; it contains no migrations or service implementation.

## Common value rules

- IDs are UUID strings, except fixed replica IDs `node-a`, `node-b`, `node-c` and named service IDs.
- All persisted/exchanged timestamps are UTC RFC 3339; your PostgreSQL databases use
  `timestamptz`. Inventory's authoritative clock is sampled inside its protected transition; its
  owner chooses the transactional engine and corresponding locking/time mechanism.
- Money is an integer minor-unit value with an uppercase three-letter currency. JSON amounts and
  totals must be between 0 and 9,007,199,254,740,991 to remain exactly representable by the frontend.
  Sale unit prices are positive. Quantity is an integer from 1 to 100 in this release; total is
  checked for overflow before multiplication. No cross-currency addition is permitted.
- Request keys are 1-128 printable ASCII characters. Canonical payload hashing includes shopper,
  sale, product, quantity, expected offer version, unit price, and currency. Keys are retained
  with the demonstration dataset until an explicitly requested reset; retries never create new IDs.
- `schema_version=1`; additive compatible fields are optional. Unknown event kinds/major schema
  versions go to quarantine, not a best-effort interpretation. Consumers tolerate optional additions.

## Ownership and persistence

| Owner | Persistent records | Access boundary |
| --- | --- | --- |
| Flash Sale Manager | SaleConfiguration, readiness, operator analytics projection | Its own PostgreSQL database |
| Catalog | Applied offer projection, product index, readiness | Owner-chosen store; metadata through storage protocol |
| Admission | Applied sale projection, quotas, leases, attempts, outbox | Own PostgreSQL database shared across its replicas |
| Inventory | StockAllocation, Reservation, inbox/outbox | Member 2's transactional store |
| Order | PurchaseIntent, Order, saga steps, inbox/outbox | Own PostgreSQL database |
| Payment | PaymentAttempt, Refund, inbox/outbox | Member 2's transactional store |
| Notification | Notice, delivery state, inbox | Member 3's store and serverless handler |
| Analytics | Window state, dedup sets, offsets, output identities/revisions | Goka group table, recoverable View and Kafka results |
| Each metadata node | MetadataVersion, operation receipts, local counter | Member 3's independently durable store/volume |

The three lead-owned transactional databases use PostgreSQL; physical storage choices for the
other owners are delegated. No service credential can write another owner's records. Flash Sale
consumes summary events into its own projection; it never queries Analytics state directly. The
gateway uses service interfaces, including an authenticated Notification query adapter, and has
no application-database credentials. Assertions use owner-supplied read-only verification adapters.

Fields below are logical contract requirements for teammate-owned entities, not their physical
schema, framework, language or engine selection. Owners document equivalent persistence choices.

## Participant and authorization

`Participant { participant_id, role: SHOPPER|OPERATOR, display_name }` is a seeded fixture identity.
Tokens contain subject, role, issuer, audience, issued/expiry times and a fixed signing algorithm.
Use 15-minute JWTs signed with a generated RSA key; services validate public keys and claims.
Only a local/operator seed tool has the signing key. The frontend holds the token in memory.
Internal calls use mTLS service identities; forwarded subject/role claims are accepted only from
an authorized calling service and never from arbitrary public headers. Shopper queries filter by
subject in the owning service. There is no public token-minting endpoint.

## SaleConfiguration and readiness

`SaleConfiguration { sale_id, product_id, version, unit_price_minor, currency, allocated_units,
starts_at, ends_at, configured_by, configured_at, cancelled_at?, frozen_at? }`.
Readiness records are unique by `(sale_id, version, participant)` where participant is Catalog,
Admission, or Inventory. Each records applied version, allocation digest, time, and status/error.

- Validate positive allocation, quantity-compatible bounds, price/currency, and `starts_at < ends_at`.
- Before start, a replacement configuration increments version and invalidates prior readiness.
- Readiness includes Catalog's applied offer and metadata quorum availability, Admission's applied
  limits/window, and Inventory's durable allocation. Duplicate/old acknowledgements are harmless.
- Effective status is CANCELLED if cancelled; otherwise UPCOMING before start, UNAVAILABLE when
  readiness is incomplete, ACTIVE when ready and inside the window, and ENDED at/after end.
- Flash Sale persists SaleActivated after all matching readiness acknowledgements and the start
  instant; Catalog, Admission and Inventory apply the activation for the same version. Every
  participant still checks the end time, so a delayed activation cannot reopen an ended sale.
- Once the original start time is reached, price/allocation/window replacement is rejected for
  that sale identity, preventing an operator from moving the start time to bypass the freeze.
- Cancellation is permitted only before start. Existing holds settle against their frozen snapshot
  after end, up to their individual expiry; new holds must still pass the current window check.

The trusted offer contains `(sale_id, product_id, version, unit_price_minor, currency, starts_at,
ends_at)`. Order compares the submitted expected offer with Flash Sale's authoritative offer;
a mismatch returns OFFER_CHANGED with the new offer, before reserving or accepting an order.

## AdmissionQuota, InFlightLease, and Attempt

`AdmissionQuota { scope: SALE|SHOPPER_SALE, scope_id, bucket_start, count, limit, interval_ms }`;
`InFlightLease { attempt_id, sale_id, shopper_id, expires_at, released_at? }`;
`Attempt { attempt_id, shopper_id, sale_id, product_id, received_at, decision, reason?,
retry_after_seconds?, response_status, response_latency_ms, request_key_digest }`.

Initial normal profile: 5 attempts/shopper/second, 100 attempts/sale/second, and 50 concurrent
purchase forwards per sale, with two-second lease lifetime and a shorter upstream timeout.
The scaling profile raises these limits explicitly in versioned sale configuration before start.
Acquire quota rows in deterministic order and claim a lease transactionally; process all
replicas against these records. Expired leases are excluded under the same lock, so a crashed
worker cannot leak capacity. Release on completion; repeat release is safe. DB unavailability
fails closed with retryable 503, not an invented successful admission decision.

Each HTTP submission has a server-assigned `attempt_id`; an explicit shopper retry is a new
attempt while retaining the purchase request key. Internal transport retries preserve attempt ID.
Record the measured response-decision latency and its outbox event before writing the response;
network transfer is excluded from that metric and measured separately by the load generator.
An interrupted response still has one durable attempted outcome, and never invents another sale.

## StockAllocation and Reservation

`StockAllocation { sale_id, product_id, config_version, allocated, available, held, sold, version }`;
`Reservation { reservation_id, order_id, shopper_id, sale_id, product_id, quantity,
config_version, state, created_at, expires_at, finalized_at?, transition_version }`.

One allocation per sale/product; one reservation per proposed `order_id`. Reserve, stock movement,
reservation creation, and required events commit in one Inventory transaction. Conditional
availability and sale-version/window validation occur inside the transaction. The invariant
`allocated = available + held + sold` holds at each commit; every quantity is nonnegative.

| Current | Operation / condition | Next | Quantity effect |
| --- | --- | --- | --- |
| Absent | Reserve; eligible and enough stock | HELD | available -= q; held += q |
| HELD | Confirm; locked DB time < expires_at | CONFIRMED | held -= q; sold += q |
| HELD | Release before expiry | RELEASED | held -= q; available += q |
| HELD | Expire or any transition at/after expiry | EXPIRED | held -= q; available += q |
| Terminal | Repeat matching operation | Same | None |
| Terminal | Competing terminal operation | Same, conflict result | None |

A confirmation at expiry returns EXPIRED and performs expiry atomically if cleanup has not run.
A release at expiry also resolves to EXPIRED. The expiry worker scans every second with bounded
batches and claims rows safely; it never depends on Order or shopper traffic. Repeating Reserve
with the same order ID returns the same terminal/held record; a payload mismatch is a conflict.

## PurchaseIntent, Order, and saga progress

`PurchaseIntent { shopper_id, request_key, payload_hash, order_id, original_attempt_id,
state: RECEIVED|RESERVING|ACCEPTED|REJECTED, reservation_id?, rejection_code?, response_snapshot?,
lease_owner?, lease_until?, created_at, updated_at }`, unique `(shopper_id, request_key)`.

`Order { order_id, shopper_id, sale_id, product_id, reservation_id, quantity, offer_version,
unit_price_minor, currency, total_minor, expires_at, public_state, workflow_step,
payment_attempt_id, refund_id?, created_at, updated_at, aggregate_version }`.

- Unique request ownership is claimed durably before calling Inventory; the proposed order ID is
  also its stable reservation-operation key. Same-payload concurrent calls share that intent.
- An unaccepted intent returns retryable REQUEST_IN_PROGRESS rather than exposing an accepted
  pending order. Once hold and order are durable, persist PENDING_PAYMENT and PaymentRequested
  in one Order transaction, then return 202 with the stable order. Normal business rejections
  are recorded; admission 429/temporary 503 are not permanent purchase rejections.
- Public order states are PENDING_PAYMENT, CONFIRMED, FAILED, EXPIRED. Internal workflow steps
  distinguish awaiting payment, confirming, releasing, and reconciling without implying fulfillment.
- Success first reads/attempts Inventory confirmation, then marks CONFIRMED and emits its outcome.
  Lost confirmation responses cause a state query and repeat-safe continuation, not immediate refund.
- Payment failure releases the hold; RELEASED -> FAILED and EXPIRED -> EXPIRED. If Inventory is
  unreachable, keep the accepted order pending/reconciling until the authoritative state is known.
- Expired/released holds can never reopen. A successful charge then creates a unique refund
  obligation while keeping EXPIRED/FAILED. Persist RefundRequested and the canonical RefundPending
  notice event in the same local transaction. Refund progress is separate from terminal order state.
- A one-second reconciliation loop resumes leased incomplete intents and steps; stale workers
  cannot overwrite a later aggregate version. Orphan holds expire even if reconciliation fails.

| Interrupted boundary | Recovery rule |
| --- | --- |
| Intent recorded, no known reservation | Query by order ID; retry Reserve with same ID |
| Reservation succeeded, response lost | Retrieve existing reservation; never generate another order ID |
| Hold exists, order absent | Create pending order/payment intent only if hold is live; else record rejection |
| Order committed, payment dispatch absent | Its durable outbox publishes the same PaymentRequested |
| Payment persisted, result not published | Payment outbox replays the stable recorded result |
| Inventory confirmed, Order not updated | Reconciliation sees CONFIRMED and finalizes the same Order |
| Expiry/release beats success | Terminal nonfulfilled Order plus unique refund obligation |
| Refund completed, result not delivered | Replay RefundCompleted; no second refund effect |

## PaymentAttempt and Refund

`PaymentAttempt { attempt_id, order_id, reservation_id, amount_minor, currency,
mode: SUCCESS|FAILURE|DELAYED_SUCCESS|DELAYED_FAILURE, due_at, state: SCHEDULED|SUCCEEDED|FAILED,
result_code?, completed_at?, version }`, unique `(order_id, attempt_id)`.
The simulator chooses/records its mode once and does not rerandomize on redelivery. Test modes
are accepted only through operator fixture controls, not ordinary shopper purchase bodies.
A delayed attempt is a durable scheduled row, never a sleeping consumer holding a partition.

`Refund { refund_id, order_id, payment_attempt_id, amount_minor, currency,
state: PENDING|COMPLETED|REPAIR_REQUIRED, retry_count, last_error?, completed_at? }`, unique
`payment_attempt_id`. Order's RefundRequested and Payment's RefundCompleted preserve that ID.
A terminal failed payment has no refundable charge. Contradictory fabricated results are
quarantined against the recorded payment-attempt version; they cannot change recorded reality.

## Inbox, Outbox, and failed work

`Inbox { consumer_name, event_id, processed_at, payload_hash }` has a unique composite key;
`Outbox { event_id, aggregate_id, aggregate_version, envelope, created_at }`;
`OutboxDelivery { event_id, destination, state, attempts, next_attempt_at, published_at? }`.

Inbox insert, domain change, and outgoing intents share the consumer's local transaction.
Offset acknowledgement follows commit. An outbox record has separate destination receipts for
domain delivery and `analytics.activity.v1`; retain until both acknowledge. Republishing retains
event ID. Use ordered per-aggregate sequence dispatch where required, but consumers must still
handle cross-topic reordering. Receipt retention is at least the dataset's replay lifetime.

`FailedWork { consumer, original_event_id, original_topic, partition, offset, original_payload,
error_code, attempts, first_seen_at, last_seen_at, replay_audit }` is inspectable and not a
silent discard. Replays retain the business/event identity; repaired payloads receive an audited
new event referencing the rejected one. Business-critical reconciliation scans durable intents
in addition to event consumption, so quarantine cannot erase a refund or accepted-order obligation.

## Notification and delivery

`Notice { notice_id, shopper_id, order_id, reservation_id?, outcome, message, delivery_state,
created_at, delivered_at?, last_error? }` is unique on `(shopper_id, order_id, outcome)`.
Outcome is CONFIRMED, FAILED, EXPIRED, REFUND_PENDING, or REFUND_COMPLETED. Inventory expiry and
Order expiry map to the same order ID/outcome, including an abandoned proposed order ID; neither
produces duplicate expiry notices. Process inbox+notice atomically. Notification queries are
subject-filtered and cursor-paginated. Failed delivery retains its status for replay.

## Analytics state and read projection

Goka keys are `(sale_id, product_id, window_start)` with UTC epoch-aligned one-minute windows.
The input and compacted group topic have six partitions and a shared hash. Domain occurrence time
selects the window; input Kafka LogAppendTime is the stable ingestion time. A configured ingestion
cutoff is `window_end + 60 seconds`; input at/after that cutoff is late. This timestamp must remain
unchanged on Kafka replay. Forwarded/replayed application records preserve the original ingestion
metadata in audited replay headers instead of pretending a retry is new timely business activity.
A finalized-window guard still wins over a delayed redrive: unseen repaired records become
inspectable late activity; a repeat of an already-counted record remains a duplicate.

`AnalyticsWindowState { state_schema_version, sale_id, product_id, window_start, window_end,
seen_event_ids, seen_attempt_ids, seen_order_ids, distinct_shopper_ids, summary, finalized,
late_outputs, last_input_offset }`. Keep exact bounded sets for the demonstration; retain dedup
and finalization state at least eight days. Test maximum serialized state size against the configured
broker/group-topic record bound. Controlled compaction/cleanup cannot reopen an old finalized key.

`WindowSummary { summary_id, sale_id, product_id, window_start, window_end, revision,
status: PROVISIONAL|FINAL, attempts, distinct_shoppers, admitted, rejected, rejection_counts,
payment_successes, payment_failures, confirmed_units, revenue_by_currency, latency_buckets_ms,
latency_count, computed_at }`. Histogram upper bounds are `[50,100,250,500,1000,2000,5000]` ms
plus overflow; bins are noncumulative counts. Percentiles identify their bin precision. A summary
ID is stable per window; event identity is deterministic for `(summary_id, revision)`.

Only AttemptRecorded contributes attempts/admission/latency; only PaymentSucceeded/Failed
contributes payment outcomes; only OrderConfirmed contributes sold units and revenue. Seen
business IDs protect against semantic duplicates even when a transport event identity changes.
Currency totals remain separate. Finalized financial totals cannot be changed by late activity.

Your Analytics clock emitter queries Flash Sale's internal window-schedule endpoint, using its
recovered View to skip already-final windows. It appends an AnalyticsHeartbeat for each open or
not-yet-finalized known sale/product/window every second, including windows with no business
activity. Heartbeats use the same key/partitioner as data. Processing a heartbeat whose broker
append timestamp reaches the cutoff persists FINAL, after all earlier records in that partition.
Do not finalize based on an unrecorded process-local timer during replay. Under a dependency outage,
clock emission/processing lag is visible as delayed reporting, never fresh zero activity.

The processor persists state, seen IDs, summary revision and late-output identities in the compacted
Goka group table. It does not publish result output before state is durable. A Goka View restores
those persisted updates and a Sarama relay emits their stored summary/late payloads. The relay
rescans/requeues state on startup and every second to repair the state-persisted/output-lost gap.
Repeats retain output identity/revision; intermediate provisional revisions may be superseded, but
the latest/final revision remains publishable. Kafka offsets cannot advance ahead of Goka's durable
state update. Kill/rebalance tests must check all state/output/offset orderings explicitly.

`AnalyticsProjection` belongs to Flash Sale and upserts only higher revisions; a FINAL row cannot
be replaced by a provisional row. `LateActivity { original_event_id, original_window, reason,
received_at }` is unique on original event ID and appears alongside finalized totals as a separate
late count. Local caches/View rebuild from Kafka, with readiness withheld until caught up. Mark
read views delayed if tick/relay/projection freshness exceeds five seconds. Known empty windows
emit explicit zero summaries and show zero activity instead of division-by-zero rejection rates.

## Metadata replica state

`MetadataVersion { product_id, version_id, operation_id, context: {node_id: uint64},
value: {name, description, image_refs}, created_at }`;
`NodeCounter { node_id, monotonic_counter }`;
`OperationReceipt { operation_id, request_hash, assigned_version_id, result_context }`.

Each node commits version, receipt, and next counter atomically and durably using Member 3's
chosen storage engine. Node identity and counter survive restart. A version dominates another only when
all vector components are at least as high and one is strictly higher; incomparable versions
remain siblings. Never compare by wall-clock timestamp to discard a concurrent value.

A coordinator unions a client's supplied context, increments its own durable counter, and
replicates that version. A retry with the same operation ID reuses the assigned version. Retrying
through another coordinator first queries quorum receipts; if coordination changed before any
receipt was durable, a new concurrent sibling may be produced and MUST be exposed, not hidden.
A quorum-timeout write may have persisted on one node; it is uncertain, not rolled back. Once a
write is acknowledged on two nodes it must remain represented until causally superseded.

A read queries two nodes, unions nondominated siblings/receipts, and repairs stale nodes. Manual
resolution supplies every observed sibling context; a newly concurrent edit remains a conflict
rather than being silently erased. Background reconciliation visits all fixed members every five
seconds. Quorum responses report UNAVAILABLE below two responses, CONFLICT above one sibling,
FOUND with one value, and NOT_FOUND only after two responses prove absence. Each replica has its
own encrypted volume; sharing a filesystem or using one-node memory copies cannot pass the demo.
