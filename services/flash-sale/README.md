# Flash Sale Manager startup shell

Owned by Mukthish. Phase 1 supplies configuration/startup and liveness only; business behavior is
scheduled in the [task backlog](../../specs/001-flash-sale-platform/tasks.md).

From the repository root, run `make bootstrap`, `make dev-init`, then `make run COMPONENT=flash-sale`.
Run `make check` for the workspace quality gates. Configuration, dependency startup, readiness,
SIGTERM shutdown and volume-preserving cleanup are in the [development guide](../../docs/development.md).
