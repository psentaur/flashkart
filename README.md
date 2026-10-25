# Flashkart

Flashkart is an eight-service flash-sale project for demonstrating limited-stock purchases,
asynchronous settlement, stream analytics, and leaderless catalog metadata recovery.
The repository currently contains specifications and contracts; application code is not built yet.

Start with the [architecture and ownership](docs/architecture.md),
[feature specification](specs/001-flash-sale-platform/spec.md),
[logical data model](specs/001-flash-sale-platform/data-model.md),
[service contracts](specs/001-flash-sale-platform/contracts/README.md), and
[engineering constitution](.specify/memory/constitution.md).

`main` is the shared planning baseline. Mukthish's Go/Goka/Sarama implementation plan and
100-task backlog live on [`001-lead-implementation`](https://github.com/psentaur/flashkart/tree/001-lead-implementation).
The other two members choose their own implementation stacks within the shared contracts.

```sh
git clone https://github.com/psentaur/flashkart.git
cd flashkart
git switch --track origin/001-lead-implementation
export SPECIFY_FEATURE_DIRECTORY=specs/001-flash-sale-platform
python3 .specify/scripts/python/check_prerequisites.py --json --require-spec --require-tasks --include-tasks
```

The prerequisite command is for an implementation branch with a plan and tasks. On `main`, read
shared artifacts and create your own feature/implementation plan before using implementation gates.
Spec Kit workflow utilities require Python 3; Python scripts are the configured variant. The
included agent skills are workflow instructions, not a language/framework mandate for teammates.

No application build, server or test command exists yet. Commands in the quickstart on Mukthish's
branch are implementation deliverables and are not claims of passing runtime tests.

The original conversation stays local and is ignored. Credentials, generated caches and the local
active-feature pointer are also excluded from version control. Review shared API/event changes
with affected consumers, and link behavior changes and validation evidence in pull requests.

License: [MIT](LICENSE).
