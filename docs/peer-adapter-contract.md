# Peer adapter contract

Mukthish owns the harness; E1/E2 owners supply their adapters and retain their runtime/storage
choices. Copy `dev/adapters.example.json` to an ignored local file and supply credentials through
its named environment variables. No database URL or migration access is accepted. `start` is an
owner-supplied argv array; an empty array means start the external service yourself. The harness
never invents a peer implementation. Missing URL, credential, capability, health or verifier data
must yield an explicit `adapters.Unavailable` result, never a skipped acceptance pass.

Use `mode=fixture` for `tests/doubles`. These doubles script Reserve outcomes, terminal states,
Payment results, Catalog responses and per-subject notices. They prove consumer behavior only.
The test controls are Go interfaces/failpoints, not public production endpoints. E4 approval in
`contracts/adoption.json` and `mode=real` are separate prerequisites for coupled acceptance.

## Health and fault inputs

Each adapter declares an HTTP(S) URL, credential environment name, GET health path, POST fault
path and GET read-only verification path. Health success is 200; unavailable is 503. Fault actions
require owner authorization and a run ID. E1 must support `advance-clock` (UTC instant),
`pause-cleanup` (boolean), and `restart`; acknowledgements report effective clock/cleanup state.
Payment supports recorded success/failure/delay and restart. E2 supplies replica reachability,
repair/conflict controls and Notification event invocation. These controls are outside public
shopper routing. Wait on reported state with a deadline; do not infer completion from elapsed time.

## Inventory verification response

The read-only stock snapshot contains `allocated_units`, `available_units`, `held_units`,
`sold_units`, `shopper_sale_ledgers` and `stable_reserve_results`. Each shopper/sale row contains
`shopper_id`, `sale_id`, `unexpired_held_units`, `confirmed_units`, `per_shopper_unit_cap`.
Include all reservations, including orphan holds and holds awaiting delayed cleanup. Sample time
inside the owner's protected decision and report it in the adapter evidence. Verify:

```text
available_units + held_units + sold_units = allocated_units
unexpired_held_units + confirmed_units <= per_shopper_unit_cap
```

Stable Reserve results include proposed order ID, request payload identity, recorded rejection
or reservation ID and current terminal state. Matching successful and rejected retries must
retain identity; distinct keys must serialize cumulative allowance. Exercise cap=1 and larger
caps, concurrent calls, expiry with cleanup paused, confirmation, release, orphan hold and restart.
The verifier does not inspect or migrate the owner's database. Fixture success is never E1 proof.

## Phase 4 complete-dataset verification boundary

`tests/us2.VerifyOwner` is separate from fixture scenario checks. Supply an explicit
`OwnerExpectation` (run/sale/product, allocated units, cap, all tested shopper and proposed Order
IDs) and the real Inventory adapter after E4 adoption. The response must additionally identify
`run_id`, `sale_id`, `product_id`, protected UTC `sampled_at`, and a complete `reservations` array.
Each reservation contains reservation/order/shopper/sale/product IDs, quantity, state and UTC
expiry. `stable_reserve_results` must be an array containing proposed `order_id`, the stable
64-character hexadecimal `payload_hash`, `result_code` and optional `reservation_id`.

The verifier compares reservation totals with held/sold stock, derives unexpired held/confirmed
allowance at the supplied authoritative instant, checks every tested shopper/order and rejects
rejections containing holds. Run it after each owner-controlled reserve/confirm/release/expiry/
restart transition and compare stable outcomes across samples. Fault acknowledgements and the
complete real acceptance orchestration remain external inputs; no such run is claimed in Phase 4.
An empty/null/mismatched snapshot is insufficient evidence, not a passed test. The default generic
`Snapshot.Verify` alone does not establish this complete-dataset guarantee (T120 remains open).
