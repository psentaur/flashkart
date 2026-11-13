# Phase 6 admission and inventory reservations

Apply the transactional migrations with `make dev-up migrate`. Run `make test-reservations` to check admission and reservations against PostgreSQL.

To run the purchase flow, create a standard purchase queue in the signed local AWS Emulator. Set these environment variables before `make run-market`.

| Setting | Value |
| --- | --- |
| `FLASHKART_ADMIN_TOKEN` | Token for sale publication and catalog administration |
| `FLASHKART_CUSTOMER_TOKEN` | Separate token for purchase submission |
| `FLASHKART_CUSTOMER_ID` | Demo customer identity associated with the token |
| `FLASHKART_PURCHASE_QUEUE_URL` | Purchase queue URL returned by the emulator |
| `FLASHKART_SQS_ENDPOINT` | Emulator loopback HTTPS endpoint |
| `FLASHKART_SQS_CREDENTIALS_FILE` | Emulator instance credential file |
| `FLASHKART_SQS_CA_FILE` | Emulator instance TLS CA |

Admission listens on port 8204 when a queue is configured. POST to `/api/attempts` with the customer bearer token, an `Idempotency-Key` header, and a JSON body containing `attempt_id` and `sale_id`. Use the same attempt ID in the header and body. A 202 confirms durable acceptance. A 429 includes a retry interval. A 409 signals reuse of an accepted attempt ID with a different sale or customer.

Per-process admission defaults are 200 new attempts per second, a burst of 200, and 32 concurrent admission requests. Configure them with `FLASHKART_ADMISSION_RATE`, `FLASHKART_ADMISSION_BURST`, and `FLASHKART_ADMISSION_CONCURRENCY`. The customer identity comes from server configuration.

The outbox retries unconfirmed sends after its 15-second claim expires. Inventory acknowledges SQS messages after committing the stock change or rejection. Commit and release are idempotent gRPC transitions for the order workflow. Reservations, order work, and business events remain durable in PostgreSQL for the later order and analytics phases. [SQS delivery semantics](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/standard-queues-at-least-once-delivery.html)
