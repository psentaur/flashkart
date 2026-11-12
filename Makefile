GO ?= go
MVN ?= mvn
PYTHON ?= python3
PROTOC ?= protoc
BUILD := $(CURDIR)/build
TOOLS := $(BUILD)/tools
PROTO := contracts/proto/flashkart/v1/services.proto

.PHONY: build build-go build-analytics build-web build-contract-tools test-unit test-contracts test-smoke generate-contracts
build: build-go build-analytics build-web

build-go:
	mkdir -p "$(BUILD)/bin"
	$(GO) build -buildvcs=false -o "$(BUILD)/bin/" ./cmd/...

build-analytics:
	$(MVN) -B -f analytics/pom.xml verify

build-web:
	npm --prefix web ci --no-audit --no-fund
	npm --prefix web run build

test-unit:
	$(GO) test -buildvcs=false ./...

build-contract-tools:
	mkdir -p "$(TOOLS)"
	$(GO) build -buildvcs=false -o "$(TOOLS)/protoc-gen-go" google.golang.org/protobuf/cmd/protoc-gen-go
	$(GO) build -buildvcs=false -o "$(TOOLS)/protoc-gen-go-grpc" google.golang.org/grpc/cmd/protoc-gen-go-grpc

generate-contracts: build-contract-tools
	mkdir -p gen
	$(PROTOC) -I contracts/proto \
	  --plugin=protoc-gen-go="$(TOOLS)/protoc-gen-go" \
	  --plugin=protoc-gen-go-grpc="$(TOOLS)/protoc-gen-go-grpc" \
	  --go_out=gen --go_opt=paths=source_relative \
	  --go-grpc_out=gen --go-grpc_opt=paths=source_relative "$(PROTO)"

test-contracts: build-contract-tools
	$(PYTHON) tests/contracts.py
	$(GO) test -buildvcs=false ./gen/...
	$(PYTHON) tests/reproduce_contracts.py "$(PROTOC)" "$(TOOLS)" "$(PROTO)"

test-smoke: build-go
	$(PYTHON) tests/smoke.py "$(BUILD)/bin"

COMPOSE ?= docker compose
LOCAL_COMPOSE = $(COMPOSE) -p flashkart-dev -f deploy/local/compose.yml

.PHONY: dev-up dev-down migrate seed test-persistence test-emulator
dev-up:
	$(LOCAL_COMPOSE) up -d postgres-a postgres-b postgres-c postgres-d postgres-transactional

dev-down:
	$(LOCAL_COMPOSE) --profile kafka --profile flink down

migrate:
	sh deploy/local/migrate.sh $(LOCAL_COMPOSE)

seed:
	. deploy/local/postgres.env && $(GO) run -buildvcs=false ./cmd/seed-replica

test-persistence:
	. deploy/local/postgres.env && FLASHKART_TEST_POSTGRES=1 $(GO) test -buildvcs=false -count=1 -v ./internal/replica ./internal/service

test-emulator:
	$(PYTHON) deploy/local/emulator-smoke.py

.PHONY: run-replicas test-replication-core
run-replicas: build-go
	. deploy/local/postgres.env && $(PYTHON) deploy/local/run-replicas.py "$(BUILD)/bin/catalog-replica"

test-replication-core:
	. deploy/local/postgres.env && FLASHKART_TEST_CLUSTER=1 $(GO) test -buildvcs=false -count=1 -v -run 'Test(PreferredOwners|ReadReconciliation|ReplicationCore)$$' ./internal/replica
