# Phase 10 browser demo

Start the marketplace with the purchase and outcome queues, notification table, and analytics table configured. Use the [Phase 8 setup](phase-8-development.md) and [Phase 9 Flink and projection setup](phase-9-development.md), then run `make run-market`.

In a second terminal, install the frontend dependencies and start Vite.

```sh
npm --prefix web ci
make run-web
```

Open [Flashkart on localhost](http://127.0.0.1:5173). Enter the same customer and administrator tokens configured on the backend under **Access tokens**. The browser keeps the tokens in memory. The local Vite proxy routes same-origin API calls to the marketplace ports. A deployment serving the static build needs equivalent `/api` routing.

1. In **Administration**, load a new product ID, enter a name, and save it. Use that product ID to create a sale draft with a window that is already open and a stock limit of one. Publish it and wait for the published terms.
2. In **Storefront**, open the sale and reserve one unit. The details show the queued and terminal states. Open the order and find the same attempt in **Activity**. To submit a separate purchase, choose **Start another attempt** after the first outcome appears.
3. In **Replicas**, inspect the product to see its preferred owners, visible versions, replica readiness, and pending hints. If the product has siblings, load it in **Administration**, choose a version, and explicitly resolve the conflict.
4. In **Analytics**, select the sale. The charts and table update after Flink finalizes a window. The latest window end and last successful query time show how current the projection is.

## Browser checks

Install Chromium with `cd web && npm exec -- playwright install chromium`. Run the suite against the local demo services, with Vite stopped so Playwright can start its own instance. Supply the same `FLASHKART_ADMIN_TOKEN` and `FLASHKART_CUSTOMER_TOKEN` environment variables to the test command.

```sh
FLASHKART_PAYMENT_FIXTURE=success FLASHKART_E2E_ANALYTICS=1 make test-e2e
# Restart the marketplace with FLASHKART_PAYMENT_FIXTURE=decline before the next run.
FLASHKART_PAYMENT_FIXTURE=decline make test-e2e
```

The suite creates unique products and sales in the local demo database. It drops one browser response after the real backend accepts the attempt, then checks that the retry preserves the attempt ID. The conflict test creates two siblings with the real catalog API and resolves them in the browser. Enable the analytics assertion when Kafka, Flink, and the projection worker are running. A window can take up to about two minutes to appear.

For an isolated analytics test run, create dedicated purchase-event and results topics. Pass them to Flink with `--events-topic` and `--results-topic`, and set `FLASHKART_PURCHASE_EVENTS_TOPIC` and `FLASHKART_ANALYTICS_RESULTS_TOPIC` on the marketplace to the same names. Use a unique Flink transactional ID prefix, a dedicated analytics table, and a dedicated consumer group for the run.
