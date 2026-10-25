PYTHON ?= python3
COMPONENT ?= gateway
PROFILE ?= foundation
CONTRACT_PYTHON := .tools/contracts/bin/python

.PHONY: bootstrap fmt fmt-check lint typecheck test-unit verify-deps generate contracts-sync contracts-check contracts-integration-check test-browser browser-bootstrap test-frontend tooling-check smoke-phase1 smoke-foundation check dev-init deps-config deps-up deps-down deps-status deps-check run dev-up dev-down topics-local migrate seed wait-ready test-integration test-races test-e2e demo load cloud-preflight cloud-plan cloud-apply release

bootstrap fmt fmt-check lint typecheck test-unit verify-deps:
	$(PYTHON) scripts/project.py $@

contracts-sync:
	$(PYTHON) scripts/promote_contracts.py

generate:
	$(PYTHON) scripts/generate_contracts.py
	$(PYTHON) scripts/generate_events.py

contracts-check:
	@test -x $(CONTRACT_PYTHON) || { echo 'Run make bootstrap first' >&2; exit 1; }
	$(CONTRACT_PYTHON) scripts/validate_contracts.py

contracts-integration-check:
	@test -x $(CONTRACT_PYTHON) || { echo 'Run make bootstrap first' >&2; exit 1; }
	$(CONTRACT_PYTHON) scripts/validate_contracts.py --require-adopted

browser-bootstrap:
	npm ci --prefix frontend --cache .cache/npm --ignore-scripts --no-audit --no-fund
	PLAYWRIGHT_BROWSERS_PATH=$(CURDIR)/.cache/playwright frontend/node_modules/.bin/playwright install chromium

test-browser:
	$(PYTHON) scripts/test_browser.py

test-frontend:
	node --test frontend/tests/*.test.cjs

tooling-check:
	$(PYTHON) -B -m unittest discover -s scripts/tests

smoke-phase1:
	@echo "Phase 1 startup evidence is historical; use make dev-up smoke-foundation for the current runtime." >&2
	@exit 2

smoke-foundation:
	$(PYTHON) scripts/smoke_foundation.py

check: fmt-check lint typecheck tooling-check contracts-check test-unit verify-deps

dev-init:
	$(PYTHON) scripts/dev.py init

deps-config:
	$(PYTHON) scripts/dev.py config

deps-up:
	$(PYTHON) scripts/dev.py up

deps-down:
	$(PYTHON) scripts/dev.py down

deps-status:
	$(PYTHON) scripts/dev.py status

deps-check:
	$(PYTHON) scripts/dev.py check

run:
	$(PYTHON) scripts/dev.py run --component $(COMPONENT)

# Seed refreshes identities; business fixture datasets are owned by their story scenarios.
seed:
	@test "$(PROFILE)" = foundation || { echo "Business seed profiles arrive in story phases; use PROFILE=foundation" >&2; exit 2; }
	$(PYTHON) scripts/command.py seed

dev-up dev-down topics-local migrate wait-ready:
	$(PYTHON) scripts/command.py $@

test-integration:
	$(PYTHON) scripts/test_foundation.py

demo:
	$(PYTHON) scripts/command.py demo $(SCENARIO)

# Business races, browser journeys, load and cloud deployment arrive in later phases.
test-races test-e2e load cloud-preflight cloud-plan cloud-apply release:
	@echo '$@ belongs to later phases; see tasks.md. Use test-integration for the implemented sale races.' >&2
	@exit 2
