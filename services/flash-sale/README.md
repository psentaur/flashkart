# Flash Sale Manager foundation runtime

Owned by Mukthish. Phase 2 supplies dependency readiness, metrics and graceful shutdown; business behavior is
scheduled in the [task backlog](../../specs/001-flash-sale-platform/tasks.md).

From the repository root, run `make bootstrap`, `make dev-init`, then `make dev-up`.
Use `make dev-down` to stop the managed local processes and dependencies while retaining volumes.
Run `make check` for the workspace quality gates. Configuration, dependency startup, readiness,
SIGTERM shutdown and volume-preserving cleanup are in the [development guide](../../docs/development.md).
