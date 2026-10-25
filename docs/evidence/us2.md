# US2 / Phase 4 evidence

**Date**: 2026-10-26 (Asia/Kolkata; artifact names use UTC).
**Scope**: T031–T045 and purchase consolidation checks T105/T115, Mukthish's code only.
**Result**: PASS for the local purchase slice with real owned PostgreSQL, actual gRPC/Kafka and
controlled peer responses. Actual E1 stock/allowance/charge guarantees and E4 review are unverified.

## Implemented behavior and checks

| Tasks | Verified behavior |
| --- | --- |
| T031 | Purchase/header/service authorization, pending/terminal replay, payload conflict, changed offer, cap rejection and subject-filtered status contracts |
| T032 | Shared configured shopper/sale/in-flight limits, repeat-safe release, crashed lease expiry, 429/Retry-After, closed SQL 503 and eligible/excluded traffic |
| T033 | 50 concurrent matching requests share one intent/Reserve/payment request; distinct-key scripted cap outcomes, lost Reserve response, restart and interrupted acceptance |
| T034 | Owned quota, lease, attempt and final response/error/latency schema with two-second leases |
| T035 | SALE-before-SHOPPER_SALE locking, shared database time after locks, repeat-safe capacity release/reclamation |
| T036 | Durable PurchaseIntent, Order, payment/workflow identity and owner schema |
| T037 | Shopper-scoped canonical payload hashing, retained accepted/rejected keys, proposed Order IDs and stale-worker fencing |
| T038 | Reserve/Get/Confirm/Release client preserves operation IDs, bounded deadlines, UUID correlation and current terminal-state replay; ResultCode 11 stays a rejection |
| T039 | Explicit cap-aware authoritative offers, no acceptance before a live hold, Order + PaymentRequested/OrderPending atomic commit, durable cap/stock/offer rejections |
| T040 | Known inactive/cancelled/unready attempts count; unauthorized/malformed/unknown/mismatched traffic is excluded once; downstream cap failure remains ADMITTED/NONE + final 409/error |
| T041 | Duplicate/semantic duplicate payment success confirms/queries Inventory first; lost successful Confirm response is recovered without duplicate OrderConfirmed |
| T042 | Admission-only internal Order POST, Location/attempt/expiry response, JWT subject-filtered GET and 202 pending / 200 terminal replay |
| T043 | Explicit public allowlist, forged identity stripping, shopper validation and bounded exclusion metrics |
| T044 | Checkout cap/error display, retained keys across timeouts, explicit new purchase, changed-offer reconfirmation, in-memory tokens and status polling |
| T045 | Runnable purchase-success, shopper-cap and duplicate-replay scenarios; cap=1 and cap=2/quantity=2 scripts, 100 distinct shoppers, 1000 terminal replays; explicit live-verifier boundary |

T031–T033 entry points first failed because the purchase test package/implementation was absent.
A later deterministic regression held a quota SQL lock, advanced controlled time and demonstrated
that the initial implementation used a stale pre-lock timestamp. Sampling the shared database
clock after acquiring both locks fixes that failure. No arbitrary sleep determines these races.

The public integration journey uses separate isolated Order/Admission SQL schemas, HTTP service
boundaries and an actual gRPC Inventory client. It dispatches the real SQL outbox to an isolated
Kafka topic, replays a scripted Payment result from Kafka, commits inbox/Order/outbox state and
verifies terminal public replay. Service identity in that in-process journey is a controlled
boundary; the running-service smoke separately checks actual private-listener mTLS authorization.

## Validation performed

All commands below passed:

```sh
make fmt-check lint typecheck contracts-check tooling-check verify-deps
make test-unit
make test-integration
make smoke-foundation
make test-frontend test-browser
make demo SCENARIO=purchase-success
make demo SCENARIO=shopper-cap
make demo SCENARIO=duplicate-replay
python3 .specify/scripts/python/check_prerequisites.py --json --require-tasks --include-tasks
```

The Go quality/race commands enumerate all ten modules. Standalone compiler checks use
`GOWORK=off`; existing dependency pins/checksums are retained. The contract validator reports
three OpenAPI documents, 24 event examples, five HTTP examples, 33 rejection controls,
cap-aware digest and generated/source drift checks. Python tooling has 13 passing checks.

Final integration/runtime JSONL reports are local under:

- `.cache/evidence/20261025T205734.485186Z/foundation-integration.jsonl`
- `.cache/evidence/20261025T205749.701306Z/foundation-runtime.jsonl`

Both Chromium journeys pass. Checkout tests use controlled HTTP response fixtures; US1 still uses
live sale APIs. Desktop/mobile screenshots and the JSON browser report are under
`.cache/evidence/20261025T204945.348808Z/browser/`. They verify the shipped pages, timeout retry
identity, cap errors, explicit new purchase, offer reconfirmation, token input clearing, order
polling, escaped rendering and mobile overflow. The checkout mobile screenshot was visually
inspected. Browser fixtures do not establish purchase backend invariants.

The demos report `mode=fixture`, `live_peer_acceptance=false` and explicitly unverified E1 stock/
allowance evidence. Observed fixture durations were approximately 0.96 s (purchase-success),
1.56 s (shopper-cap) and 2.51 s (duplicate-replay). These are scenario runtimes, not load or p95
acceptance measurements. Initial dependency-unavailable runs failed; PostgreSQL/Kafka were started
before the passing runs. Local owned migrations and all five application processes were verified.

## Remaining acceptance and recovery boundaries

- **E1 / Member 2**: No live Inventory/Payment adapter or charge/stock/allowance ledger was supplied.
  SC-002/SC-004/SC-005 actual stock/cap/settlement acceptance remains unverified. Scripted distinct-key
  replies, terminal state changes and 100 shopper acceptances test consumer behavior only.
- **E4 / affected consumers**: `1.0.0-draft.2` adoption/review remains pending. No contract approval,
  merge, deployment, producer compatibility or actual multi-process peer guarantee is implied.
- **Phase 5**: Autonomous one-second reconciliation, failure/expiry handling, late success/refunds
  and complete recovery remain unimplemented. A successful payment with unresolved Inventory state
  is durably retained as RECONCILING; other unsupported outcomes remain inspectable failed work.
- **Later phases / E2/E3**: Notices, analytics aggregation, HPA/load/cloud acceptance are not tested here.

`tests/us2/owner.go` separates live verification from fixtures. It rejects fixture mode and
missing/mismatched run/sale/product/time, incomplete shopper/reservation ledgers, missing proposed
orders/outcomes and rejection-with-hold evidence. Unit tests demonstrate that incomplete responses
cannot pass. Actual callers must supply the owner adapter, explicit expected dataset and E4 gate;
no live verifier run was performed. The broader T120 owner-adapter remediation remains open.

Keep the local database/Kafka volumes, service identities and request keys across restart. Retry
an uncertain purchase with its original key. Do not clear business state or reopen an Inventory
hold. See [development/recovery instructions](../development.md#purchase-slice-and-retained-keys).
