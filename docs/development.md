# Local development

Mukthish's Phases 3–4 implement sale configuration/readiness, shared SQL Admission quotas,
durable purchase keys, Order acceptance/payment success and operator/storefront/checkout/status pages.
Failure/expiry reconciliation, refunds and analytics remain later work. E4 contract review and E1/E2
real peer acceptance remain pending; local fixture behavior never substitutes for those gates.

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
make demo SCENARIO=sale-readiness
make demo SCENARIO=sale-overlap
make dev-down
```

`dev-init` creates an ignored mode-0600 `.env` with independent random database passwords and a
run-specific Compose project. It refuses to overwrite an existing environment. Keep credentials
and RUN_ID with their named volumes. No reset/delete-volume action runs implicitly. Existing
Phase 1 environments work; new optional defaults are documented in `dev/lead.env.example`.

`dev-up` defaults to explicit local fixture mode. It starts this run's PostgreSQL/Kafka, precreates the seven sale/purchase fixture topics from `dev/topics.json`, applies
each owner's foundation and applicable domain migrations, seeds disposable identities/certificates, builds five
application binaries and the contract fixture process, and waits on readiness under a deadline.
`DEV_PEER_MODE=real` fails with an explicit handoff result until reviewed peer integration exists.
The fixture process exposes no production stock, payment, replica or serverless implementation.

```sh
make deps-up       # Containers only; useful for integration tests
make deps-check    # Real logins, ownership isolation, btree_gist and Kafka API
make migrate       # Checksummed foundation and Flash Sale/Admission migrations
make topics-local  # Explicit fixture topics only; no shared/cloud provisioning
make seed PROFILE=foundation
make wait-ready
make run COMPONENT=order
make demo          # Lists sale-readiness and sale-overlap
```

Business seed profiles and unregistered demos fail explicitly. The two sale demos register through
`tests/scenarios/registry.go` and create isolated SQL/Kafka fixtures. Browser fixtures create unique
sales through the live gateway and cancel their own pre-start records after each run. `make test-races`, `test-e2e`,
`load` and cloud/release targets remain unimplemented. `test-unit` already uses the race detector;
the future `test-races` target is reserved for distributed purchase/expiry scenarios.

## Identity and service boundaries

`seed` writes one operator and 100 shoppers with 15-minute RS256 tokens to the ignored configured
`SEEDED_TOKENS_FILE`. Services receive only `JWT_PUBLIC_KEY_FILE`, issuer and audience. The
signing key is never passed to application/fixture processes. Tokens are local inputs, not a
public minting route. Rerun seed when tokens expire. Refresh preserves all 101 subjects/roles for the retained dataset;
an invalid existing manifest fails closed for repair. Seed does not create a product or sale.

Local service certificates live beside the JWT key. They use `spiffe://flashkart/service/<name>`
subjects, a disposable CA and TLS 1.3. The Inventory fixture accepts only the Order certificate.
Forwarded shopper headers are trusted only after an allowed mTLS service identity is verified.
The gateway has no database credentials. Each SQL service receives only its own database URL;
credentials cannot connect to other service databases. Cloud identity setup stays with E3.

Each application exposes `/health/live`, `/health/ready` and `/metrics`. Ready means the configured
foundation dependencies are reachable; JSON retains `mode: foundation` and `business_ready: false`
for incomplete full services, with implemented US1 capabilities listed separately. Startup detects
occupied listeners and exited children; wait-ready checks recorded process/component/revision identity.
The storefront is `http://127.0.0.1:8080/`; the operator desk is `/operator/sales`.
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
These targets use build tags `integration` and `runtime`, respectively. They now also test sale
contracts, all three schedule races, version/cap digests, activation recovery, live readiness/clock
boundaries and the separate Order-only private offer listener at `https://127.0.0.1:8442`. Override
`FLASH_SALE_INTERNAL_LISTEN_ADDR`/`FLASH_SALE_INTERNAL_URL` together when changing that port.

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

## Sale browsing and CLI browser verification

The operator desk accepts the disposable OPERATOR token from the ignored manifest, held only in
page memory. Create a future sale; omitted API cap and the editable form default to one. Readiness
must match all three participants for the current version and digest. Only Admission applies and
acknowledges configurations automatically here. Catalog/Inventory fixture responses are explicit
controlled inputs, not inferred readiness. The default Catalog fixture lists no products.

The authoritative offer remains `/v1/sales/{sale_id}/offer`; Catalog prices are display metadata.
An operator can poll `GET /v1/admin/sales/{sale_id}` for readiness. `X-Sale-Freeze-At` keeps the editor
frozen at the original start even if its visible window moved later. These draft additions and
compatibility questions are in [the review packet](contract-review.md); E4 approval is still pending.

The user requested Playwright for CLI verification. Node.js 22+ is needed only for frontend test
commands; application serving remains Go. Exact Playwright 1.64.1 and its npm integrity lock are in
`frontend/`; matching Chromium is downloaded to ignored `.cache/playwright`. This machine tested
Node 22.23.1 and Chromium revision 1248. On Playwright-supported Linux installations, install any
required browser system libraries using the official Playwright prerequisites before these commands.

```sh
make browser-bootstrap
make test-frontend
make test-browser       # Requires make dev-up; refreshes expiring fixture tokens safely
```

The browser journey exercises live sale APIs, default/revised cap, overlap errors, adjacency,
different products, cancellation/reuse and original-boundary UI freeze. Controlled Catalog browser
responses verify authoritative offer fetching, escaped text and conflict/unavailable messages.
It checks desktop/mobile overflow and saves screenshots plus a JSON report in `.cache/evidence/`.
Tokens are masked/cleared, stay out of committed fixtures, and are excluded from network traces.

For recovery, keep PostgreSQL/Kafka volumes and rerun `make dev-up`; pending destination receipts
and inbox identities are retained. Do not roll deployed schemas back: foundation repairs use a new
checksummed migration, and incomplete retries use expiring fenced claims. A publication failure now
returns an inspectable error after its retry metadata commits. See [US1 evidence](evidence/us1.md).


## Purchase slice and retained keys

Open `/checkout` or `/orders` at the gateway. Use a seeded SHOPPER token; tokens remain in page
memory. Review the authoritative offer, quantity and cap before submitting. Transport timeouts,
429 and retryable 503 retain the original request key and payload. A recorded cap rejection stays
409 with no Retry-After; an explicitly new purchase after allowance release needs a new key.
OFFER_CHANGED displays the returned terms for explicit reconfirmation. Reloading the page loses
in-memory checkout state; keep it open for retries and retain the displayed request key when
using the API directly. Never reuse a different payload with that key.

The gateway calls Admission over mTLS at `ADMISSION_INTERNAL_URL` (default
`https://127.0.0.1:8441`); Admission calls Order at `ORDER_INTERNAL_URL` (default
`https://127.0.0.1:8443`). Override the matching `*_INTERNAL_LISTEN_ADDR` with each URL. These
listeners authenticate the caller's certificate; shopper/service headers supplied publicly are
stripped. Order uses its mTLS identity for the private Flash Sale offer and Inventory gRPC.
The ordinary `ORDER_URL` serves JWT-authenticated, subject-filtered status reads. Neither Inventory
nor Payment operations are publicly proxied. Metrics include bounded
`purchase_excluded_submissions_total{reason="unauthorized|malformed|unknown_target"}` at the first
rejecting boundary; authenticated known inactive/cancelled/unready targets still emit attempts.

Admission uses configured shopper/sale counters and in-flight leases (defaults 5/100/50 and two
seconds). It samples the database clock after ordered quota row locks; expired leases cease to
consume capacity. Order retains keys for the dataset lifetime, fences five-second worker claims,
reserves before acceptance, and commits OrderPending + PaymentRequested with the Order. A lost
Reserve response is unknown; retry with the same key after REQUEST_IN_PROGRESS. Reserve replay
uses the original proposed order and trusted terms before fresh offer checks. Successful payment
confirms/queries Inventory before confirming the Order. Contradictory identities are quarantined.

```sh
make demo SCENARIO=purchase-success
make demo SCENARIO=shopper-cap
make demo SCENARIO=duplicate-replay
make test-integration
make test-frontend test-browser
```

These demos create isolated owned SQL schemas and explicitly scripted peer outcomes. The success
journey also crosses actual gRPC/Kafka. The default long-running fixture process has no general
stock/payment simulator: arbitrary purchases will not settle without matching owner responses or
explicit scripts. No fixture output certifies no-oversell, cumulative allowance or charge effects.
[US2 evidence](evidence/us2.md) records current results and the live E1/E4 gates.

Restart with retained volumes and keys. Accepted work retains outbox receipts; uncertain intents
can be retried after their lease using their original key. Phase 5 supplies the autonomous
one-second reconciliation loop, payment failure/expiry handling and refund obligations. Until
then, an unresolved successful payment is durably marked RECONCILING, and unsupported failure/refund
records remain in inspectable failed work. Do not reset keys, manually reopen reservations or
claim full failure recovery. Revert application revisions only with compatible forward migrations;
never drop retained intents/orders or roll a checksummed migration back.
