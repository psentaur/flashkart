# Order purchase service

Owned by Mukthish. Phase 4 claims shopper/request keys durably, checks the authoritative Flash
Sale offer, calls Inventory over gRPC, then commits a pending Order and PaymentRequested together.
Accepted keys replay the same Order; durable cap/stock/offer rejections replay unchanged. Successful
payment confirms/queries Inventory before emitting OrderConfirmed. Shopper GET is subject-filtered.
Only Admission's mTLS identity can POST `/internal/orders`.

From the repository root, run `make bootstrap`, `make dev-init` once, then `make dev-up`.
Use `make check test-integration smoke-foundation`, `make demo SCENARIO=purchase-success` and
`make demo SCENARIO=duplicate-replay`. `make dev-down` stops local processes/dependencies and
retains volumes and request keys.

Configuration includes `ORDER_DATABASE_URL`, Kafka, Inventory gRPC, JWT public key/certificates,
`FLASH_SALE_INTERNAL_URL`, and the private `ORDER_INTERNAL_LISTEN_ADDR`/`ORDER_INTERNAL_URL`.
See the [development guide](../../docs/development.md#purchase-slice-and-retained-keys) for defaults,
startup, metrics and recovery. [US2 evidence](../../docs/evidence/us2.md) separates scripted peer
responses from pending E1 acceptance and E4 adoption. Order never owns stock/allowance counters.

Keep keys and SQL/outbox state across restart. Retries use the original proposed Order ID and
fenced five-second claims. Phase 5 still owns autonomous reconciliation, failure/expiry transitions
and refunds; unresolved successful payments remain pending/reconciling. No full saga recovery or
live charge/refund acceptance is claimed. Roll forward with checksummed migrations; do not delete
intents/orders or roll schemas back as a recovery shortcut.
