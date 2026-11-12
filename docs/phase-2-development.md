# Phase 2 local development

Start five standalone PostgreSQL containers, apply the repeatable migrations, and seed replica A. The base Compose profile exposes the five instances on loopback ports 5541 through 5545 and gives each a named volume. The transactional instance has separate roles and schemas for Flash Sale Manager, Admission, Inventory, Order, and Payment.

```sh
make dev-up migrate seed
make test-persistence
```

With Podman and a Compose provider, use `make dev-up migrate COMPOSE='podman compose'` before running `make seed test-persistence`. The checked-in passwords and DSNs are for these local containers. `make dev-down` stops and removes containers while retaining volumes.

Enable Kafka alone with `docker compose -p flashkart-dev -f deploy/local/compose.yml --profile kafka up -d`. The `flink` profile starts Kafka and both Flink components. Local clients use Kafka at `127.0.0.1:9092`, and containers use `kafka:29092`. The Flink dashboard listens at `http://127.0.0.1:8081`.

## Replica A

```sh
. deploy/local/postgres.env
make build-go
./build/bin/catalog-replica -config deploy/config/local.example.json
```

The agent reads its assigned DSN and uses a fixed identity from the configuration. HTTP and standard gRPC health are available on ports 8080 and 7001. Liveness reflects the running process. Readiness requires the PostgreSQL connection and expected schema. The catalog and peer gRPC calls in this phase operate against that agent's database. Phase 3 introduces coordinator fanout and quorum behavior.

Prepare allocates and journals a dot in a transaction. Only apply makes a version visible. A database row lock serializes changes per document. An operation lock prevents two transactions on different documents from claiming the same operation ID. Stored envelopes preserve exact payload bytes. Hidden versions remain available to enforce idempotency and retain hints even after a causally later write replaces them in reads.

The persistence suite uses the local PostgreSQL instance and runs the SQL and gRPC paths. It checks invisible preparations, replay after reopening the store, conflicting operation IDs, concurrent siblings, rollback during merge, retained hints, pagination, and unavailable-database errors.

Local verification passed with Podman Compose. All five PostgreSQL containers reached healthy status. Reapplying migrations and restarting PostgreSQL A preserved the seeded version and counter. Kafka responded to the broker API check and Flink registered its task manager. The workspace CI workflow includes the PostgreSQL integration suite for its next hosted run.

## AWS Emulator

Start the installed AWS Emulator with authentication on and binding to loopback. Its default port is 9297. Run `make test-emulator`, or set `FLASHKART_EMULATOR_PORT` if using a different port. The smoke script uses the emulator's TLS CA and per-port credential files in a child process, with an explicit loopback endpoint and a test account check. It cleans up its temporary resources.

Observed locally on version 1.0.322:

| Probe | Result |
| --- | --- |
| SQS create, send, receive, delete message and queue | Passed |
| DynamoDB create table, conditional put, duplicate rejection, get and delete | Passed |
| S3 create bucket, put/get/delete object and delete bucket | Passed |
| Lambda ListFunctions | Passed |
| Lambda deployment, invocation, SQS event source mapping and retry behavior | Requires later verification |

These probes cover the tested calls. Phase 8 still needs the notification handler and queue integration tests. IAM, networking, and managed-runtime behavior remain AWS acceptance checks.
