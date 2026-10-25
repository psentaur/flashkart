# Repository Guidelines

## Project Structure & Module Organization

This repository contains shared planning artifacts and Mukthish's Phase 4 sale/browsing/purchase implementation and Go foundation for an eight-service flash-sale system. Admission and the Order happy path are implemented against contract fixtures; recovery/refunds and real peer acceptance remain pending.

- `docs/architecture.md` records the shared architecture, current decisions and ownership. The original conversation remains local and must not be committed.
- `.specify/templates/` contains specification, plan, and task templates.
- `.specify/scripts/python/` and `.specify/scripts/bash/` contain workflow utilities; Python is the configured script variant.
- `.specify/memory/constitution.md` is the adopted v1.0.0 engineering constitution.
- Shared behavior/data/contracts are in `specs/001-flash-sale-platform/`. Personal plans and tasks belong on contributor implementation branches; Mukthish's branch is `001-mukthish-implementation`. Foundation runtimes and test fixtures are under `services/`, `gateway/`, `frontend/`, `internal/platform/`, `contracts/` and `tests/`; teammate source directories are not created.

## Build, Test, and Development Commands

Run from the repository root:

```sh
python3 .specify/scripts/python/create_new_feature.py --help
python3 .specify/scripts/python/create_new_feature.py --short-name purchase-flow "Purchase and settle an order"
python3 .specify/scripts/python/check_prerequisites.py --json --require-spec --require-tasks --include-tasks
```

These display feature-creation options, scaffold a numbered feature, and validate an active feature's planning prerequisites, respectively. The last command requires populated feature artifacts. Foundation commands are `make bootstrap`, `make check`, `make dev-init`, `make deps-up`, `make deps-check`, `make dev-up`, `make test-integration`, `make smoke-foundation` and `make dev-down`; see `docs/development.md`. Business scenario and cloud targets fail clearly until implemented.

## Coding Style & Naming Conventions

Use descriptive Markdown headings, fenced examples, and repository-relative paths. Follow existing utility style: four-space indentation and `snake_case` names in Python. Use numbered, kebab-case feature names such as `001-purchase-flow`. Mukthish's Go code uses gofmt, go vet, pinned staticcheck and `_test.go` files. Run `make fmt-check lint typecheck test-unit`; language/tool choices remain documented in the feature plan.

## Testing Guidelines

Use Go's testing package and `_test.go` files for Mukthish's code. `make test-unit` enumerates all workspace modules and uses the race detector. No numeric coverage threshold replaces behavior checks. Derive checks from specification acceptance criteria. Prioritize concurrent reservation limits, duplicate requests/events, expiry and late-payment races, and leaderless replica recovery. For documentation changes, verify paths, command options, and consistency across specs, plans, and tasks.

## Commit & Pull Request Guidelines

Use concise imperative subjects, for example, `Document inventory reservation contract`. Keep feature branches short-lived. PRs should link the relevant spec or issue, explain behavior changes, and report validation. Update affected API/event contracts and request review from their consumers before merging.
