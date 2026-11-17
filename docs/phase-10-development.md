# Phase 10 browser demo

Start the marketplace with the purchase and outcome queues, notification table, and analytics table configured. Use the [Phase 8 setup](phase-8-development.md) and [Phase 9 Flink and projection setup](phase-9-development.md), then run `make run-market`.

In a second terminal, install the frontend dependencies and start Vite.

```sh
npm --prefix web ci
make run-web
```

Open [Flashkart on localhost](http://127.0.0.1:5173) and sign in. Choose **Customer** or **Administrator** and enter that role's access key, which is the matching bearer token configured on the backend. The sign-in checks the key against an endpoint of that role, and the tab keeps the session in session storage until you sign out. The local Vite proxy routes same-origin API calls to the marketplace ports, as nginx does in Kubernetes.

Administrators see these pages:

1. **Products.** Create a product with a name, a permanent ID suggested from the name, a description, an optional regular price and a photo. Every product is a catalog item. It is put on sale from the Sales page, either as a flash sale or as a regular listing. The list shows each product's status (flash, regular, scheduled or not listed) and the stock it has for sale, and can be sorted by status or price; products that are not listed are hidden from customers. Drop or choose a photo and it previews at once; it uploads to S3 when the product is saved, and the Product Catalog serves it at `/api/products/{id}/image`. If replicas hold concurrent versions of a product, the page names it, and **Resolve** opens the version picker.
2. **Sales.** Choose **Flash sale** (a price below the regular price, limited stock, a short window) or **Regular listing** (the regular price, with a 30-day window by default). Both are sales in the Flash Sale Manager and go through the same purchase services; the storefront treats a sale cheaper than the product's regular price, or for a product without one, as a flash deal. A regular listing's price can be changed before publishing, and saving it also updates the product's regular price. Published terms are frozen: **End sale** stops a live or scheduled sale (Inventory Reservation rejects further purchases while held reservations finish), and **Publish a replacement** starts a new sale for the same product with a new price or stock. Choose the product from a dropdown, then set the price, the currency, the stock limit and the window. Save the draft and publish it. The Flash Sale Manager rejects drafts for products that are not in the catalog.
3. **Rush test.** A demonstration tool, not a shopper feature. It sends up to 300 shoppers at a live sale at once, signed with the customer key, and shows how the Admission Controller, Inventory Reservation, Order Service, Payment Service and Notification Service handled them. Inventory never sells more than the stock limit. The Admission Controller turns shoppers away above its configured rate (`FLASHKART_ADMISSION_RATE`, `FLASHKART_ADMISSION_BURST`); a browser reaches about 100 attempts per second, so that appears only when those limits are lower than the 200 per second default.
4. **Analytics.** **All sales** totals demand, confirmed orders, revenue, sold-out and throttled rejections and cancellations across every sale, with a per-sale table. Pick a sale by product name to see demand, throttled and sold-out rejections, reservations, confirmed purchases, latency, stock consumed, a per-minute chart and the window history.
5. **Replicas.** Shows replica readiness and pending hints. Choose a product to see its preferred owners and visible versions.

Customers see these pages:

1. **Shop.** A banner shows how many flash deals are live. Each deal has its own countdown and shows its price, its discount against the regular price, a countdown, live "X of Y left" stock from Inventory Reservation and a quantity picker. The catalog follows, sortable by on-sale first or price. It lists only products that can be bought now: at the flash price while a flash deal has stock, otherwise at the regular listing's price.
2. **Cart.** Adjust quantities and choose the payment: **Card** succeeds, and **Test card** is always declined so the unit returns to stock. **Place order** sends one purchase attempt per unit and retries a lost response with the same attempt ID.
3. **Notifications.** The bell shows the Notification Service's messages about this shopper's orders, with an unread count.
4. **My orders.** Each order lists its items with a plain tracker (order placed, item reserved, payment, confirmed) that shows where an item stopped and why, for example sold out or payment declined.

## Browser checks

Install Chromium with `cd web && npm exec -- playwright install chromium`. Run the suite against the local demo services, with Vite stopped so Playwright can start its own instance. Supply the same `FLASHKART_ADMIN_TOKEN` and `FLASHKART_CUSTOMER_TOKEN` environment variables to the test command.

```sh
FLASHKART_E2E_ANALYTICS=1 make test-e2e
```

The suite creates unique products and sales in the local demo database. It uploads a product image, checks that the Flash Sale Manager rejects unknown products, buys with a declining test card and then a normal card, and sells the last unit before a flash crowd is rejected as sold out. It drops one browser response after the real backend accepts the attempt, then checks that the retry preserves the attempt ID. The conflict test creates two siblings with the real catalog API and resolves them in the browser. Enable the analytics assertion when Kafka, Flink, and the projection worker are running. A window can take up to about two minutes to appear.

For an isolated analytics test run, create dedicated purchase-event and results topics. Pass them to Flink with `--events-topic` and `--results-topic`, and set `FLASHKART_PURCHASE_EVENTS_TOPIC` and `FLASHKART_ANALYTICS_RESULTS_TOPIC` on the marketplace to the same names. Use a unique Flink transactional ID prefix, a dedicated analytics table, and a dedicated consumer group for the run.
