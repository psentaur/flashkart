PYTHON ?= python3
COMPONENT ?= gateway
CONTRACT_PYTHON := .tools/contracts/bin/python

.PHONY: bootstrap fmt fmt-check lint typecheck test-unit verify-deps generate contracts-sync contracts-check contracts-integration-check tooling-check smoke-phase1 check dev-init deps-config deps-up deps-down deps-status deps-check run dev-up dev-down migrate seed wait-ready test-integration test-races test-e2e demo load cloud-preflight cloud-plan cloud-apply release

bootstrap fmt fmt-check lint typecheck test-unit verify-deps:
	$(PYTHON) scripts/project.py $@

contracts-sync:
	$(PYTHON) scripts/promote_contracts.py

generate:
	$(PYTHON) scripts/generate_contracts.py

contracts-check:
	@test -x $(CONTRACT_PYTHON) || { echo 'Run make bootstrap first' >&2; exit 1; }
	$(CONTRACT_PYTHON) scripts/validate_contracts.py

contracts-integration-check:
	@test -x $(CONTRACT_PYTHON) || { echo 'Run make bootstrap first' >&2; exit 1; }
	$(CONTRACT_PYTHON) scripts/validate_contracts.py --require-adopted

tooling-check:
	$(PYTHON) -B -m unittest discover -s scripts/tests

smoke-phase1:
	$(PYTHON) scripts/smoke_phase1.py

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

# These belong to later task phases. Never return a false passing application check.
dev-up dev-down migrate seed wait-ready test-integration test-races test-e2e demo load cloud-preflight cloud-plan cloud-apply release:
	@echo '$@ is not implemented in Phase 1; see tasks.md. Use deps-up/deps-down for local dependencies.' >&2
	@exit 2
