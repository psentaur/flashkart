# Admission sale projection

Owned by Mukthish. Applies SaleConfigured/SaleActivated/SaleCancelled into its own durable SQL projection and
publishes cap-aware SaleReady. Early activation is buffered; stale versions and ended windows
cannot authorize an active projection. Phase 4 adds shared SQL quotas/expiring leases, authenticated
known-target purchase forwarding and durable final AttemptRecorded/outbox publication.

From the repository root:

```sh
make bootstrap
make dev-init  # Once; preserves existing .env and volumes
make dev-up
make check test-integration smoke-foundation
make demo SCENARIO=sale-readiness
make dev-down
```

The private mTLS `/v1/purchases` route accepts verified Gateway shoppers. Known inactive/cancelled/
unready projections still produce eligible attempts. Unknown/mismatched targets produce only a
bounded exclusion counter. Defaults are 5 attempts/shopper/second, 100/sale/second and 50 in-flight
forwards, with two-second leases and a 1.5-second forwarding budget. Lease release is repeat-safe. Stock and shopper allowance remain Inventory responsibilities.

Configuration, private identities, SQL migrations, Kafka topics and retained-state recovery are in
[development instructions](../../docs/development.md). `make dev-up` uses explicit local fixtures;
real E1/E2 acceptance and E4 contract review remain pending. [US1 evidence](../../docs/evidence/us1.md)
records verification and its limits. Restart using retained volumes; pending events replay through
inbox/outbox identities. Do not use schema rollback or delete business data as a recovery shortcut.

[Purchase configuration, retry/recovery and private listener defaults](../../docs/development.md#purchase-slice-and-retained-keys)
and [US2 evidence](../../docs/evidence/us2.md) cover the purchase slice.
