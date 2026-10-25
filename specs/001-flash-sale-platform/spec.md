# Feature Specification: Flash Sale Platform

**Feature Branch**: Contributor implementation branches; Mukthish: `001-mukthish-implementation`.

**Feature Directory**: `specs/001-flash-sale-platform`

**Created**: 2026-10-25

**Status**: Draft

**Input**: The agreed Flashkart architecture, project requirements and subsequent ownership/stack amendments.

**Source**: [Shared architecture and decisions](../../docs/architecture.md). Later recorded amendments
supersede earlier suggestions. This specification covers one integrated release across all eight
services and follows [Constitution v1.0.0](../../.specify/memory/constitution.md).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Publish a sale and browse its products (Priority: P1)

An authorized sale operator configures a scheduled sale. Shoppers can browse the product,
price, and sale window and distinguish an upcoming, active, ended, or unavailable sale.

**Why this priority**: Shoppers need a trustworthy offer, and every purchase depends on the same
sale configuration and stock allocation being ready.

**Independent Test**: Publish a seeded product and sale with controlled time and downstream
readiness responses. Verify the displayed offer and whether purchases are permitted.

**Acceptance Scenarios**:

1. **Given** a valid product and an authorized operator, **When** the operator submits a sale with
   a price, currency, allocation, and start/end times, **Then** the sale records a configuration
   version and remains unavailable for purchase until all required participants are ready.
2. **Given** one required participant has not applied that version, **When** the start time
   arrives, **Then** the sale remains unavailable and the operator can identify the missing
   readiness confirmation; a delayed obsolete confirmation cannot activate a newer version.
3. **Given** every participant is ready, **When** time reaches the start and then the end,
   **Then** shoppers can attempt purchases only during the start-inclusive, end-exclusive window.
4. **Given** an active sale, **When** an operator attempts to change its price or allocation,
   **Then** the change is rejected and existing offers and orders retain their agreed values.
5. **Given** invalid sale values or an unauthorized visitor, **When** sale configuration is
   attempted, **Then** no configuration is accepted; cancelling a not-yet-started sale prevents
   subsequent activation.

---

### User Story 2 - Purchase scarce stock without overselling (Priority: P1)

A shopper requests a purchase, receives either a clear rejection or a pending order, and can
check the order until it has a final outcome.

**Why this priority**: Correctly selling a limited allocation is the central business outcome.

**Independent Test**: Seed an active sale with 100 units and a controllable payment simulator.
Submit competing and repeated requests, then reconcile orders, reservations, and available stock.

**Acceptance Scenarios**:

1. **Given** an active sale and available capacity, **When** a shopper requests a valid quantity,
   **Then** stock is reserved before a pending order is accepted, the response identifies that
   order and its expiry, and the order records the authoritative price and currency.
2. **Given** more demand than stock, **When** concurrent purchases compete for the final units,
   **Then** held plus confirmed sold units never exceed allocation and unsuccessful shoppers see
   sold-out or insufficient-stock outcomes without a payment being requested.
3. **Given** an already submitted purchase, **When** the same shopper repeats its request key and
   payload concurrently or after a timeout, **Then** the same logical order or recorded purchase
   rejection is returned without another reservation or charge. Reusing that key with different
   purchase details is rejected.
4. **Given** a shopper or sale has reached its configured admission limit, **When** another
   attempt arrives through any running instance, **Then** it is rejected with a reason and retry
   delay before a new reservation is created. Admission approval alone does not promise stock.
5. **Given** a pending order and a successful simulated payment before reservation expiry,
   **When** reservation confirmation succeeds, **Then** the order becomes confirmed exactly once
   and stock moves from held to sold. The shopper can retrieve that outcome after a restart.
6. **Given** a shopper viewing order status, **When** another shopper's order is requested,
   **Then** its details are not disclosed. Invalid quantities and inactive sales are rejected
   without a reservation or payment.

---

### User Story 3 - Resolve failures, expiry, and late payment (Priority: P1)

A shopper receives a consistent outcome even when payment fails, processing is interrupted, or
a reservation expires. Unfulfilled payments are visibly compensated.

**Why this priority**: Failure must neither lose stock permanently nor charge a shopper for an
order that cannot be fulfilled.

**Independent Test**: Use a controlled clock and simulated payment outcomes. Interrupt each
purchase stage and replay outcomes in different orders while inspecting final balances.

**Acceptance Scenarios**:

1. **Given** a held reservation, **When** its payment fails before expiry, **Then** the order
   becomes failed and its units become available exactly once.
2. **Given** an unpaid reservation, including one whose order was never durably created,
   **When** its expiry passes, **Then** the reservation expires and its units return to stock;
   an associated pending order eventually becomes expired.
3. **Given** confirmation and expiry or release compete for one reservation, **When** both are
   attempted concurrently, **Then** exactly one terminal reservation state wins. At or after
   the expiry instant, an unconfirmed hold cannot be confirmed, even if expiry cleanup is late.
4. **Given** a released or expired reservation, **When** a successful payment result arrives,
   **Then** the order is not confirmed, the units remain available or sold to another shopper,
   and one simulated refund progresses from pending to completed without duplicate refunds.
5. **Given** a recorded business change whose next action was interrupted, **When** processing
   restarts, **Then** the required work resumes. Repeated or out-of-order results cannot change
   a terminal outcome, double-charge, double-release, or recreate a completed notification.
6. **Given** an outcome that repeatedly cannot be processed, **When** the configured retry
   budget is exhausted, **Then** it is isolated for operator inspection and replay without
   blocking unrelated purchases or silently disappearing.

---

### User Story 4 - See purchase outcome notifications (Priority: P2)

A shopper can see a recorded confirmation, payment-failure, or reservation-expiry notification
and any subsequent refund status without waiting for notification delivery during checkout.

**Why this priority**: Clear follow-up explains what happened while keeping checkout responsive.

**Independent Test**: Supply seeded order and reservation outcomes without running a live sale.
Observe the shopper's notification view, duplicates, and recovery after delivery failure.

**Acceptance Scenarios**:

1. **Given** a purchase outcome, **When** notification processing runs, **Then** the affected
   shopper sees one logical notice identifying the order or reservation and its outcome.
2. **Given** duplicate outcomes or a delivery retry, **When** they are processed, **Then** no
   duplicate logical notice appears and delivery status remains inspectable.
3. **Given** unavailable notification processing, **When** checkout and payment complete,
   **Then** their outcomes remain valid and the notice is delivered after recovery; a different
   shopper cannot retrieve the notice.

---

### User Story 5 - Understand demand and sales as they happen (Priority: P2)

A sale operator views time-windowed demand, admission rejections, payment outcomes, stock
consumption, and purchase latency for each sale and product.

**Why this priority**: Operators need to distinguish real demand, overload, failures, and sales,
including during the scalability demonstration.

**Independent Test**: Replay a known set of timestamped sale activity with duplicates, late
arrivals, and a processing restart. Compare the resulting summaries with hand-calculated totals.

**Acceptance Scenarios**:

1. **Given** known activity for multiple products and sales, **When** a reporting window closes,
   **Then** each summary has correct attempt counts, distinct shoppers, admission rejections and
   reasons, payment outcomes, confirmed units, confirmed revenue by currency, and purchase latency.
2. **Given** a duplicate activity record or restart, **When** summaries are recomputed,
   **Then** one logical activity contributes only once and completed windows remain recoverable.
3. **Given** out-of-order activity arriving within the allowed lateness, **When** it is received,
   **Then** its original window is corrected. Later activity is counted as late and made
   inspectable rather than silently changing a finalized summary or being silently discarded.
4. **Given** an operator reading analytics, **When** purchase processing is busy or reporting is
   temporarily unavailable, **Then** reporting does not hold up purchases and the operator can
   distinguish provisional, finalized, and delayed results.

---

### User Story 6 - Browse replicated catalog metadata through failures (Priority: P2)

Shoppers can browse product metadata while one storage replica is unavailable. An operator can
show that requests work without a permanent leader and that conflicts and stale copies recover.

**Why this priority**: This demonstrates the assigned leaderless replication concept without
weakening purchase correctness.

**Independent Test**: Use three independently persisted copies and a dataset of 100 products.
Send requests through each replica, stop and restart replicas, and create concurrent edits.

**Acceptance Scenarios**:

1. **Given** three healthy replicas, **When** any replica coordinates a metadata request,
   **Then** a successful write is durable on at least two replicas and a successful read obtains
   responses from at least two; no fixed primary is required.
2. **Given** one replica is unavailable, **When** shoppers read or operators update metadata,
   **Then** the remaining two support those operations and the unavailable replica is reported.
3. **Given** concurrent edits without a shared causal successor, **When** their versions meet,
   **Then** both are preserved and identified as conflicting; ordinary product display reports
   that metadata is temporarily unavailable until an authorized operator resolves the conflict.
4. **Given** a resolved conflict or an offline replica that missed updates, **When** all replicas
   communicate again, **Then** they converge on the resolved metadata, including after a durable
   restart, without changing authoritative sale prices, orders, or available stock.
5. **Given** fewer than two reachable replicas, **When** a read or write is attempted,
   **Then** it reports temporary unavailability rather than product absence or successful storage.

---

### User Story 7 - Operate, scale, and demonstrate the complete system (Priority: P2)

The three team members can reproduce the environment, operate all eight services, demonstrate
scaling under sustained demand, and explain their individual responsibilities using evidence.

**Why this priority**: Reproducible deployment and observable behavior are project deliverables,
including the assigned cloud and distributed-systems demonstrations.

**Independent Test**: Start from the documented environment definition, apply a reviewed release,
run the declared workload, inspect operational evidence, and perform recovery demonstrations.

**Acceptance Scenarios**:

1. **Given** access to the chosen cloud environment, **When** the documented provisioning and
   release process is followed, **Then** the public storefront and purchase/status capabilities
   are reachable, all eight services run in their required execution models, and internal stock
   and payment operations cannot be invoked through public routes.
2. **Given** sustained purchase load and configured scaling thresholds, **When** demand exceeds
   the initial capacity, **Then** both admission and order processing add running instances;
   shared admission limits and stock invariants continue to hold.
3. **Given** an intentionally induced failure, **When** the team inspects operational views,
   **Then** it can correlate the affected purchase across processing stages and see errors,
   latency, queued-work age, resource usage, and replica health, including every execution model.
4. **Given** a reviewed release followed by rollback, **When** the declared deployment state is
   reconciled, **Then** the environment reports the intended revision and existing business data
   remains intact; service updates do not depend on unrecorded manual deployment steps.
5. **Given** the final demonstration, **When** ownership and requirement evidence are reviewed,
   **Then** all eight services and assignment categories (a)-(h) have named leads, all three
   members can demonstrate their contributions, and three distinct storage products have
   observable application uses.

### Edge Cases

- A sale's readiness acknowledgements refer to different configuration versions or arrive after
  cancellation; they cannot activate it.
- A request starts before the sale ends but reaches reservation afterward; reservation time
  determines eligibility. Existing valid holds can settle after the sale ends until they expire.
- Catalog metadata is stale or conflicting; checkout uses the authoritative sale version and
  displays its price for shopper confirmation before submitting a purchase.
- One purchase requests more units than remain, or supplies zero, negative, fractional, or
  excessive quantity; it is rejected in full without partial fulfillment.
- A response is lost after a reservation or order is recorded; a retry with the original request
  key resolves the same operation. An abandoned hold is eventually released on expiry.
- A request key is reused by a different shopper or with a changed sale, product, or quantity;
  shoppers' keys are isolated and changed payloads cannot reuse an earlier result.
- A process fails between business-state persistence and follow-up work, or after performing
  work but before acknowledging completion; recovery preserves one logical business effect.
- Duplicate, contradictory, or late payment outcomes arrive after an order is terminal; the
  recorded payment attempt and reservation state govern resolution, including compensation.
- A refund or notification repeatedly fails; its pending/failed delivery state remains visible
  and retryable without reopening the order or duplicating the effect.
- One catalog replica fails during a request, or two fail together; the success/quorum rule
  applies to completed responses and unavailable data is not reported as missing.
- Analytics receives no activity in a window, mixed currencies, or records after finalization;
  no division by zero or cross-currency revenue sum is presented, and late counts are explicit.
- Adding instances, restarting workers, or recovering replicas cannot reset shared limits,
  create stock, erase durable outcomes, or expose another shopper's records.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Authorized operators MUST configure a sale's product, positive price, currency,
  positive integer allocation, and start/end times; invalid values and end-before-start or equal
  times MUST be rejected. A not-yet-started sale MUST be cancellable.
- **FR-002**: A sale MUST become purchasable only after Catalog, Admission, and Inventory have
  applied the same current configuration and allocation and the sale window is active. Price
  and allocation MUST be immutable after the sale starts.
- **FR-003**: Shoppers MUST be able to browse product descriptions, image references, the current
  displayed offer, and upcoming/active/ended/unavailable sale status. Reservation eligibility
  MUST use the authoritative start-inclusive, end-exclusive window.
- **FR-004**: The system MUST distinguish authorized operators and shopper identities. Shoppers
  MUST access only their own order and notification details, and operator actions MUST reject
  unauthorized identities.
- **FR-005**: Admission MUST enforce configurable per-shopper and per-sale attempt limits and
  concurrent in-flight purchase limits across all instances. Rejections MUST identify overload
  or rate limitation and a retry delay; passing admission MUST NOT imply a stock guarantee.
- **FR-006**: Inventory MUST reserve a requested positive integer quantity atomically or reject
  it in full. At every committed inventory transition, available units plus held units plus
  confirmed sold units MUST equal the sale allocation, with all three quantities nonnegative.
- **FR-007**: A pending order MUST be accepted only after a durable reservation exists and MUST
  include its identifier, reserved quantity, reservation expiry, and retrievable order identifier.
  No payment MUST be initiated for a purchase rejected before order acceptance.
- **FR-008**: Orders MUST preserve an authoritative sale-version and price snapshot, currency,
  and total. A changed displayed offer MUST require shopper confirmation before submission;
  catalog display data MUST NOT determine authoritative charges.
- **FR-009**: A purchase request key MUST be scoped to a shopper and original purchase payload.
  Repeated requests after acceptance MUST resolve to the same logical order. Recorded business
  rejections MUST also be repeatable; mismatched payloads MUST be rejected. Admission-level
  rejections MAY be retried with the same key because no purchase has yet been accepted.
- **FR-010**: Shoppers MUST retrieve pending, confirmed, failed, or expired order state and,
  when applicable, pending/completed simulated refund status after a processing restart.
- **FR-011**: Payment MUST run independently of the initial purchase response and support
  controllable simulated success, failure, and delayed results. Each logical payment attempt
  MUST preserve its first recorded result across retries without another charge.
- **FR-012**: A reservation MUST transition from held to exactly one of confirmed, released, or
  expired. Only Inventory MUST decide that transition. Confirmation MUST require a live hold
  and MUST finish before Order reports confirmation; transitions MUST be safe to repeat.
- **FR-013**: Holds MUST have a configurable expiry, initially 120 seconds. Unconfirmed holds
  MUST be ineligible for confirmation at or after expiry and MUST be reclaimed even when no
  order was durably created. Expiry cleanup MUST not depend on shopper activity.
- **FR-014**: A failed payment MUST release its hold if still held and leave its order failed;
  an already expired reservation MUST instead leave the order expired. Expiry/release and
  confirmation MUST resolve to one authoritative terminal result under concurrency.
- **FR-015**: Successful payment for a hold that cannot be confirmed MUST create one durable
  simulated refund obligation. Retries MUST complete that obligation without duplicate refunds
  or reclaiming stock already offered to another shopper. The order MUST become or remain
  expired for an expired reservation, or failed for a released reservation.
- **FR-016**: Persisted business changes MUST retain any required follow-up work across crashes.
  Recovery MUST cover reservation without order, order without payment dispatch, confirmation
  without order-state update, and recorded payment or refund without outcome delivery.
- **FR-017**: Event processing MUST tolerate duplicate and out-of-order delivery without repeated
  business effects or terminal-state regression. Retries MUST have a documented finite budget;
  exhausted work MUST be inspectable and safely replayable without blocking unrelated work.
- **FR-018**: Outcome notifications MUST record the recipient, related order/reservation,
  outcome, and delivery state. Confirmation, failure, expiry, and refund updates MUST each
  produce at most one logical notice per recipient and outcome.
- **FR-019**: Shoppers MUST retrieve their recorded notifications. Unavailable notification or
  analytics processing MUST NOT block purchase acceptance or payment settlement, and processing
  MUST catch up after recovery.
- **FR-020**: Analytics MUST report, by sale/product and reporting window, purchase attempts,
  distinct shoppers, admission decisions and rejection reasons, payment successes/failures,
  confirmed units, confirmed revenue separated by currency, and purchase-response latency.
- **FR-021**: Analytics MUST use activity occurrence time and initially one-minute windows with
  a 60-second lateness allowance. Duplicate activity MUST count once. In-window corrections
  MUST update the same summary; post-finalization activity MUST be counted and inspectable as
  late without silently changing finalized totals. Provisional/final/delayed status MUST be shown.
- **FR-022**: Analytics MUST publish windowed summaries separately from raw purchase activity
  and MUST recover completed summaries and processing progress after restart without duplicate
  totals. Attempt counts MUST count distinct submitted attempts, including retries; confirmed
  sales and revenue MUST count distinct confirmed orders. Latency MUST measure initial response
  time for each attempt, including admission rejections, using documented measurement precision.
- **FR-023**: Catalog metadata MUST use three independently durable replicas without a permanent
  leader. Any replica MUST coordinate requests; successful writes MUST persist on two replicas
  and successful reads MUST receive two responses. One unavailable replica MUST be tolerated.
- **FR-024**: Metadata versions MUST distinguish causal successors from concurrent edits.
  Concurrent versions MUST be preserved and exposed to an authorized operator for explicit
  resolution; conflicting product metadata MUST be shown as temporarily unavailable until
  resolved. A successful resolution MUST become the causal successor of all resolved versions.
- **FR-025**: Returning or stale metadata replicas MUST converge through repair without losing
  acknowledged updates or the conflict-resolution history needed to reconcile them. Durable
  restart MUST preserve stored versions. Fewer than two reachable replicas MUST produce an
  explicit temporary-unavailability result for reads and writes.
- **FR-026**: Replicated catalog metadata MUST NOT become authoritative for sale prices,
  reservations, orders, or payments. Transactional purchase-state guarantees MUST remain
  independent of catalog availability and conflict resolution.
- **FR-027**: The storefront, browsing, purchase submission, and order status MUST be available
  through a public encrypted endpoint. Internal reservation and payment operations MUST have
  no public invocation route; protected operator capabilities MUST enforce operator access.
- **FR-028**: The project MUST retain eight separately owned domain responsibilities: Catalog,
  Flash Sale Manager, Admission, Inventory, Order, Payment, Notification, and Sales Analytics.
  The gateway, message transport, storage replicas, and monitoring are supporting components.
  Each service MUST own its state; consumers MUST use agreed interfaces rather than write it.
- **FR-029**: Infrastructure creation MUST be reproducible from versioned definitions. Service
  releases MUST reconcile to a reviewed, versioned desired state, expose the running revision,
  and support a documented rollback; unrecorded direct deployment MUST NOT be required.
- **FR-030**: Admission and Order MUST each scale to additional running instances under sustained
  demand using the resource-based scaling mechanism required by the source. Scaling MUST
  preserve shared limits, accepted work, and stock invariants; workload evidence MUST include
  instance counts, throughput, latency, rejection rates, and resource utilization over time.
- **FR-031**: Operators MUST inspect request rates, errors, latency, queued-work age, resource
  use, and service/replica health and correlate structured logs for one purchase across all
  eight services. Secrets and payment credentials MUST NOT appear in logs.
- **FR-032**: At least three distinct cloud storage products MUST be used with demonstrable
  responsibilities: transactional purchase state, product media and recovery artifacts, and
  independently persisted leaderless replicas. Application data ownership MUST remain explicit.
- **FR-033**: The project MUST preserve the source's required communication mechanisms,
  deployment/execution models, stateful analytics input/output separation, infrastructure and
  deployment controls, and monitoring requirements. The planning handoff in Assumptions MUST
  carry these constraints forward; general examples MUST NOT become provider or language mandates.
- **FR-034**: Ownership MUST be recorded in `task_divison.txt` during implementation planning,
  using the three-person 4+2+2 division below, with a named lead for every assignment category
  (a)-(h), supporting component, and individual demonstration.
- **FR-035**: The implementation plan MUST map acceptance scenarios to automated behavior,
  contract, integration, concurrency, failure, and recovery checks as applicable. Each runnable
  component MUST have an owner, reproducible start/test instructions, configuration guidance,
  and recovery instructions consistent with Constitution v1.0.0.

### Key Entities *(include if feature involves data)*

- **Participant**: A shopper or operator identity, role, and access to their own purchases and
  notices; used for admission accounting and activity attribution.
- **Product Metadata**: Product identity, description, image references, and version context;
  replicated display information rather than authoritative purchase state.
- **Sale Configuration**: Sale/product identities, version, authoritative price and currency,
  allocation, schedule, cancellation, readiness confirmations, and effective status.
- **Admission Decision**: Submitted attempt identity, shopper/sale, occurrence time, decision,
  rejection reason, and retry delay; admission approval does not allocate stock.
- **Stock Allocation and Reservation**: Available/held/sold quantities and a reservation's
  quantity, request identity, owner, expiry, and single terminal state.
- **Order**: Shopper, sale/product, request key and payload identity, reservation, price/version
  snapshot, quantity, total/currency, purchase state, and any compensation status.
- **Payment Attempt and Refund**: Stable simulated result, order identity, processing status,
  and uniquely associated refund obligation and completion result.
- **Activity Record**: Unique activity identity, schema version, type, occurrence time,
  correlation identity, sale/product, relevant shopper/order, and outcome or measurement.
- **Notification**: Recipient, causal outcome identity, related order/reservation, content,
  and processing/delivery state.
- **Windowed Summary**: Sale/product, window boundaries, revision and finalization status,
  counts, distinct shoppers, revenue by currency, latency distribution, and late-activity count.
- **Replica and Metadata Version**: Independently persisted node identity, availability, product
  versions and causal relationships, concurrent siblings, and resolution/repair state.
- **Release and Demonstration Record**: Desired/running revision, responsible member, workload
  definition, measurements, recovery evidence, and mapping to assignment requirements.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Every sale readiness, schedule-boundary, invalid-input, and cancellation scenario
  produces its specified outcome; no purchase is accepted before readiness or outside its window.
- **SC-002**: With a 100-unit allocation and at least 10,000 purchase attempts, confirmed units
  never exceed 100, available stock never becomes negative, and every recorded inventory
  transition satisfies available plus held plus sold equals 100.
- **SC-003**: In a ten-minute run with at least 10,000 attempts from at least 50 distinct shoppers,
  95% of browsing and initial purchase responses arrive within two seconds and 95% of explicit
  overload rejections within one second. Expected business rejections are reported separately;
  unexpected processing errors remain below 1% of attempts. Workload and limits are recorded.
- **SC-004**: In at least 1,000 repeated submissions and duplicate deliveries, including 50
  concurrent copies of one accepted purchase, there is one logical order, at most one charge,
  one reservation outcome, and at most one notice per shopper/order/outcome and one refund if
  required for each logical purchase.
- **SC-005**: With healthy processing restored, 95% of ordinary simulated payments reach a
  confirmed or failed shopper-visible outcome within ten seconds of order acceptance. Forced
  delays and failure-injection cases are reported separately from this normal-path measurement.
- **SC-006**: Under healthy recovery processing, 100% of abandoned unpaid holds are reclaimed
  within ten seconds after their 120-second expiry. Confirmation at/after expiry always fails;
  100% of unfulfillable successful payments have a visible refund obligation, completed within
  30 seconds after refund processing becomes available.
- **SC-007**: All declared interruption/reordering scenarios preserve inventory and payment
  invariants. Retryable work resumes within 60 seconds after dependencies recover; exhausted
  work is inspectable and replayable, with zero silently lost accepted orders or obligations.
- **SC-008**: Under healthy delivery, 95% of terminal-outcome notices are visible to the correct
  shopper within ten seconds of the outcome; retry tests produce no duplicate logical notices.
- **SC-009**: For a known activity dataset, finalized per-minute summaries exactly match expected
  counts, distinct shoppers, confirmed units, and revenue by currency; reported latency
  measurements match the dataset at the declared precision. Finalized summaries appear within
  five seconds after the 60-second lateness allowance ends and remain identical after duplicate
  replay and restart.
- **SC-010**: With one of three catalog replicas unavailable, every read/write in a fault test
  of 100 seeded products completes successfully while the remaining two are healthy. After
  connectivity is restored and concurrent edits are explicitly resolved, all three converge
  within 60 seconds. With fewer than two reachable replicas, none reports a successful operation.
- **SC-011**: A sustained-load demonstration records at least one increase in running instance
  count for both admission and order processing within ten minutes of sustained scaling demand,
  while preserving configured shared admission limits and the stock invariant.
- **SC-012**: From the documented setup, a teammate can reproduce the environment and complete
  one successful purchase, one failed-payment release, one expiry/late-payment refund, and one
  single-replica recovery using only documented steps; every step yields its expected evidence.
- **SC-013**: All eight services appear in operational evidence, a selected purchase is traceable
  through its participating stages, and the team can locate an injected failure and its affected
  order within five minutes using the recorded operational views.
- **SC-014**: Public-access and authorization checks allow browsing and the shopper's own
  purchase/status flows while denying every tested internal-operation, unauthorized operator,
  and cross-shopper record-access attempt.
- **SC-015**: The final project review maps every assignment category (a)-(h), all eight services,
  and three distinct storage-product uses to an owner and demonstration evidence. All three
  members can reproduce their assigned demonstrations; a reviewed release and rollback report
  the intended revision without losing existing orders or reservations.

## Assumptions

- **Scope**: This is one integrated educational project release, including storefront/operator
  views, the eight services, supporting infrastructure, cloud demonstration, and leaderless
  catalog storage. Individual work can be sequenced in the plan without creating extra specs
  in this invocation. Real payments, fulfillment/shipping, a shopping cart, multi-region
  deployment, and post-confirmation customer returns are outside this release.
- **Source precedence**: The latest three-member split supersedes the earlier four-person
  suggestion. Leaderless catalog storage and its independently persisted replicas supersede
  the earlier generic catalog-storage proposal; a separate additional document store is not
  required solely to reach three storage products. Cloud provider, language, and general-purpose
  tooling examples remain choices for planning.
- **Assignment evidence**: The assignment document and group sheet referred to in the discussion
  are not in this checkout. This specification treats the requirements recorded in the supplied
  discussion, including the later confirmed leaderless concept, as the available source of
  truth; it does not claim independent verification of those external documents.
- **Planning amendment (2026-10-25)**: The user's later planning instructions replace the
  source's prescribed analytics engine and limit concrete implementation-stack choices to the
  Mukthish's services and supporting components. The other members retain ownership of their
  language/framework/database/deployment-tool choices. The shared broker remains Kafka; a client
  library is not a replacement broker. Mukthish's detailed choices are recorded in the plan.
- **Planning constraint handoff**: Carry forward the original six independently deployed
  application services on managed container orchestration, with Mukthish's stateful
  Analytics added to that platform; event-triggered serverless Notifications; the three required
  communication mechanisms; separate raw/results destinations on the required event transport;
  resource-driven scaling for Admission and Order; infrastructure-as-code; repository-driven
  reconciled deployments; prescribed metrics/dashboard tools and centralized logs. The earlier
  prescribed analytics-engine/managed-processing-runtime choice is superseded by the amendment
  above. Provider and teammate implementation choices remain delegated to their owners.
- **Identity and access**: Seeded or test identities with enforced shopper/operator roles are
  sufficient for the demonstration. Self-service registration and external identity federation
  are outside scope. Shoppers can view only their own records; operator-only writes are protected.
- **Purchase boundaries**: Each order contains one product with a positive integer quantity;
  partial fulfillment is excluded. An operator may cancel a sale before its start, and active
  sale cancellation or edits to price/allocation are outside scope. An existing unexpired hold
  may settle after the sale's end. Inventory's time and state decide expiry races.
- **Demonstration defaults**: The source suggests a two-minute hold and 100-unit sale; these are
  adopted as initial defaults. The ten-minute/10,000-attempt workload, timing thresholds,
  one-minute analytics windows, 60-second lateness, and 100-product recovery dataset are explicit
  initial acceptance targets, not measurements or guarantees already established by the source.
  The plan MUST specify reproducible load, scaling thresholds, retry budgets, and capacity.
- **Metric meanings**: An attempt is one uniquely identified submission seen by Admission; a
  transport redelivery is not a new attempt, but a shopper's explicit retry is. Rejection rate is
  rejected admission decisions divided by all admission decisions, with zero-denominator windows
  shown as zero activity. Confirmed revenue is the sum of confirmed order totals per currency.
- **Metadata conflicts**: Authorized manual reconciliation is the initial conflict policy. A
  conflicting product is unavailable for ordinary metadata browsing until resolution; accepted
  order processing uses its durable authoritative snapshot. Fixed replica membership is enough;
  automatic membership changes and advanced placement/handoff extensions are outside scope.
- **Dependencies**: Implementation requires agreed producer/consumer contracts and state
  transitions, shared cloud access and budget, provisioned compute/storage/message transport,
  and a reviewable release repository. The plan MUST define these before implementation and
  provide local reproducibility; credentials, technology selection, and detailed interfaces
  are not delivered by this specification.
- **Quality**: Formatting, static analysis, behavioral tests, contract review, and operational
  documentation follow Constitution v1.0.0. This specification defines expected behavior;
  runnable code, detailed contracts, deployment artifacts, and task allocation files follow
  during planning and implementation.

The agreed ownership baseline is:

| Member | Services | Supporting responsibilities and assignment leads |
| --- | --- | --- |
| Mukthish | Admission, Flash Sale Manager, Order, Sales Analytics | Frontend, gateway, Message Queue; (c) scaling/load testing and (e) stream processing |
| Member 2 | Inventory, Payment | Shared infrastructure definitions, managed platform, reconciled deployments, observability; (a), (d), (h) |
| Member 3 | Catalog, Notification | Leaderless storage, serverless integration, storage-product design; (b), (f), (g) |

These are coordination leads; shared contracts and integration require review from affected
members. Actual member names and implementation tasks belong in the plan and ownership record.
