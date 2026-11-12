# Phase 1 development

Use Go 1.27.2, JDK 17, Maven 3.9.11, the Node release in `.nvmrc`, Python 3 with pip, and protoc 33.2. The Go module pins the protobuf generators. The Maven project pins the Flink runtime and Kafka connector, and the frontend uses an npm lockfile.

From the repository root, install the contract validator dependencies in your Python environment and run the same Make targets as CI.

```sh
python3 -m pip install -r tests/requirements.txt
make build test-unit test-contracts test-smoke
```

`make generate-contracts` updates the checked-in Go protobuf bindings under `gen/`. Contract tests regenerate into a temporary directory and compare the output. Make accepts overrides for `GO`, `MVN`, `PYTHON`, and `PROTOC`. The tests use temporary loopback listeners.

```sh
make build-go
. deploy/local/postgres.env
./build/bin/catalog-replica -config deploy/config/local.example.json
```

The local example starts HTTP on port 8080 and gRPC on port 7001. Use `GET /healthz`, `GET /readyz`, `GET /metrics`, or the standard gRPC health service. The HTTP business routes return 501 and pending gRPC calls return UNIMPLEMENTED. SIGINT and SIGTERM initiate graceful shutdown.

Use `-config` or `FLASHKART_CONFIG` to select a JSON configuration. Assign unique listener ports when running multiple shells. The example profiles identify five DSN environment variables and four stable peer addresses. The replica now connects to PostgreSQL and exposes local document RPCs. [Phase 2 development](phase-2-development.md)

The notification binary runs the HTTP shell for local development. With `AWS_LAMBDA_RUNTIME_API` supplied, it polls the Lambda Runtime API and reports invocations as failed while the notification handler is pending. [Lambda Runtime API](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html)
