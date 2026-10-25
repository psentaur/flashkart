# Phase 1 local development

Mukthish's Phase 1 contains nine Go modules: four domain services, gateway, embedded frontend,
small platform helpers, draft contracts/bindings, and the integration-test module. Purchase,
readiness coordination, persistence adapters, JWT middleware and analytics processing arrive in
later task phases. Peer services and shared cloud resources are external handoffs.

## Prerequisites

Use Linux amd64/arm64, Go with automatic toolchain download enabled, Python 3.11+ with venv,
Make and a C compiler for race-enabled tests. The pinned Go toolchain is 1.27.3. The exact module,
image and tool versions/checksums are in [tool-versions.json](../dev/tool-versions.json).
Bootstrap needs HTTPS access to the official Go module proxy/sum database, PyPI and release assets.

For dependency containers use a Docker engine or an existing rootless Podman API socket. The
pinned standalone Compose client is downloaded by bootstrap; Docker CLI is optional with Podman.
If needed, start your own socket with `systemctl --user start podman.socket`. Supply DOCKER_HOST
or COMPOSE_COMMAND to select an existing compatible runtime. This repository never configures
system services or teammate runtimes for you.

## Setup and quality checks

```sh
make bootstrap
make check
make dev-init
make smoke-phase1
make deps-config
make deps-up
make deps-check
make deps-status
```

Bootstrap downloads/checks pinned tools, resolves the nine workspace modules and generates only
the Inventory Go bindings. Python contract-tool dependencies are version/hash locked. `make check`
runs formatting, vet/staticcheck, per-module compilation, source/example/generated contract checks,
race-enabled Go tests, Python environment-tool checks and module checksum verification.
`make smoke-phase1` starts each shell on a temporary loopback port, checks HTTP status/assets
and SIGTERM shutdown, then stops it. It does not imply live cross-service tests.
The feature workflow in `.github/workflows/mukthish-checks.yml` runs these checks for your branch
and pull requests; action revisions are pinned by commit. It adds no teammate build jobs.
`make fmt` intentionally applies formatting; `make fmt-check` reports drift without editing files.

`dev-init` creates an ignored mode-0600 `.env` with independent random database passwords and a
run-specific Compose project name. It never prints secrets or overwrites an existing environment.
Keep RUN_ID and passwords stable while reusing volumes. Edit ports before creating the containers;
when changing them, update the corresponding URLs/KAFKA_BROKERS in `.env` too. The checked-in
[environment example](../dev/lead.env.example) contains empty credential fields and peer addresses.
Do not commit `.env`, signing keys, seeded tokens, caches or the original design conversation.

PostgreSQL and Kafka bind host ports to loopback only, defaulting to 15432 and 19092. Three
PostgreSQL databases have distinct login owners; PUBLIC cannot connect to them. `btree_gist` is
installed only in Flash Sale's database. Kafka is one KRaft broker/controller for local development,
with six default partitions and seven-day raw retention. Application topic creation belongs to
later tasks; no peer data store, cloud resource or serverless implementation is created here.
Image tags are paired with immutable registry digests. Runtime checks do not prove broker HA.

## Run a startup shell

```sh
make run COMPONENT=admission
# Other choices: flash-sale, order, analytics, gateway.
```

Each binary validates only its required local configuration, exposes `GET /health/live` as 200,
and keeps `GET /health/ready` at 503 with `phase_1_scaffold`. The gateway serves its embedded
placeholder at `http://127.0.0.1:8080/`. Domain routes are not implemented. APP_ENV accepts only
`development` or `test` at this stage; no production readiness is claimed. Each domain process receives only its own database URL; the gateway receives no database
credentials, fixture signing key or token-file input. Missing/invalid values
fail with key-specific messages that omit secret values. SIGINT/SIGTERM drains HTTP connections
with SHUTDOWN_TIMEOUT (default 5s, permitted up to 30s).

```sh
curl --fail http://127.0.0.1:8081/health/live
curl -i http://127.0.0.1:8081/health/ready
```

Later-phase targets (`dev-up`, migrations, business seed/wait-ready, integration/race/e2e demos,
load and cloud release) deliberately exit nonzero until implemented. `test-unit` already runs
Go's race detector; `test-races` is reserved for the distributed reservation/expiry scenarios.
Use `deps-up`/`deps-down` for Phase 1 dependency containers.

## Contracts and review

```sh
make contracts-sync
make generate
make contracts-check
make contracts-integration-check
```

`contracts-sync` explicitly replaces the root draft copies from their feature source; it keeps
the six wire/fixture files byte-for-byte intact and rebases the contract-index data-model link.
`generate` regenerates Go protobuf bindings with pinned plugins. `contracts-check` compares all
seven root artifacts with that defined promotion, checks OpenAPI/events/examples, cap bounds,
canonical cap presence, the current readiness-digest fixture and generated-client drift.
`contracts-integration-check` additionally requires the recorded E4 review state in
[adoption.json](../contracts/adoption.json); pending review is an expected blocking result.
Do not edit that record to assert approval that has not occurred. Agree on contracts before
coupled service implementation. The outstanding analysis findings are listed in
[lead-boundaries.md](lead-boundaries.md); this setup does not resolve business protocol gaps.

## Stop local dependencies

```sh
make deps-down
```

This stops/removes only this run's containers/network and preserves its named volumes. No volume
reset runs implicitly. To use a fresh disposable dataset, initialize a separate environment/project;
review any data removal explicitly. Keep the environment and retained-volume identities together.
