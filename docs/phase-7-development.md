# Phase 7 checkout workflow

Apply the transactional migrations and run the checkout integration tests.

```sh
make dev-up migrate
make test-workflows
```

Use the queue and demo credentials from the Phase 6 guide, then start the local marketplace with `make run-market`. Order queries are available on port 8205. Send the customer bearer token to `GET /api/orders/{order_id}`. The order ID and payment ID both use the reservation's attempt ID. An order appears once Inventory delivers its durable order work. [Phase 6 development](phase-6-development.md)

| Payment fixture | Effect |
| --- | --- |
| `success` | Persist a successful payment immediately |
| `decline` | Persist a decline and release the reservation |
| `delay` | Persist a pending payment and settle it when queried after the configured delay |
| `lost-response` | Persist success and return an unavailable error once, then recover by querying the same payment ID |

Set `FLASHKART_PAYMENT_FIXTURE` before starting the services. The default is `success`. Delayed payments use `FLASHKART_PAYMENT_DELAY` with a default of three minutes. Inventory uses `FLASHKART_RESERVATION_TTL` and `FLASHKART_EXPIRY_INTERVAL` with defaults of two minutes and five seconds.

Order workers claim persisted work for a bounded interval and retry from the recorded order, payment, and reservation states. Inventory releases an expired reservation under the same row lock used by confirmation. A successful payment whose reservation can no longer commit is refunded before Order records cancellation. Order and payment events and terminal outcomes are stored transactionally for Phase 8 delivery.
