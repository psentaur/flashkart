# Flash Sale Manager

Owned by Mukthish. Owns sale configuration, versioned readiness, schedule exclusion and authoritative offers.
Pre-start mutations commit with their events; cap and schedule freeze at the original start.
SaleConfigured/SaleActivated/SaleCancelled are relayed to Kafka through independent receipts.

From the repository root:

```sh
make bootstrap
make dev-init  # Once; preserves existing .env and volumes
make dev-up
make check test-integration smoke-foundation
make demo SCENARIO=sale-readiness
make dev-down
```

`POST /v1/admin/sales`, operator GET/PUT/cancel, public `GET /v1/sales/{sale_id}/offer`,
and Order-only mTLS `GET /internal/offers/{sale_id}` on the separate private listener.
Its database alone contains `sales` and `sale_readiness`; it never writes Inventory state.

Configuration, private identities, SQL migrations, Kafka topics and retained-state recovery are in
[development instructions](../../docs/development.md). `make dev-up` uses explicit local fixtures;
real E1/E2 acceptance and E4 contract review remain pending. [US1 evidence](../../docs/evidence/us1.md)
records verification and its limits. Restart using retained volumes; pending events replay through
inbox/outbox identities. Do not use schema rollback or delete business data as a recovery shortcut.
