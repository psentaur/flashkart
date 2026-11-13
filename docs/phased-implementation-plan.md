# Flashkart: Phased Implementation Plan

This roadmap turns the [technical architecture](implementation-plan.md) into executable implementation phases. That document remains the source of truth for system behavior; this document defines build order, dependencies, deliverables, and completion checks.

**Current status:** Phases 1–5 passed their implementation checks. Phase 6 passed local PostgreSQL concurrency, admission replay, and SQS emulator delivery checks. Phases 7–12 are unstarted. [Phase 6 development](phase-6-development.md)

Develop and verify locally through Phase 11. Phase 12 covers AWS integration and reproducible deployment. Progress is based on passing technical checks, without calendar or team-size assumptions.

## Phase overview

| Phase | Focus | Working result at completion | Requires |
| --- | --- | --- | --- |
| 1 | Repository and contracts | Buildable service shells and agreed interfaces | Architecture |
| 2 | Local environment and replica persistence | One Go agent durably stores versioned documents in PostgreSQL | 1 |
| 3 | Quorum coordination and conflicts | Four agents perform replicated reads/writes and preserve concurrent versions | 2 |
| 4 | Replication recovery | Sloppy quorums, durable hints, repair, and partition recovery work | 3 |
| 5 | Catalog and sale publication | Products are browsable and sales can be published and activated | 4 |
| 6 | Admission and inventory | Purchase attempts queue safely and reserve stock without overselling | 5 |
| 7 | Orders and simulated payments | Purchases complete, expire, or compensate correctly | 6 |
| 8 | Notifications and event delivery | Terminal outcomes reach users and business events reach Kafka | 7 |
| 9 | Flink analytics | Checkpointed window aggregates reach the analytics API | 8 |
| 10 | Demo frontend | Customer, admin, analytics, and replication screens work together | 9 |
| 11 | Local Kubernetes and GitOps | The application deploys through ArgoCD, is observable, and scales under load | 10 |
| 12 | AWS integration and redeployment | The complete managed-cloud deployment passes its acceptance suite | 11 |

The default execution order is sequential. Keep intermediate states explicit: for example, Phase 6 proves reservation correctness before Phase 7 adds successful purchases. A phase is complete only when its checks pass against its real dependencies.

## Phase 1: Repository, contracts, and service foundation

**Objective:** establish boundaries and tooling before implementing distributed behavior.

**Deliverables**

- Create one root Go module, a Java 17/Maven analytics project, a React/TypeScript frontend project, and areas for contracts, migrations, deployment configuration, documentation, and tests.
- Add executable shells for the six Go application services, Notification, the replica agent, and the analytics companion. Include configuration, graceful shutdown, structured logs, and metrics; HTTP/gRPC servers expose health endpoints. Do not return placeholder business success responses.
- Define OpenAPI endpoints, protobuf messages/RPCs, and versioned JSON event schemas. Include attempt IDs, operation IDs, entity IDs, causal context, error codes, and correlation IDs.
- Define the reservation, order, payment, and publication states and their allowed transitions. Specify which service owns each state and outgoing event.
- Establish separate configuration for five PostgreSQL connections, peer identity/address mapping, AWS endpoints, Kafka authentication, and local versus AWS credentials. Define demo identity and admin-access boundaries.
- Add build/test automation and CI for the components that exist. Lock compatible project dependencies and toolchains, including the agreed Flink/connector combination.

**Completion checks**

- Go, Java, and frontend shells build; generated contracts are reproducible; CI passes.
- A service starts with valid configuration, reports health, and shuts down cleanly. Invalid configuration fails clearly.
- Contracts represent retries, conflicts, pending publication, and unknown outcomes without pretending these are successful purchases or rolled-back writes.

**Evidence:** build/contract checks and a service startup smoke test. Proposed commands: `make build`, `make test-unit`, `make test-contracts`.

## Phase 2: Local dependencies and one durable replica

**Objective:** prove the local persistence boundary before introducing network replication.

**Deliverables**

- Add a Compose environment with five independent PostgreSQL instances and separate volumes: four replica backends and one transactional backend. Add selectable Kafka/Flink profiles so heavier dependencies can start when needed.
- Integrate the installed AWS Emulator through isolated local configuration. Verify the SQS, DynamoDB, S3, and Lambda operations needed by later phases; record unverified behaviors as cloud-test requirements rather than claiming emulator fidelity.
- Add repeatable migrations and idempotent seed fixtures. Replica databases share a schema; the transactional database uses service-owned schemas and roles.
- Implement one replica agent's SQL persistence for document versions, exact causal contexts, counters, operation journals, and hints. Implement transactional per-document locking and acknowledge only after established commit success.
- Implement local mutation preparation, idempotent application, sibling merge, version reads, document-ID listing, and SQL-backed readiness. Keep prepared-but-unapplied mutations invisible to document reads.
- Unit-test causal interval union, dominance, duplicate envelopes, and gaps; add real-PostgreSQL tests for concurrency, rollback, and operation replay.

**Completion checks**

- Fresh bootstrap, migration, and seeding succeed repeatedly. Restarting an agent or its database preserves data and counters.
- Reapplying the same envelope has one effect; conflicting reuse of an operation identity is rejected.
- Concurrent updates preserve the correct versions. SQL failures do not produce success acknowledgements.

**Evidence:** persistence integration tests. Proposed commands: `make dev-up`, `make migrate`, `make seed`, `make test-persistence`.

## Phase 3: Four-node coordination and causal conflicts

**Objective:** implement normal replicated operations over independent PostgreSQL backends.

**Deliverables**

- Start agents A-D with stable identities, separate database credentials, and peer gRPC endpoints. Each agent may coordinate a request; it accesses other databases only through peer agents.
- Implement the fixed consistent-hash ring and virtual nodes, selecting three distinct preferred owners for each document.
- Implement the preferred-owner write path with N=3/W=2 and quorum reads with R=2. Add configurable deadlines and explicit quorum-unavailable/unknown-write-outcome errors. Fallback writes are added in Phase 4.
- Implement the shared client and peer contracts for mutation preparation/application, reads, version merging, and diagnostics. Preparation retries retain origin affinity until the immutable envelope is recovered.
- Return concurrent siblings and their exact combined context. Resolve them only through an explicit context-bearing update.
- Export replica participation, SQL failures, quorum failures, latency, and sibling metrics.

**Completion checks**

- The same document can be written and read through different coordinators; SQL inspection shows the expected independent copies.
- One preferred owner can be unavailable while the two remaining owners satisfy a write. An insufficient write quorum returns an unknown outcome, including when a partial commit exists.
- Concurrent stale-context writes through both different coordinators and the same coordinator survive as siblings. Resolving observed siblings converges without incorrectly claiming unseen versions were observed.

**Evidence:** `make test-replication-core` passed against four independent PostgreSQL databases over loopback gRPC. A separate startup check verified readiness and metrics on all four agent executables.

## Phase 4: Sloppy quorums, hinted handoff, and recovery

**Objective:** complete the selected Extended Dynamo behavior.

**Deliverables**

- Add fallback writes with complete durable envelopes and intended-owner hints. Count distinct physical replicas, never hint targets, toward W.
- Include hinted versions in reads across all four agents; distinguish an empty successful SQL read from an unreachable database.
- Implement hinted handoff with destination-commit acknowledgement before source hint deletion. Keep pending hints independent of visible-version pruning and without expiry.
- Add read repair and paginated background synchronization covering visible versions and outstanding hints. Use the same causal merge everywhere.
- Add repeatable fault controls that can stop an agent, restart a PostgreSQL backend, block one agent's SQL connection, or partition peer traffic. Keep failure injection in the test harness, not in public customer endpoints.
- Add recovery diagnostics: pending hints, oldest hint age, synchronization progress, and backend health.

**Completion checks**

- With two preferred owners unavailable, a surviving owner plus fallback can satisfy W=2. Two hints on that fallback count as one acknowledgement.
- Hints survive process/backend restarts. Crashes around destination commit and source deletion cause safe replay, not lost acknowledged versions.
- A 2-2 partition permits divergent writes; healing exposes siblings and explicit resolution converges. Stale reads are demonstrated rather than hidden.
- Untouched documents converge through background synchronization; three unavailable replicas prevent quorum operations.

**Evidence:** `make test-replication-recovery` passed against four PostgreSQL backends. The local database fault target also verified persisted hints across a container restart.

## Phase 5: Product Catalog and durable sale publication

**Objective:** make the replication layer part of normal marketplace behavior.

**Deliverables**

- Implement product administration, browsing, and published-sale reads using the replication client. Add product images through the configured S3 adapter and eventual document-list discovery.
- Surface product conflicts as `409 CATALOG_CONFLICT`; provide protected admin resolution and replica-inspection APIs.
- Implement Flash Sale Manager's draft APIs and durable publication workflow in its transactional schema. Published terms remain immutable.
- Implement Inventory's `PrepareSale` and `ActivateSale` endpoints now, including its authoritative immutable sale configuration. Reservation processing belongs to Phase 6.
- Connect publication: persist operation/origin, prepare Inventory, prepare and durably record the replication envelope, apply it, activate Inventory, and expose the final publication status. Every step resumes after interruption.

**Completion checks**

- A product can be created and read through the public Catalog API while one replica is unavailable.
- A sale progresses from editable draft to published and activated with matching immutable terms in the replication layer and Inventory.
- Crashes after each publication step resume the same operation. Lost preparation responses do not allocate replacement versions on another origin.
- Published-term edits are rejected, and unresolved catalog conflicts remain visible instead of silently selecting a price.

**Evidence:** `make test-catalog-publication` passed with PostgreSQL and loopback gRPC. The local service launcher completed a publication through HTTP, and a signed image download succeeded against AWS Emulator.

## Phase 6: Admission, queues, and atomic reservations

**Objective:** accept purchase intents durably and enforce the stock invariant.

**Deliverables**

- Implement Admission's per-replica token bucket, bounded concurrency, input validation, and idempotency rules. Distinguish `202` acceptance, `429` throttling, and conflicting reuse of an attempt ID.
- Commit accepted attempts and purchase-queue outbox entries together. Publish outbox work to SQS with stable identifiers and retryable delivery.
- Implement Inventory's SQS consumer and the transaction that records the attempt outcome, checks authoritative sale activation/time, conditionally decrements stock, creates a held reservation, and records outgoing order work.
- Persist rejection outcomes as well as successful reservations. Add conditional commit/release transition primitives for use by the order workflow.
- Persist business events with stable IDs as part of state transitions; Kafka publication arrives in Phase 8. Keep outgoing order work pending until Phase 7 implements its consumer.

**Completion checks**

- Under 1,000 distinct concurrent attempts against 100 units, no more than 100 units are held and `available + held + sold = initial stock` remains true.
- Concurrent duplicates and queue redelivery produce one reservation/decrement. Reusing an attempt ID with different input fails.
- A queued attempt still checks the sale window when processed. Inactive, ended, or exhausted sales produce stable rejection outcomes.
- Queue interruption and crashes around SQL commit/outbox publication recover without losing accepted attempts. `202` is sent only after durable acceptance.

**Evidence:** `make test-reservations` passed against PostgreSQL and an isolated signed SQS emulator queue.

## Phase 7: Orders, payments, expiry, and compensation

**Objective:** complete purchases through a durable, recoverable workflow.

**Deliverables**

- Implement idempotent Order creation from Inventory's durable gRPC work, plus the order-query API.
- Persist orchestration progress so workers resume unfinished operations after restart; do not rely on an uninterrupted chain of RPC calls.
- Implement Payment's stable payment IDs, persisted outcomes, status queries, and idempotent refund operation. Add deterministic success, decline, delay, and lost-response fixtures.
- Implement confirmation only after Inventory commits the reservation. Add release and expiry, using the configured two-minute lifetime and five-second sweep.
- Serialize expiry/confirmation on reservation rows and check database time after locking. Compensate late payment success and preserve unknown payment outcomes until queried or retried.
- Emit durable order, payment, and terminal-attempt events for later consumers.

**Completion checks**

- The API completes successful and declined purchases end to end. A 100-unit sale never produces more than 100 confirmed purchases.
- Duplicate RPCs yield one order/payment effect. Crashes after payment success but before Order records it recover using the existing payment result.
- Repeated confirmation/expiry races have one winner. Late successful payments trigger one refund, and releases never return stock twice.

**Evidence:** workflow, concurrency, and crash-recovery tests. Proposed command: `make test-workflows`.

## Phase 8: Notifications and reliable business-event delivery

**Objective:** expose terminal purchase outcomes and make committed events available to analytics.

**Deliverables**

- Implement outbox delivery to the outcome SQS queue and Kafka `purchase-events`. Track each destination independently so failure of one does not falsely complete another.
- Implement Notification's Go handler and Lambda entry point. Use the same handler in a local SQS runner if emulator event-source mapping behavior is unverified.
- Write terminal notifications to DynamoDB conditionally, with attempt/customer identity and stable event IDs. Add attempt-status and notification queries to the application APIs.
- Add retries, dead-letter handling, replay commands, and backlog metrics. A failed item in a Lambda batch must remain eligible for retry.
- Implement local Kafka configuration and the configurable AWS IAM authentication adapter; actual MSK token-refresh verification belongs to Phase 12.

**Completion checks**

- Success, stock rejection, payment decline, and expiry produce the correct in-app outcome.
- Duplicate or reordered events cannot duplicate notifications or regress terminal status.
- Kafka/SQS interruption, publish-before-ack crashes, and consumer restarts preserve delivery; replay retains original event IDs.
- Captured Kafka records match the event schemas and recorded transaction outcomes.

**Evidence:** notification and event-delivery integration tests. Proposed command: `make test-events-notifications`.

## Phase 9: Stateful Flink analytics and result projection

**Objective:** implement the eighth application service with measurable, recoverable stateful processing.

**Deliverables**

- Implement the Java Flink job using the agreed runtime and Kafka connector. Consume `purchase-events` and publish complete snapshots to `analytics-results`.
- Add checkpointed event deduplication, one-minute event-time windows, ten seconds of out-of-order tolerance, and thirty seconds of allowed lateness.
- Calculate demand, admission/stock rejections, reservations, confirmed purchases, and processing latency. Produce final window results and count dropped late events.
- Implement partition heartbeats and exclude them from business counts. Configure checkpoint/restart behavior using standard Flink filesystem support for local durable storage and S3.
- Implement the Go analytics companion's result consumer, conditional DynamoDB upserts, and query API. Commit consumer progress only after the projection write is established.

**Completion checks**

- A fixed input fixture produces exact expected window results, including duplicates and events around window boundaries.
- Windows finish when business traffic becomes idle. Events beyond the late boundary are accounted for without changing finalized totals.
- Restarting Flink from a checkpoint and restarting the projector after a database write but before offset commit do not inflate results.
- Analytics can be queried through the API and traced back to source events.

**Evidence:** deterministic aggregation and recovery tests. Proposed command: `make test-analytics`.

## Phase 10: Complete demo frontend

**Objective:** demonstrate the implemented system through coherent customer and administrator flows.

**Deliverables**

- Build product/sale browsing, purchase submission, queued-state polling, terminal outcomes, order details, and notifications.
- Build protected product/draft administration, publication-progress views, and immutable published-sale details.
- Add analytics charts backed by the analytics API, with window timestamps and projection freshness visible.
- Add replica diagnostics showing health, preferred owners, version siblings, pending hints, and explicit conflict resolution. The UI observes failures; the test harness injects them.
- Handle loading, retries, throttling, unavailable quorums, and conflicts. Preserve the attempt ID when retrying the same purchase intent.

**Completion checks**

- Browser tests cover publication, successful purchase, declined purchase, sold-out rejection, and queued-to-terminal transitions using real backend APIs.
- A repeated click or network retry does not create another purchase intent accidentally.
- Admin actions are protected; demo customer queries respect their identity boundary; conflict resolution and analytics display real persisted data.

**Evidence:** browser end-to-end tests and a complete local demo. Proposed command: `make test-e2e`.

## Phase 11: Local Kubernetes, GitOps, observability, and HPA

**Objective:** rehearse the deployment model locally before using managed cloud services.

**Deliverables**

- Create container builds and kind overlays. Deploy five real local PostgreSQL instances with isolated volumes; give each replica agent its fixed identity and dedicated backend.
- Bootstrap ArgoCD and make it own subsequent application deployments, operators, migration Jobs, Services, and configuration. Use the same base manifests with environment-specific overlays.
- Deploy the Flink operator/job, Metrics Server, Prometheus, Grafana, and local centralized logging. Build dashboards for application health, stock, workflow backlog, replica recovery, and analytics progress.
- Configure Catalog and Admission HPAs at two to five replicas with a 60% CPU target and resource requests. Give the cluster capacity to reach both maximums; keep replica-agent membership fixed.
- Add readiness, graceful termination, bounded SQL pools, and shutdown behavior that preserves retryable work. Agent readiness depends on its database; liveness does not fail merely because RDS is unavailable.
- Run sustained k6 scenarios and repeat the replication/workflow recovery suites through deployed services. Capture the evidence rather than only checking manifest syntax.

**Completion checks**

- A fresh local cluster reaches the expected state through GitOps and migrations. A Git change rolls out an immutable image, and ArgoCD repairs intentional configuration drift.
- Both HPAs visibly scale out under sustained application traffic. Stock and idempotency invariants continue to hold.
- Dashboards and centralized logs cover all application components, including Notification's local runner and Flink.
- Replacing a replica-agent pod preserves its state in PostgreSQL; backend outages and peer partitions remain distinguishable in telemetry.

**Evidence:** local deployment, load-test, and recovery reports. Proposed commands: `make kind-up`, `make gitops-bootstrap`, `make test-load`, `make test-kubernetes`.

## Phase 12: AWS integration, teardown, and reproducible redeployment

**Objective:** validate cloud behavior that local tests and emulation cannot establish.

**Deliverables**

- Implement Terraform for networking, EKS, four independent replica RDS instances, the transactional RDS instance, IAM, image registry, SQS/DLQs, Lambda, DynamoDB, S3, MSK Serverless, CloudFront/ALB routing, and centralized logging. Keep bootstrap state/build artifacts separate from the disposable application environment.
- Bootstrap ArgoCD and deploy cloud overlays, migration Jobs, EMR's Flink operator/job, and applications through GitOps. Replica agents use RDS and have no persistent local volumes.
- Configure scoped credentials, real MSK IAM authentication, Lambda-SQS invocation, RDS connectivity, and S3 checkpoints. Use the same business code, contracts, and seed fixtures as the local deployment.
- Run the cloud acceptance suite: public customer/admin flows, real permissions, Kafka token refresh, Lambda retry behavior, checkpoint restoration, SQL outages, centralized logging, and sustained EKS HPA scaling.
- Export verification evidence, perform ordered teardown while required controllers are still available, and recreate the environment from the same versioned infrastructure, images, manifests, migrations, and seed data.

**Completion checks**

- All eight application services and the PostgreSQL-backed replication layer operate through the public application and private service interfaces as designed.
- One RDS backend becoming unreachable triggers actual quorum/fallback behavior. A healthy agent cannot conceal its failed backend or manufacture an acknowledgement.
- Cloud-specific tests pass; local emulator success alone is never recorded as IAM, networking, or managed-runtime verification.
- Teardown and clean redeployment succeed without relying on undocumented manual setup. Repeat smoke tests and representative purchase, analytics, and replication-recovery scenarios after recreation.

**Evidence:** cloud acceptance and redeployment reports. Proposed commands: `make cloud-smoke`, `make test-cloud`, `make demo`, `make teardown-demo`.

## Completion and tracking rules

- Implement tests with the behavior they verify, rather than deferring correctness checks to the final phase. Later phases rerun the relevant suites against their deployed environment.
- Keep phase-specific fixtures and failure scripts repeatable. Avoid tests that pass only against mocks when the behavior depends on SQL commits, queue delivery, or peer communication.
- Record each phase's status, implemented deliverables, check results, and known limitations as work proceeds. An unverified requirement remains open.
- PostgreSQL owns local durability throughout. The custom work remains coordination, version/conflict handling, replication, and recovery; none of the phases builds a database engine.
- Project completion requires all phase checks plus the acceptance scenarios in the architecture document. The runtime must not depend on temporary stub responses or manually repaired data.
