# Admission sale projection

Owned by Mukthish. Applies SaleConfigured/SaleActivated/SaleCancelled into its own durable SQL projection and
publishes cap-aware SaleReady. Early activation is buffered; stale versions and ended windows
cannot authorize an active projection. Quotas and purchase forwarding arrive in Phase 4.

From the repository root:

```sh
make bootstrap
make dev-init  # Once; preserves existing .env and volumes
make dev-up
make check test-integration smoke-foundation
make demo SCENARIO=sale-readiness
make dev-down
```

No purchase route is exposed yet. Known inactive/cancelled projections are retained for later
attempt eligibility. Stock and shopper allowance remain Inventory responsibilities.

Configuration, private identities, SQL migrations, Kafka topics and retained-state recovery are in
[development instructions](../../docs/development.md). `make dev-up` uses explicit local fixtures;
real E1/E2 acceptance and E4 contract review remain pending. [US1 evidence](../../docs/evidence/us1.md)
records verification and its limits. Restart using retained volumes; pending events replay through
inbox/outbox identities. Do not use schema rollback or delete business data as a recovery shortcut.
