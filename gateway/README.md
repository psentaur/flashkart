# Flashkart gateway

Owned by Mukthish. Serves the Go storefront/operator pages and registered sale/Catalog routes. Operator
JWTs are checked; forged identity headers are stripped, upstream responses are schema-checked,
and metadata conflict/unavailable results remain distinct. The gateway has no database credentials.

From the repository root:

```sh
make bootstrap
make dev-init  # Once; preserves existing .env and volumes
make dev-up
make check test-integration smoke-foundation
make demo SCENARIO=sale-readiness
make dev-down
```

The storefront is `/` and the operator desk is `/operator/sales`. The gateway proxies public
product and sale-offer reads, plus operator sale creation, configuration reads/updates and
cancellation. Internal Inventory, Payment and offer paths are not publicly forwarded.

Configuration, private identities, SQL migrations, Kafka topics and retained-state recovery are in
[development instructions](../docs/development.md). `make dev-up` uses explicit local fixtures;
real E1/E2 acceptance and E4 contract review remain pending. [US1 evidence](../docs/evidence/us1.md)
records verification and its limits. Restart using retained volumes; pending events replay through
inbox/outbox identities. Do not use schema rollback or delete business data as a recovery shortcut.
