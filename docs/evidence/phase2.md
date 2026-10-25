# Phase 2 foundation evidence

Date: 2026-10-25. Owner: Mukthish. Scope: T009–T020 on `001-mukthish-implementation`.
Phase 1 was committed and pushed separately as `1dc53b7` before Phase 2 implementation continued.

## Implemented and verified

- Value/schema boundaries, exact integer money/overflow/currency checks, UTC/UUID/request keys;
  JWT RS256 issuer/audience/expiry/role/lifetime checks and authenticated service forwarding.
- Explicit clocks, identity generation and cancellable failpoints; JSON error/validation helpers,
  correlation propagation and remaining-deadline forwarding, including stable 409 cap rejection.
- Per-owner SQL pools, transactional checksum-tracked migrations and inbox/business/outbox commits;
  independent destination receipts and at-least-eight-day completed outbox retention.
- Typed event bindings with generated drift checks, known-major optional additions and quarantine
  for unknown kinds/majors, invalid payloads, producers, topic keys and aggregate identities.
- Checked idempotent Sarama configuration and consumer acknowledgement after durable handling;
  durable delayed retries, inspectable quarantine, paused outages and authorized audited replay.
- Scripted Inventory/Payment/Catalog/Notification fixtures, real-owner adapter/verifier inputs,
  lifecycle health/metrics/logging, local identity/certificate seed and scenario command registry.

`make check` passes formatting, vet/staticcheck, standalone builds, race-enabled tests, Python
configuration checks, schema/example/generated checks and module checksum verification across ten
modules. The tooling module under `scripts/` is the sole addition to Phase 1's nine modules.
`make test-integration` passes against actual local PostgreSQL 17/Kafka 4.0.2:

| Check | Observed result |
| --- | --- |
| Migration interruption/checksum | Failed DDL rolls back; repeat succeeds; changed checksum rejected |
| Inbox/business/outbox interruption | Pre-commit crash leaves no business effect; matching replay applies once |
| Post-publish interruption | Original event repeats; domain and analytics receipts both eventually persist |
| Retry/replay | Explicit clock advances 1/2/4/8/16 seconds; fifth retry failure quarantines; operator replay audited |
| Dependency-wide outage | Work pauses without consuming retry attempts and resumes explicitly |
| Kafka group failure/restart | Handler failure leaves offset uncommitted; new group instance redelivers; success commits |

`make dev-up` applies each owned foundation migration, seeds identities and starts the five
application processes plus one fixture process. `make smoke-foundation` passes health, readiness,
metrics and route isolation; a trusted Order certificate reaches the Inventory fixture while a
trusted Gateway certificate receives PERMISSION_DENIED. `business_ready` remains false. Local
HTTP, one broker and a fixture process are not cloud, HA, peer or purchase-acceptance evidence.

The final run retained JSON test events at
`.cache/evidence/20261025T140510.636872Z/foundation-integration.jsonl` and
`.cache/evidence/20261025T140514.135321Z/foundation-runtime.jsonl`. `make dev-down` then
stopped the task processes/containers and removed their network while retaining credentials,
keys and volumes. `make contracts-integration-check` failed as expected pending E4; unknown
business demos and `seed PROFILE=baseline` also failed explicitly. This records validation
before publication. The subsequent convergence assessment records remaining implementation gaps
as T113–T129 in Phase 12 of `specs/001-flash-sale-platform/tasks.md`.

## Design choices and limits

Runtime validation uses pinned `github.com/santhosh-tekuri/jsonschema/v6` v6.0.2 to enforce the
same draft-2020-12 schemas and conditional required fields as contract CI. This avoids a second,
partial handwritten validator. Source: [validator documentation](https://github.com/santhosh-tekuri/jsonschema/tree/v6.0.2).
Generated typed bindings live in the shared platform; wire schemas remain unchanged.

Two existing AttemptRecorded shape fixtures have aggregate IDs different from their attempt IDs.
The codec rejects them as negative semantic controls. This preserves the previously recorded E4
follow-up rather than silently changing shared examples or accepting inconsistent identities.
Aggregate monotonicity across events remains the responsibility of each later domain consumer.

The scenario registry has no business scenarios yet; unknown scenarios and business seed profiles
fail explicitly. T021–T100, coupled contract adoption, real E1/E2 guarantees, cloud deployment,
Goka processing, HPA/load and complete purchase behavior remain unimplemented/unverified.
