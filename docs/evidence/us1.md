# Phase 3: sale publication and browsing evidence

Date: 2026-10-25. Owner: Mukthish. Scope: T021–T030, independent fixture mode.

## Implemented behavior

Flash Sale owns canonical configuration, current-version readiness and authoritative offers.
Creation/replacement defaults an omitted cap to one; all stored responses/events contain it.
A GiST exclusion constraint on product and half-open `tstzrange` rejects overlapping non-cancelled
windows. Per-product transaction locks prevent PostgreSQL exclusion-check deadlocks across writers;
the constraint remains the final invariant. Adjacency, different products and cancelled-window reuse
are permitted. Matching creation replays return the current configuration; changed creation content,
stale mutation versions, product changes and original/current-start freeze bypasses are rejected.

Readiness compares the version, product and shared SHA-256 configuration digest. Only matching
Catalog, Admission and Inventory confirmations count. Activation, freeze and its outbox commit
atomically; interruption rolls back, replay emits once, and restart after end cannot reopen a sale.
Admission keeps a durable cap-aware projection, buffers activation before configuration, invalidates
readiness on replacement and ignores obsolete versions. Its consumer publishes SaleReady through
its own inbox/business/outbox transaction. No stock or shopper allowance engine is implemented here.

The gateway explicitly exposes the sale and Catalog read routes, checks operator JWTs, strips forged
identity headers, validates upstream schemas and preserves metadata conflict/unavailable errors.
Order-only authoritative offers are served on a separate mTLS listener. The Go-served operator
form and storefront display canonical caps/statuses and readiness; edits freeze at the original
boundary even after a later reschedule. Browser tokens remain in page memory.

The public draft adds operator `GET /v1/admin/sales/{sale_id}` and the `X-Sale-Freeze-At` response
header, because readiness polling and the original boundary were otherwise absent from the read
contract. Both promoted/source copies match. This addition is pending E4 review; it is not an
approval or a claim of compatible prior-draft rollout. See [review packet](../contract-review.md).

## Validation

- `make check`: Go formatting, vet/staticcheck, standalone builds, race-enabled unit/contract tests,
  Python tooling tests, contract/generated drift and module checksums across all ten modules.
- `make test-integration`: real PostgreSQL/Kafka and gateway contracts; create/create,
  create/replace and replace/replace races; default/frozen caps, version/readiness/digest mismatch,
  activation rollback/replay, end boundaries, private-offer authorization and no rejected outbox effect.
- `make smoke-foundation`: five live processes, fixture transports, Kafka-driven Admission readiness,
  activation and end transitions, rendered frontend/assets and Order-allowed/Gateway-denied mTLS offers.
- `make demo SCENARIO=sale-readiness` and `make demo SCENARIO=sale-overlap`: isolated fixture reports
  explicitly contain `live_peer_acceptance:false` and fail on incorrect observed outcomes.
- `make test-frontend`: runs the shipped JavaScript against a DOM boundary, checking operator access,
  default/canonical cap, original-boundary freeze, errors and authoritative-offer reads.
- `make test-browser`: pinned Playwright/Chromium, real operator APIs, fixture Catalog metadata,
  default cap, overlap rejection, adjacency/different products, cancellation/reuse, replacement,
  original-start UI freeze, escaped metadata, conflict/unavailable display and desktop/mobile captures.

SQL scenarios create unique schemas and UUID fixtures; broker tests use unique topics. Browser tests
use unique product/sale identities and cancel their own pre-start configurations while retaining audit
records. Tokens are refreshed without replacing subjects. Screenshots exclude the token value;
network traces are disabled. Evidence and reports remain under ignored `.cache/evidence/`.

The final SQL/Kafka/contract report is
`.cache/evidence/20261025T155101.015383Z/foundation-integration.jsonl`.
The successful live runtime report is
`.cache/evidence/20261025T155155.913247Z/foundation-runtime.jsonl`.
Playwright passed its complete journey in
`.cache/evidence/20261025T154002.779610Z/browser/report.json`, with four desktop/mobile screenshots
under its `artifacts/` directory. Those screenshots were visually inspected. A second Playwright run against the final rebuilt
runtimes also passed in `.cache/evidence/20261025T155349.549283Z/browser/report.json`. The two demo reports
passed in fixture mode (readiness 817ms, overlap 72ms); these durations are observations of this local
run, not performance acceptance. Earlier red runs found missing packages, a fixture-window error,
GiST deadlock, nullable failed-work rows, hidden delivery failures, absent local topics and expired
fixture tokens; the corresponding fixes were validated before recording these successful results.

## Foundation prerequisites repaired

The forward `000_platform_events_002_repair` migration preserves the applied foundation checksum,
allows nullable Kafka key/value bytes and adds fenced retry claims. A one-connection pool and
saturated workers can commit their inbox handlers; an interrupted/cancelled claimant replays safely.
Malformed keyless tombstones quarantine before acknowledgement and do not prevent a later valid key.
The schema compatibility policy keeps constrained currency dictionaries while allowing optional
record fields. Outbox publication errors are visible after their retry receipt commits. SQL/broker
checks cover lost receipts, independent destinations and retention with an outstanding receipt.
Local startup checks process identity/revision, child exits and occupied listeners.
The full dev-down/dev-up restart reapplied migrations and verified all three local topics without
changing partition counts or deleting retained volumes. The updated CI workflow includes the pinned
Node/Playwright journey; its remote execution was not triggered in this session.

## Limits and handoff

Catalog/Inventory readiness and Catalog responses are controlled fixtures. Actual E1/E2 services,
quorum/stock behavior, peer acceptance, E4 approval, cloud/HPA and purchase processing remain unverified.
Local topics have six partitions and one broker, with seven-day retention and LogAppendTime;
this does not prove broker availability or provision shared deployment resources.

`business_ready` remains false for the unfinished complete services. Readiness advertises the
implemented US1 capabilities separately. No teammate source or storage is created. The broader
foundation closure checks remain open where their complete acceptance scope is not established.
