# Draft contract review packet

Status: pending E4 producer/consumer review. No approvals were obtained or inferred.
Contract version remains `1.0.0-draft.2`; `contracts/adoption.json` is unchanged.

## Phase 3 review scope

- The canonical Sale/Offer/configuration events require `per_shopper_unit_cap`; SaleWrite omission
  normalizes to one on both creation and replacement. Zero, negative and unsafe integer caps fail.
- Same-product half-open windows cannot overlap. Concurrent conflicts return 409
  `SALE_WINDOW_CONFLICT`, retryable=false, with no Retry-After or rejected-configuration outbox effect.
- The readiness digest contains normalized lowercase sale/product UUIDs, version, price/currency,
  allocation, explicit cap, starts/ends and admission limits. Go recursively sorts map keys into compact
  UTF-8 JSON and formats UTC times with RFC3339Nano without redundant fractional zeroes. Dynamic status
  and readiness are excluded. The published digest vector is checked against the actual codec.
- Operator readiness polling adds `GET /v1/admin/sales/{sale_id}` returning the existing Sale schema.
  Sale admin responses add `X-Sale-Freeze-At`, the earliest original/current start boundary. This lets
  the UI freeze correctly after rescheduling without changing the public Offer or event payload shape.
- Order-only `GET /internal/offers/{sale_id}` requires an authenticated Order mTLS identity. The
  gateway cannot forward arbitrary internal paths or identity headers. Catalog remains display metadata.
- Generated protobuf/event drift and schema fixtures pass locally. Consumers must use the same cap
  and digest revision; these checks are independent fixture evidence, not peer adoption.

## Existing review questions

The two AttemptRecorded examples with aggregate identity different from `attempt_id` remain negative
runtime controls. Affected owners must agree on corrected canonical fixtures before adoption; this
packet does not silently rewrite them. Inventory ResultCode 11 (`PURCHASE_LIMIT_EXCEEDED`) and its
stable rejected-key semantics remain required before the later purchase integration.

## Migration and rollback

Do not roll cap-aware runtime data back to a cap-unaware consumer. For disposable prior-draft data,
create a fresh explicitly isolated fixture dataset, or assign cap=1 and increment configuration and
readiness versions through a reviewed migration. Historic held/confirmed totals exceeding the cap
require explicit owner reconciliation. Never delete accepted business work to force agreement.

If an owner already deployed the earlier draft, use reviewed `/v2`, protobuf v2 and `.v2` topics
with an explicit migration/rollback plan rather than in-place adoption. Only record actual approvals
from Mukthish, Member 2 and Member 3 in the adoption record. `make contracts-integration-check` must
continue to reject this pending state. Actual E1/E2 cap/readiness/expiry and compatibility evidence
remain owner handoff requirements. T097 is the later coordinated refresh, not a substitute for E4.
