# Phase 8 event delivery and notifications

Start PostgreSQL and the local Kafka broker, apply migrations, and create the Kafka topic.

```sh
make dev-up migrate
docker compose -p flashkart-dev -f deploy/local/compose.yml --profile kafka up -d kafka
docker compose -p flashkart-dev -f deploy/local/compose.yml exec -T kafka \
  /opt/kafka/bin/kafka-topics.sh --bootstrap-server kafka:29092 \
  --create --if-not-exists --topic purchase-events --partitions 3 --replication-factor 1
```

Use the [Phase 6 purchase queue and demo credentials](phase-6-development.md). In the same signed local AWS Emulator, create a standard outcome queue and a DynamoDB table. The table uses string partition key `customer_id` and string sort key `attempt_id`. Wait until the table is active. Configure the outcome queue's redrive policy with a standard dead-letter queue so invalid or repeatedly failing notification messages can be inspected and redriven.

Set these variables before `make run-market`.

| Variable | Value |
| --- | --- |
| `FLASHKART_PUBLISH_EVENTS` | `1` to enable Kafka publication |
| `FLASHKART_OUTCOME_QUEUE_URL` | Outcome queue URL |
| `FLASHKART_NOTIFICATION_TABLE` | Notification table name |
| `FLASHKART_DYNAMODB_ENDPOINT` | Emulator loopback HTTPS endpoint |
| `FLASHKART_DYNAMODB_CREDENTIALS_FILE` | Emulator instance credential file |
| `FLASHKART_DYNAMODB_CA_FILE` | Emulator instance TLS CA |

Use the customer bearer token for `GET /api/attempts/{attempt_id}` on port 8204 and `GET /api/notifications` on port 8207. An accepted attempt stays `QUEUED` until its terminal outcome is projected. Notification applies the first terminal outcome for each customer and attempt. Repeated delivery preserves that outcome.

Kafka records use the sale ID as the partition key and include a stable event ID. Consumers should deduplicate by event ID. PostgreSQL tracks Kafka events and SQS outcomes independently. Workers retry unacknowledged deliveries after a 15-second claim interval and retain a row for manual replay after ten attempts. A crash after publish can produce duplicate messages with the same event ID.

The publishing services expose `flashkart_outbox_pending` and `flashkart_outbox_dead` by source on `/metrics`. Query the source table for rows with `dead_at IS NOT NULL`. After resolving the failure, replay a row using its original ID and a database role with access to that source. The following command replays an inventory outcome.

```sh
. deploy/local/postgres.env
export FLASHKART_TRANSACTIONAL_DSN="$FLASHKART_INVENTORY_DSN"
build/bin/replay-event -source inventory.outcomes -id "attempt.terminal:$attempt_id"
```

Run `make test-events-notifications` for PostgreSQL replay and partial-batch checks. To include the real local transports, provide a dedicated empty outcome queue and notification table and set `FLASHKART_TEST_QUEUE_URL`, `FLASHKART_TEST_QUEUE_ENDPOINT`, `FLASHKART_TEST_QUEUE_CREDENTIALS`, `FLASHKART_TEST_QUEUE_CA`, `FLASHKART_TEST_NOTIFICATION_TABLE`, and `FLASHKART_TEST_KAFKA` (for example, `127.0.0.1:9092`).

The Notification binary uses the same handler for its local SQS runner and the Lambda Runtime API. Configure the SQS event source mapping with `ReportBatchItemFailures` so successful records in a partially failed invocation are acknowledged. In AWS, set Kafka auth to `iam` and use the IAM broker addresses and an AWS region. Live Lambda and MSK verification is part of Phase 12. [Lambda batch handling](https://docs.aws.amazon.com/lambda/latest/dg/services-sqs-errorhandling.html), [MSK IAM clients](https://docs.aws.amazon.com/msk/latest/developerguide/configure-clients-for-iam-access-control.html)
