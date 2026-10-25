# Quickstart and Validation Guide: Flash Sale Platform

**Status**: Phase 1 design guide; application and deployment code do not exist yet.
**Date**: 2026-10-25

The `make`, service, frontend, cloud, and scenario commands below are the command contract that
implementation must supply. They have not been run against an application. The existing Spec Kit
command in the next section is usable now. Expected outcomes come from [spec.md](spec.md),
[data-model.md](data-model.md), and [service contracts](contracts/README.md).

## Validate planning prerequisites now

From the repository root:

```sh
export SPECIFY_FEATURE_DIRECTORY=specs/001-flash-sale-platform
python3 .specify/scripts/python/check_prerequisites.py --json --require-spec
```

Expected: active feature resolves to `specs/001-flash-sale-platform`, plan/spec exist, and
`research.md`, `data-model.md`, `contracts/`, and this guide are discoverable. Do not require
`tasks.md` until `$speckit-tasks` has generated it. No application build/test result is implied.

## Prerequisites for implemented local scenarios

- For your components: Go 1.27.3, Python 3.11+, a supported Docker Engine with Compose v2,
  Make, curl, jq and k6; browser automation tooling is supplied by the integration target. Goka
  v1.1.17 and IBM Sarama v1.46.3 are locked Go dependencies, not replacement Kafka servers.
- Teammates supply their own runtimes/build/test tools and service adapters. This guide does not
  choose their languages, database engines, function provider or deployment products.
- Container images and tools are version/digest pinned. Do not use floating `latest` tags.
- A developer configuration contains service addresses and disposable local database credentials.
  Local signing keys and seeded JWTs are generated during setup and excluded from version control.
- Seeded operator and shopper identities, one product, one 100-unit sale, a controllable clock for
  deterministic tests, and a payment simulator with stable success/failure/delay fixtures.
- Distinct named volumes for the three metadata nodes and separate logical service databases.
  Topic prefixes, fixture identifiers and verifier permissions are isolated by run ID.

## Start the implemented local environment

```sh
make bootstrap
make dev-up
make migrate
make seed PROFILE=baseline
make wait-ready
```

`bootstrap` checks your tools and installs locked dependencies; `dev-up` starts your PostgreSQL,
Kafka, four Go services, gateway/frontend and the teammates' supplied service/function adapters.
`migrate` runs your migrations and delegates teammate migrations to their declared commands. `seed` generates live sale schedules, operator
and shopper tokens, product metadata and declared simulator modes; it must not use the fixed
timestamps in contract examples as a live sale. `wait-ready` waits on explicit health/readiness
conditions with a deadline, rather than sleeping for an arbitrary duration.

The gateway defaults to `http://localhost:8080`; the frontend uses that same origin. Local HTTP
is a development transport; the cloud demonstration uses the HTTPS output URL. The notification
adapter exercises the same handler without claiming the local process is a serverless deployment.
Goka caches and Kafka group tables exercise restart behavior; local containers do not prove
cloud-platform availability. Run an empty-cache restore and partition reassignment as well as a
normal retained-cache restart.

## Contract and quality gates

```sh
make fmt-check lint typecheck contracts-check
make test-unit
make test-integration
make test-races
make test-e2e
```

Expected: gofmt, vet, static analysis and compilation pass for your Go code; teammate checks run
through their declared adapters. No teammate unit framework is prescribed. Contract validation parses
the three OpenAPI documents, compiles Inventory protobuf, validates every JSON event example,
checks allowed producer/topic/type combinations, and detects generated-client drift. Schema-only
checks cannot prove business invariants; integration/race tests must run real SQL and messaging.
The test targets produce machine-readable reports under a run-specific evidence directory.

Do not run Go workspace-root `go test ./...` and assume it covered all modules. The Make target
enumerates your modules and runs `go test -race ./...` there. Use Goka tester for pure callbacks
and real Kafka for group-state/View recovery, relay failures, duplicate replay and rebalancing.
A controlled clock and failpoints drive tests; ordinary end-to-end runs also verify real time.

## Validate one purchase through the public contract

After seeding, obtain the disposable `SHOPPER_TOKEN`, `SALE_ID`, and `PRODUCT_ID` from the local
fixture report. The baseline unit price is 2500 minor units of INR and offer version is 1.

```sh
export FLASHSALE_BASE_URL=http://localhost:8080
curl --fail-with-body "$FLASHSALE_BASE_URL/v1/sales/$SALE_ID/offer"
curl --fail-with-body -X POST "$FLASHSALE_BASE_URL/v1/purchases" \
  -H "Authorization: Bearer $SHOPPER_TOKEN" \
  -H "Idempotency-Key: quickstart-purchase-1" \
  -H "Content-Type: application/json" \
  --data "{\"sale_id\":\"$SALE_ID\",\"product_id\":\"$PRODUCT_ID\",\"quantity\":1,\"expected_offer_version\":1,\"expected_unit_price_minor\":2500,\"currency\":\"INR\"}"
```

Expected: an active, fully ready offer, then 202 with a durable pending Order, reservation and
expiry. Copy `order_id` from that response into `ORDER_ID` and inspect status:

```sh
curl --fail-with-body "$FLASHSALE_BASE_URL/v1/orders/$ORDER_ID" \
  -H "Authorization: Bearer $SHOPPER_TOKEN"
curl --fail-with-body "$FLASHSALE_BASE_URL/v1/notifications" \
  -H "Authorization: Bearer $SHOPPER_TOKEN"
```

With the success fixture, the Order becomes CONFIRMED and one confirmation notice appears within
the specified timing targets. Repeat the original POST with the same key and payload: it resolves
the same Order, with no additional stock or payment effect. Changing quantity with that key returns
409 IDEMPOTENCY_CONFLICT. An operator can retrieve conflicting catalog versions and resolve them
using the admin GET/PUT contract; shoppers cannot invoke those operations.

## Deterministic acceptance scenarios

Each `make demo` scenario must seed an isolated run, drive explicit conditions/failpoints, wait
for bounded outcomes, and export invariant and timing results. It must fail on a mismatch rather
than print a plausible demonstration transcript.

| Planned command | Required observation | Acceptance references |
| --- | --- | --- |
| `make demo SCENARIO=sale-readiness` | Missing/current/obsolete acknowledgements, cancellation and start/end boundaries behave correctly; price/allocation freeze holds | US1; SC-001 |
| `make demo SCENARIO=purchase-success` | One live hold -> pending Order -> stable successful charge -> confirmed hold/Order -> one notice | US2; SC-004/005/008 |
| `make demo SCENARIO=payment-failure` | Failed charge releases once; Order fails; stock returns | US3.1; SC-006 |
| `make demo SCENARIO=expiry-race` | 120-second hold expires; simultaneous Confirm/Release/Expire has one winner; confirmation at expiry fails | US3.2/3; SC-006 |
| `make demo SCENARIO=late-payment` | Expired/released hold is never reopened; later success creates/completes one visible refund | US3.4; SC-006 |
| `make demo SCENARIO=crash-recovery` | All interrupted boundaries in data-model recovery table resume safely, including a lost successful Confirm response | US3.5; SC-007 |
| `make demo SCENARIO=duplicate-replay` | 1000 repeats/deliveries with 50 concurrent copies of one request preserve one logical effect | US2.3, US3.5, US4.2; SC-004 |
| `make demo SCENARIO=poison-record` | Finite retries, inspectable original payload, unrelated-key progress, authorized replay | US3.6; SC-007 |
| `make demo SCENARIO=notice-recovery` | Notifications catch up after failure; duplicate Order/Inventory expiry yields one expiry notice | US4; SC-008 |
| `make demo SCENARIO=analytics-replay` | Golden per-minute results, distinct shoppers, lateness corrections, explicit FINAL, ordered quiet-input heartbeats, View/relay repair and empty-cache restore | US5; SC-009 |
| `make demo SCENARIO=catalog-quorum` | Requests coordinate via every node; one down works; two down report unavailability, not absence | US6.1/2/5; SC-010 |
| `make demo SCENARIO=catalog-conflict-repair` | Concurrent siblings survive, explicit resolution dominates observed contexts, stale node/durable restart converge | US6.3/4; SC-010 |
| `make demo SCENARIO=access-boundaries` | Own records allowed; other shoppers/operator-only/internal stock/payment routes denied | US2.6, US4.3, US7.1; SC-014 |

The verifier records every Inventory commit or transition-ledger entry and checks
`available + held + sold = allocation` and nonnegativity, not just a final count. It also checks
stable payment/refund and canonical notice counts. Independent database inspection is read-only
and uses the verifier role; normal services never bypass each other's ownership.

## Sustained workload and actual autoscaling

```sh
make load PROFILE=correctness
make load PROFILE=scaling
```

The correctness profile uses a 100-unit sale, at least 10,000 attempts over ten minutes and at
least 50 distinct shoppers. Export p50/p95/p99 response latency, rate/overload/business rejections,
unexpected errors, queue age, payment/refund timings and the full stock invariant report. Keep
expected sold-out/limit outcomes separate from unexpected errors. Count browse and purchase
latencies separately; include unsuccessful attempts in response distributions.

The scaling profile uses a separate sale with ample preallocated units so demand reaches Order
throughout the run. Raise admission limits in the versioned pre-start configuration. Calibrate
real purchase load to exceed the initial pods' 60% CPU target; record request rates and all
thresholds. Do not inject fake CPU work or claim one replica count proves HPA scale-out. Both
Admission and Order must gain a ready running instance within ten minutes of sustained demand,
while shared limits and stock invariants remain valid. CPU requests, Metrics Server and worker
headroom must be present; failed scheduling or database saturation is a failed demonstration.

## Shared cloud release and demonstration

These are integration command contracts, not provisioned infrastructure. The platform owner
chooses the cloud provider, managed Kubernetes, IaC and GitOps products; Member 3 chooses the
serverless Notification implementation. They supply account/repository/registry inputs and
resource definitions. You supply your containers, Kafka configuration and Goka state/recovery needs.
A concrete provider-specific deployment recipe follows their owned planning, not this stack choice.

```sh
make cloud-preflight
make cloud-plan
make cloud-apply
make release ENV=demo
make wait-ready ENV=demo
make demo SCENARIO=cloud-evidence ENV=demo
```

`cloud-preflight` verifies the owners' selected capabilities, permissions, quotas, runtime versions,
HTTPS access and cost inputs. `cloud-plan` produces the reviewable IaC change. `cloud-apply`
provisions its declared scope; `release` records immutable desired-state references and the chosen
GitOps controller reconciles Kubernetes applications. No direct `kubectl apply` substitutes for
service release. The platform owner documents the serverless release boundary and rollback too.

Cloud evidence shows the original six services plus your Goka Analytics on managed Kubernetes,
event-triggered serverless Notifications, Kafka raw/results/compacted-state separation, all three
communication mechanisms and three independently durable catalog replicas. Show Prometheus/Grafana
views and centralized logs for all eight services; no provider-specific logging product is mandated.
Locate an injected order failure within five minutes. Record three distinct product uses for
transactional state, object media/evidence and independent replica block storage.

Revert a reviewed release, reconcile, and verify existing orders, reservations and notices survive.
Use compatible schema changes. Restart Goka with an empty cache and move a partition to another
instance: state and seen IDs restore from the group table, and a lost output is republished from
the View without double-counting. Restore/replay uses the recorded broker-arrival/clock-marker
ordering, not current process time. Verify that no final result regresses or disappears.

## Cleanup and handoff

```sh
make dev-down
```

`dev-down` stops processes while retaining fixture volumes. An explicit run-scoped reset target
may remove only disposable test data; no generic destructive cleanup runs as a side effect of
tests. Cloud teardown is a separately reviewed operation that records what state/evidence is
retained and which billable resources remain. Handoff includes the release revision, contract
version, run ID, reports, dashboards/log queries, three-member ownership record and assignment
(a)-(h) evidence. Actual member names fill the role labels when the team starts implementation.
