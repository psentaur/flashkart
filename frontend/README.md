# Flashkart frontend

Owned by Mukthish. Go-embedded templates and plain JavaScript provide the operator sale desk and shopper product
views. The form defaults cap to one, displays canonical terms/readiness, explains overlaps and
freezes at the original boundary. Tokens remain in page memory.

From the repository root:

```sh
make bootstrap
make dev-init  # Once; preserves existing .env and volumes
make dev-up
make check test-integration smoke-foundation
make demo SCENARIO=sale-readiness
make dev-down
```

The gateway serves `/` and `/operator/sales` from this module. Catalog is display metadata;
current sale terms are fetched from Flash Sale. Checkout is a later story.

Configuration, private identities, SQL migrations, Kafka topics and retained-state recovery are in
[development instructions](../docs/development.md). `make dev-up` uses explicit local fixtures;
real E1/E2 acceptance and E4 contract review remain pending. [US1 evidence](../docs/evidence/us1.md)
records verification and its limits. Restart using retained volumes; pending events replay through
inbox/outbox identities. Do not use schema rollback or delete business data as a recovery shortcut.

CLI browser tests need development-only Node.js 22+:

```sh
make browser-bootstrap
make test-frontend test-browser
```

`frontend/package-lock.json` pins Playwright; Chromium and reports/screenshots stay in ignored
`.cache/`. The browser journey uses real sale APIs and controlled Catalog metadata. Browser tests
refresh expiring local tokens without changing subjects, disable network traces and cancel their
own pre-start sale fixtures. Node/Playwright are not application runtime dependencies.
