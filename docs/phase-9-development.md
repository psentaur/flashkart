# Phase 9 sales analytics

Start Kafka and Flink, build the job, and create the topics. Skip the topic creation command for any topic already created in Phase 8.

```sh
make build-analytics build-go
docker compose -p flashkart-dev -f deploy/local/compose.yml --profile flink up -d
for topic in purchase-events analytics-results; do
  docker compose -p flashkart-dev -f deploy/local/compose.yml exec -T kafka \
    /opt/kafka/bin/kafka-topics.sh --bootstrap-server kafka:29092 \
    --create --if-not-exists --topic "$topic" --partitions 3 --replication-factor 1
done
docker compose -p flashkart-dev -f deploy/local/compose.yml exec -T flink-jobmanager \
  sh -c 'cat > /tmp/sales-analytics.jar' < analytics/target/sales-analytics-0.1.0.jar
docker compose -p flashkart-dev -f deploy/local/compose.yml exec -T flink-jobmanager \
  /opt/flink/bin/flink run -d -p 1 /tmp/sales-analytics.jar \
  --brokers kafka:29092 --checkpoints file:///opt/flink/checkpoints/analytics \
  --transactional-id-prefix flashkart-sales-analytics
```

Create an analytics table in the signed local DynamoDB emulator with string partition key `sale_id` and string sort key `starts_at`. Set `FLASHKART_ANALYTICS_TABLE` to its name and use the DynamoDB endpoint, credentials file, and CA variables from [Phase 8](phase-8-development.md). Run `make run-market` with `FLASHKART_PUBLISH_EVENTS=1`. The companion listens on port 8208. Query `GET /api/analytics/sales/{sale_id}` for finalized windows in chronological order.

The companion discovers the input partitions and publishes a UTC heartbeat to each about every five seconds. Flink computes watermarks per Kafka partition with ten seconds of out-of-order tolerance and excludes heartbeats from aggregation. A minute window emits once the watermark reaches its end plus thirty seconds of allowed lateness. Flink checkpoints the latest finalized window boundary for each sale separately from the window contents. The Flink UI exposes `lateEventsDropped` on the late-event sink and resets the metric when that task restarts.

Demand counts accepted attempts and durable admission throttles. The separate stock-rejection count covers `SOLD_OUT`. The average is over the `latency_ms` of confirmed orders and is null for windows with zero confirmations. Each snapshot identifies its sale and minute so the contributing purchase events can be located by sale ID and event timestamp. The admission service's immediate concurrency-limit responses bypass its durable outbox.

Flink retains externalized checkpoints when canceled. To recover, find the latest retained `chk-*` directory containing `_metadata` after the job stops, then add `-s <checkpoint-uri>` before the JAR in the Flink command. Use `s3://<bucket>/<prefix>` for AWS checkpoints and install the Flink S3 filesystem plugin in each Flink runtime. Use a retained checkpoint when restarting the existing job so Kafka offsets and open windows recover together. The job accepts a Kafka properties file via `--kafka-properties` for runtime authentication settings. The Go companion reuses the Phase 8 Kafka IAM adapter. Live S3 and MSK verification belongs to Phase 12. [Flink checkpoints](https://nightlies.apache.org/flink/flink-docs-release-1.20/docs/ops/state/checkpoints/), [Flink Kafka connector](https://nightlies.apache.org/flink/flink-docs-release-1.20/docs/connectors/datastream/kafka/)

Flink commits Kafka result transactions with its checkpoints. Give each concurrently running job a unique `--transactional-id-prefix` and reuse that prefix when restoring the same job. The default Kafka transaction timeout is ten minutes. If checkpointing and recovery can exceed that, set a higher `transaction.timeout.ms` in the Kafka properties and raise the broker limit as needed. The projector reads committed results and commits Kafka offsets after DynamoDB accepts the full snapshot. Replaying an identical snapshot is safe. A conflicting snapshot or malformed record holds progress at its offset and logs the partition and offset for diagnosis. A fresh projection table must use a fresh consumer group via `FLASHKART_ANALYTICS_GROUP` or reset the existing group's offsets while the companion is stopped.

Run `make test-analytics` to build the job and exercise Kafka, Flink, late windows, and checkpoint restore with isolated Kafka topics. To include the DynamoDB projection recovery test, set `FLASHKART_TEST_ANALYTICS_TABLE`, `FLASHKART_TEST_DYNAMODB_ENDPOINT`, `FLASHKART_TEST_DYNAMODB_CREDENTIALS`, and `FLASHKART_TEST_DYNAMODB_CA` for a dedicated local fixture. Set `FLASHKART_TEST_KAFKA=127.0.0.1:9092` to also verify offset replay and commit against Kafka.
