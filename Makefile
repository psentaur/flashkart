GO ?= go
MVN ?= mvn
PYTHON ?= python3
PROTOC ?= protoc
BUILD := $(CURDIR)/build
TOOLS := $(BUILD)/tools
PROTO := contracts/proto/flashkart/v1/services.proto

.PHONY: build build-go build-analytics build-web test-unit test-contracts test-smoke generate-contracts
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

generate-contracts:
	mkdir -p "$(TOOLS)" gen
	$(GO) build -buildvcs=false -o "$(TOOLS)/protoc-gen-go" google.golang.org/protobuf/cmd/protoc-gen-go
	$(GO) build -buildvcs=false -o "$(TOOLS)/protoc-gen-go-grpc" google.golang.org/grpc/cmd/protoc-gen-go-grpc
	$(PROTOC) -I contracts/proto \
	  --plugin=protoc-gen-go="$(TOOLS)/protoc-gen-go" \
	  --plugin=protoc-gen-go-grpc="$(TOOLS)/protoc-gen-go-grpc" \
	  --go_out=gen --go_opt=paths=source_relative \
	  --go-grpc_out=gen --go-grpc_opt=paths=source_relative "$(PROTO)"

test-contracts: generate-contracts
	$(PYTHON) tests/contracts.py
	$(GO) test -buildvcs=false ./gen/...
	$(PYTHON) tests/reproduce_contracts.py "$(PROTOC)" "$(TOOLS)" "$(PROTO)"

test-smoke: build-go
	$(PYTHON) tests/smoke.py "$(BUILD)/bin"
