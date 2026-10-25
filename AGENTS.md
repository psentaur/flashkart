# Repository Guidelines

## Project Structure & Module Organization

This repository is currently a planning scaffold for an eight-service flash-sale system; application source, tests, and assets have not been added.

- `docs/architecture.md` records the shared architecture, current decisions and ownership. The original conversation remains local and must not be committed.
- `.specify/templates/` contains specification, plan, and task templates.
- `.specify/scripts/python/` and `.specify/scripts/bash/` contain workflow utilities; Python is the configured script variant.
- `.specify/memory/constitution.md` is the adopted v1.0.0 engineering constitution.
- Shared behavior/data/contracts are in `specs/001-flash-sale-platform/`. Personal plans and tasks belong on contributor implementation branches; Mukthish's branch is `001-lead-implementation`. Future application directories are not implemented yet.

## Build, Test, and Development Commands

Run from the repository root:

```sh
python3 .specify/scripts/python/create_new_feature.py --help
python3 .specify/scripts/python/create_new_feature.py --short-name purchase-flow "Purchase and settle an order"
python3 .specify/scripts/python/check_prerequisites.py --json --require-spec --require-tasks --include-tasks
```

These display feature-creation options, scaffold a numbered feature, and validate an active feature's planning prerequisites, respectively. The last command requires populated feature artifacts. No application build, local server, or test command is configured; document those commands when introducing runnable services.

## Coding Style & Naming Conventions

Use descriptive Markdown headings, fenced examples, and repository-relative paths. Follow existing utility style: four-space indentation and `snake_case` names in Python. Use numbered, kebab-case feature names such as `001-purchase-flow`. No formatter or linter is configured; record language and tooling choices in the feature's `plan.md` before implementation.

## Testing Guidelines

No test framework, test-file naming convention, or coverage threshold is established. Define these and execution commands when adding code. Derive checks from specification acceptance criteria. Prioritize concurrent reservation limits, duplicate requests/events, expiry and late-payment races, and leaderless replica recovery. For documentation changes, verify paths, command options, and consistency across specs, plans, and tasks.

## Commit & Pull Request Guidelines

Use concise imperative subjects, for example, `Document inventory reservation contract`. Keep feature branches short-lived. PRs should link the relevant spec or issue, explain behavior changes, and report validation. Update affected API/event contracts and request review from their consumers before merging.
