# Local foundation development

Mukthish's Phase 2 implements T009–T020 on top of the Phase 1 workspace. Four domain entry points,
gateway/frontend, shared transport/persistence helpers, contract fixtures and development commands
are available. Purchase flows, sale configuration, quotas and analytics processing begin in story
phases. E4 contract review and E1/E2 real peer acceptance remain pending.

## Prerequisites and setup

Use Linux amd64/arm64, Go toolchain download support, Python 3.11+ with venv, Make and a C compiler.
Go 1.27.3 and all dependency/tool/image checksums are recorded in `dev/tool-versions.json`.
Bootstrap needs access to the Go proxy/sum database, PyPI and pinned release assets. Docker or a
rootless Podman API is required for real PostgreSQL/Kafka. Start your existing socket yourself or
supply `DOCKER_HOST`/`COMPOSE_COMMAND`; these commands do not configure system services.

```sh
make bootstrap
make check
make dev-init
make dev-up
make test-integration smoke-foundation
make dev-down
```

`dev-init` creates an ignored mode-0600 `.env` with independent random database passwords and a
run-specific Compose project. It refuses to overwrite an existing environment. Keep credentials
and RUN_ID with their named volumes. No reset/delete-volume action runs implicitly. Existing
Phase 1 environments work; new optional defaults are documented in `dev/lead.env.example`.

`dev-up` defaults to explicit local fixture mode. It starts this run's PostgreSQL/Kafka, applies
only each owner's foundation migration, seeds disposable identities/certificates, builds five
application binaries and the contract fixture process, and waits on readiness under a deadline.
`DEV_PEER_MODE=real` fails with an explicit handoff result until reviewed peer integration exists.
The fixture process exposes no production stock, payment, replica or serverless implementation.

```sh
make deps-up       # Containers only; useful for integration tests
make deps-check    # Real logins, ownership isolation, btree_gist and Kafka API
make migrate       # Checksummed transactional foundation migrations in three owned databases
make seed PROFILE=foundation
make wait-ready
make run COMPONENT=order
make demo          # Lists registered scenarios; currently []
```

Business seed profiles and unregistered demos fail explicitly. Story phases register scenarios
through `tests/scenarios/registry.go` and add their own fixtures. `make test-races`, `test-e2e`,
`load` and cloud/release targets remain unimplemented. `test-unit` already uses the race detector;
the future `test-races` target is reserved for distributed purchase/expiry scenarios.

## Identity and service boundaries

`seed` writes one operator and 100 shoppers with 15-minute RS256 tokens to the ignored configured
`SEEDED_TOKENS_FILE`. Services receive only `JWT_PUBLIC_KEY_FILE`, issuer and audience. The
signing key is never passed to application/fixture processes. Tokens are local inputs, not a
public minting route. Rerun seed when tokens expire. Seed does not create a product or sale.

Local service certificates live beside the JWT key. They use `spiffe://flashkart/service/<name>`
subjects, a disposable CA and TLS 1.3. The Inventory fixture accepts only the Order certificate.
Forwarded shopper headers are trusted only after an allowed mTLS service identity is verified.
The gateway has no database credentials. Each SQL service receives only its own database URL;
credentials cannot connect to other service databases. Cloud identity setup stays with E3.

Each application exposes `/health/live`, `/health/ready` and `/metrics`. Ready means the foundation
dependencies are reachable and SQL migrations are present. Its JSON explicitly includes
`mode: foundation` and `business_ready: false`. No business routes or complete-service claim is
implied. The gateway's embedded placeholder remains at `http://127.0.0.1:8080/`.
Structured request logs carry correlation UUIDs; metrics use bounded route/status/reason labels.
No raw token, request body, database URL or shopper ID is a metric label.

Logs and process records are under ignored `.cache/`. `dev-down` signals only recorded binaries,
waits for shutdown, then stops the current Compose project while retaining named volumes and keys.
It does not kill arbitrary listeners. Use `deps-down` to stop only dependency containers.
`make smoke-phase1` now points to the current smoke command and exits nonzero; its original
startup evidence remains in `docs/evidence/phase1.md`.

## Contracts and checks

```sh
make fmt-check lint typecheck contracts-check
make test-unit
make test-integration
make smoke-foundation   # Requires make dev-up
```

Checks enumerate ten modules, including the Phase 2 Go tooling module under `scripts/`; workspace
root `go test ./...` does not cover them. Compiler checks also run with `GOWORK=off`. Bootstrap
retains the intentionally pinned Phase 1 libraries even when Go reconciles unused future imports.
`make check` includes Python configuration tests, module checksums and generated-binding drift.

`test-integration` uses real local PostgreSQL/Kafka and fails when either is unavailable. It checks
transactional DDL rollback, checksum drift, inbox repeat handling, business/outbox rollback,
post-publication crash recovery, independent destination receipts, delayed retries, quarantine,
audited replay and Kafka group offset ordering/redelivery. `smoke-foundation` checks five live
processes, honest foundation readiness, metrics, route isolation and allowed/denied gRPC mTLS
subjects. Both targets retain Go JSON test events under run-specific `.cache/evidence/` directories.
These targets use build tags `integration` and `runtime`, respectively.

Root contracts remain promoted from `specs/001-flash-sale-platform/contracts/`. `make generate`
regenerates protobuf bindings and typed event payloads. `contracts-check` validates schemas,
fixtures, cap/digest controls and both generated outputs. Runtime event consumers accept optional
additions on known majors and quarantine unknown/invalid records. Two existing AttemptRecorded
shape examples have inconsistent aggregate identities; runtime tests require their rejection.
No shared wire schema or approval record was changed to hide that inconsistency.

`make contracts-integration-check` intentionally fails pending E4 review in
`contracts/adoption.json`. See `docs/peer-adapter-contract.md` for real owner inputs and verifier
requirements. Fixture mode never satisfies those acceptance gates.

## Recovery and audited replay

Inbox/business/outbox effects share one owner transaction. Kafka offsets follow commit. Each
outbox record retains separate domain and analytics receipts; incomplete receipts remain pending
and completed records remain for at least eight days. A crash after publish replays the original
event ID. Retry work retains original bytes, headers, topic, partition and offset; failed work is
inspectable through `messaging.RetryStore.Inspect`. Five retries use 1/2/4/8/16-second delays;
dependency-wide outages pause intake/obligations instead of spending that budget.

After inspecting and repairing a quarantined dependency, supply a valid operator token through
`REPLAY_OPERATOR_TOKEN` and a reason:

```sh
python3 scripts/command.py replay --owner order --id 42 --reason 'dependency repaired'
```

Replay schedules the same record and commits an operator audit row. It never edits the original
payload or removes a business obligation. Consumer workers in story phases use these primitives;
there is no generic public replay endpoint. Reconciliation of purchase/refund obligations arrives
with those stories.
