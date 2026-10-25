# Flashkart

Flashkart is an eight-service flash-sale project for demonstrating limited-stock purchases,
asynchronous settlement, stream analytics, and leaderless catalog metadata recovery.
Mukthish's implementation branch includes sale configuration/readiness, Admission's sale projection,
shared SQL quotas, durable purchase keys, Order acceptance/payment success, checkout/status pages
and the Go authentication/SQL/Kafka foundation. Recovery/refunds and real peer acceptance remain pending.

Start with the [architecture and ownership](docs/architecture.md),
[feature specification](specs/001-flash-sale-platform/spec.md),
[logical data model](specs/001-flash-sale-platform/data-model.md),
[service contracts](specs/001-flash-sale-platform/contracts/README.md), and
[engineering constitution](.specify/memory/constitution.md).

`main` is the shared planning baseline. Mukthish's Go/Goka/Sarama implementation plan and
129-task backlog live on [`001-mukthish-implementation`](https://github.com/psentaur/flashkart/tree/001-mukthish-implementation).
The other two members choose their own implementation stacks within the shared contracts.

```sh
git clone https://github.com/psentaur/flashkart.git
cd flashkart
git switch --track origin/001-mukthish-implementation
export SPECIFY_FEATURE_DIRECTORY=specs/001-flash-sale-platform
python3 .specify/scripts/python/check_prerequisites.py --json --require-spec --require-tasks --include-tasks
```

The prerequisite command is for an implementation branch with a plan and tasks. On `main`, read
shared artifacts and create your own feature/implementation plan before using implementation gates.
Spec Kit workflow utilities require Python 3; Python scripts are the configured variant. The
included agent skills are workflow instructions, not a language/framework mandate for teammates.

For the local development environment:

```sh
make bootstrap
make check
make dev-init
make dev-up
make test-integration smoke-foundation
make demo SCENARIO=sale-readiness
make demo SCENARIO=sale-overlap
make demo SCENARIO=purchase-success
make demo SCENARIO=shopper-cap
make demo SCENARIO=duplicate-replay
make dev-down
```

Use Linux, Go toolchain download support, Python/venv, Make and a C compiler. Dependency containers
need Docker or a rootless Podman API socket. See [development instructions](docs/development.md)
for prerequisites, configuration, foundation readiness, peer-review gates and cleanup.
[Phase 3 evidence](docs/evidence/us1.md) records sale/browsing checks and
[Phase 4 evidence](docs/evidence/us2.md) records purchase, race and browser checks;
real peer acceptance and E4 review are pending. The storefront is `/` and the operator desk is
`/operator/sales`; checkout is `/checkout` and order polling is `/orders` on `http://127.0.0.1:8080`. CLI browser checks use Node.js 22+ and pinned Playwright:
`make browser-bootstrap test-frontend test-browser` while the local processes are running. The broader quickstart
contains later-phase command contracts; those targets fail clearly until implemented.

The original conversation stays local and is ignored. Credentials, generated caches and the local
active-feature pointer are also excluded from version control. Review shared API/event changes
with affected consumers, and link behavior changes and validation evidence in pull requests.

License: [MIT](LICENSE).
