import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";
import { randomUUID } from "node:crypto";
import { deflateSync } from "node:zlib";

const admin = process.env.FLASHKART_ADMIN_TOKEN;
const customer = process.env.FLASHKART_CUSTOMER_TOKEN;

/** A small valid PNG, so the upload goes through the real S3 path. */
function png(): Buffer {
  const crc = (data: Buffer) => {
    let c = ~0;
    for (const byte of data) { c ^= byte; for (let k = 0; k < 8; k++) c = (c >>> 1) ^ (0xedb88320 & -(c & 1)); }
    return ~c >>> 0;
  };
  const chunk = (type: string, data: Buffer) => {
    const head = Buffer.alloc(4); head.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(type), data]);
    const tail = Buffer.alloc(4); tail.writeUInt32BE(crc(body));
    return Buffer.concat([head, body, tail]);
  };
  const header = Buffer.alloc(13); header.writeUInt32BE(8, 0); header.writeUInt32BE(8, 4); header[8] = 8; header[9] = 2;
  const rows = Buffer.concat(Array.from({ length: 8 }, () => Buffer.concat([Buffer.from([0]), Buffer.alloc(24, 0x80)])));
  return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk("IHDR", header), chunk("IDAT", deflateSync(rows)), chunk("IEND", Buffer.alloc(0))]);
}

async function signIn(page: Page, role: "Customer" | "Administrator", token: string) {
  await page.goto("/");
  if (await page.getByRole("button", { name: "Sign out" }).isVisible()) await page.getByRole("button", { name: "Sign out" }).click();
  await page.getByRole("radio", { name: new RegExp(role) }).click();
  await page.getByLabel(`${role} access key`).fill(token);
  await page.getByRole("button", { name: `Sign in as ${role.toLowerCase()}` }).click();
}

test("publishes a sale and follows real purchase outcomes through every service", async ({ page, request }) => {
  if (!admin || !customer) throw new Error("Set the tokens used by the local marketplace.");
  const name = "Browser product " + randomUUID().slice(-8);
  const protectedPath = "/api/admin/replicas";
  expect((await request.get(protectedPath)).status()).toBe(401);
  expect((await request.get(protectedPath, { headers: { Authorization: "Bearer " + customer } })).status()).toBe(403);

  // A customer key is refused for the administrator role.
  await signIn(page, "Administrator", customer);
  await expect(page.getByRole("alert")).toContainText("not valid for the administrator role");
  await page.getByLabel("Administrator access key").fill(admin);
  await page.getByRole("button", { name: "Sign in as administrator" }).click();

  // Product with an uploaded image.
  await page.getByRole("button", { name: "New product" }).click();
  await page.getByLabel("Name").fill(name);
  const id = await page.getByLabel("Product ID").inputValue();
  expect(id).toBe(name.toLowerCase().replaceAll(" ", "-"));
  await page.getByLabel("Regular price").fill("49.99");
  await page.locator("input[type=file]").setInputFiles({ name: "product.png", mimeType: "image/png", buffer: png() });
  await expect(page.getByText("New photo — it uploads when you create the product.")).toBeVisible();
  await page.getByRole("button", { name: "Create product" }).click();
  const productCard = page.locator("article").filter({ has: page.getByRole("heading", { name, exact: true }) });
  await expect(productCard.locator("img")).toHaveAttribute("src", new RegExp(`/api/products/${id}/image\\?v=`));
  const image = await request.get(`/api/products/${id}/image`);
  expect(image.headers()["content-type"]).toBe("image/png");

  // The Flash Sale Manager refuses sales for products that are not in the catalog.
  const unknown = await request.post("/api/admin/sale-drafts", { headers: { Authorization: "Bearer " + admin }, data: {
    product_id: "missing-" + id, price_minor: 100, currency: "INR", stock: 1,
    starts_at: new Date(Date.now() - 60_000).toISOString(), ends_at: new Date(Date.now() + 600_000).toISOString() } });
  expect(unknown.status()).toBe(400);
  expect((await unknown.json()).code).toBe("PRODUCT_NOT_FOUND");

  // Sale from the product dropdown, with one unit of stock.
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await page.getByLabel("Product").selectOption(id);
  await page.getByLabel("Price").fill("24.99");
  await page.getByLabel("Stock limit").fill("1");
  await page.getByRole("button", { name: "Save draft" }).click();
  await page.getByRole("button", { name: "Publish sale" }).click();
  await expect(page.getByRole("heading", { name: "Sale published" })).toBeVisible({ timeout: 30_000 });

  // A regular listing sells the same product at its regular price through the same services.
  await page.getByRole("button", { name: "Schedule another sale" }).click();
  await page.getByRole("radio", { name: /Regular listing/ }).click();
  await page.getByLabel("Product").selectOption(id);
  await expect(page.getByLabel("Price (regular)")).toHaveValue("49.99");
  await expect(page.getByLabel("Price (regular)")).toHaveAttribute("readonly", "");
  await expect(page.getByLabel("Starts at")).toHaveCount(0);
  await expect(page.getByText("stays on sale until you end it")).toBeVisible();
  await page.getByLabel("Stock available").fill("2");
  await page.getByRole("button", { name: "Save draft" }).click();
  await page.getByRole("button", { name: "Publish sale" }).click();
  await expect(page.getByText("is now listed at its regular price")).toBeVisible({ timeout: 30_000 });
  const sale = (await (await request.get("/api/sales")).json()).find((s: { terms: { product_id: string; price_minor: number } }) => s.terms.product_id === id && s.terms.price_minor === 2499).sale_id;
  expect((await request.patch("/api/admin/sale-drafts/" + sale, {
    headers: { Authorization: "Bearer " + admin },
    data: { product_id: id, price_minor: 1, currency: "USD", stock: 1,
      starts_at: new Date(Date.now() - 60_000).toISOString(), ends_at: new Date(Date.now() + 60_000).toISOString() },
  })).status()).toBe(409);

  await page.getByRole("button", { name: "Replicas", exact: true }).click();
  await page.getByLabel("Product").selectOption(id);
  await expect(page.getByText("Preferred owners")).toBeVisible();
  await expect(page.getByText(/pending hints?/)).toHaveCount(4);

  // Customer: a declined test card cancels the order and returns the unit to stock.
  await signIn(page, "Customer", customer);
  // Each product appears once: as a flash deal while it has stock.
  const card = page.getByRole("article", { name, exact: true });
  await expect(card).toHaveCount(1);
  await expect(card.getByText("⚡ Flash deal")).toBeVisible();
  await expect(card.getByText("1 of 1 left")).toBeVisible();
  await expect(card.getByText("50% off")).toBeVisible();
  await card.getByRole("button", { name: "Add to cart" }).click();
  await page.getByRole("button", { name: /^Cart/ }).click();
  await page.getByLabel(/Test card/).check();
  await page.getByRole("button", { name: "Place order" }).click();
  const declined = page.locator("article.order").first();
  await expect(declined.locator(".order-line .pill", { hasText: /^Payment declined$/ })).toBeVisible({ timeout: 35_000 });
  await expect(declined.getByText(/nothing was charged and the item went back on sale/)).toBeVisible();

  // A lost response is retried with the same attempt ID and buys exactly once.
  await page.getByRole("button", { name: "Shop", exact: true }).click();
  await expect(card.getByText("1 of 1 left")).toBeVisible({ timeout: 15_000 });
  await card.getByRole("button", { name: "Add to cart" }).click();
  const posts: string[] = [];
  await page.route("**/api/attempts", async route => {
    posts.push(route.request().headers()["idempotency-key"]);
    if (posts.length === 1) {
      expect((await route.fetch()).status()).toBe(202);
      await route.abort();
    } else {
      await route.continue();
    }
  });
  await page.getByRole("button", { name: /^Cart/ }).click();
  const place = page.getByRole("button", { name: "Place order" });
  await place.evaluate(button => { (button as HTMLButtonElement).click(); (button as HTMLButtonElement).click(); });
  const bought = page.locator("article.order").first();
  await expect(bought.locator(".order-line .pill", { hasText: /^Confirmed$/ })).toBeVisible({ timeout: 35_000 });
  expect(posts).toEqual([posts[0], posts[0]]);
  await expect(bought.getByText("Paid and confirmed. You got it!")).toBeVisible();
  await expect(bought.getByText(/test card|Technical details|Admission Controller/)).toHaveCount(0);
  expect((await request.get("/api/attempts/" + posts[0], { headers: { Authorization: "Bearer " + admin } })).status()).toBe(401);
  const payment = await request.get("/api/payments/" + posts[0], { headers: { Authorization: "Bearer " + customer } });
  expect((await payment.json()).state).toBe("SUCCEEDED");
  await page.unroute("**/api/attempts");

  // The last unit is gone: the next attempt is rejected by Inventory.
  await page.getByRole("button", { name: "Shop", exact: true }).click();
  // With the flash deal sold out, the same card switches to the regular listing.
  await expect(card.getByText("2 of 2 left")).toBeVisible({ timeout: 15_000 });
  await expect(card).toHaveCount(1);
  await expect(card.getByRole("button", { name: "Add to cart" })).toBeEnabled();
  await expect(card.getByText(/Flash deal/)).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Flash-crowd demo" })).toHaveCount(0);

  // The Notification Service's messages appear in the bell for this shopper's own orders.
  await page.getByRole("button", { name: /^Notifications/ }).click();
  const bell = page.getByRole("dialog", { name: "Notifications" });
  // One message per order, naming its items; opening it shows that order.
  await expect(bell.getByRole("button", { name: /^Order confirmed · 1 item/ })).toBeVisible();
  await expect(bell.getByRole("button", { name: /^Payment declined · 1 item, not charged/ })).toBeVisible();
  await expect(bell.getByText(name)).toHaveCount(2);
  await bell.getByRole("button", { name: /^Order confirmed/ }).click();
  await expect(page.getByRole("heading", { name: "My orders" })).toBeVisible();
  await expect(page.locator("article.order.focused").getByText("Paid and confirmed. You got it!")).toBeVisible();

  // The administrator's rush test sends a crowd of shoppers and traces it through each service.
  await signIn(page, "Administrator", admin);
  await page.getByRole("button", { name: "Rush test", exact: true }).click();
  await page.getByLabel("Live sale").selectOption(sale);
  await page.getByLabel("Shoppers at once").fill("3");
  await page.getByLabel("Customer access key").fill(customer);
  await page.getByRole("button", { name: "Send 3 shoppers" }).click();
  await expect(page.locator(".crowd-card").getByText("0 got a unit · 3 sold out")).toBeVisible({ timeout: 35_000 });

  // A cloud burst is a real flash crowd: the Admission Controller turns shoppers away.
  await page.getByRole("button", { name: "Run 20-second burst" }).click();
  const burst = page.locator(".burst");
  await expect(burst.getByText("Finished")).toBeVisible({ timeout: 40_000 });
  const throttled = Number((await burst.locator(".tile", { hasText: "Throttled" }).locator("strong").innerText()).replace(/,/g, ""));
  expect(throttled).toBeGreaterThan(0);

  // Ending the regular listing early stops further orders; held stock is unaffected.
  const listing = (await (await request.get("/api/sales")).json()).find((s: { terms: { product_id: string; price_minor: number } }) =>
    s.terms.product_id === id && s.terms.price_minor === 4999).sale_id;
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  const row = page.locator("tr").filter({ hasText: "Regular" }).filter({ hasText: name });
  await row.getByRole("button", { name: "End sale" }).click();
  await row.getByRole("button", { name: "End", exact: true }).click();
  await expect(page.getByText(`${name} has ended.`)).toBeVisible();
  await expect(row.getByText("Ended", { exact: true })).toBeVisible({ timeout: 10_000 });
  expect((await (await request.get("/api/inventory/sales/" + listing)).json()).active).toBe(false);
  expect((await request.post(`/api/inventory/sales/${listing}/end`, { headers: { Authorization: "Bearer " + customer } })).status()).toBe(403);
  await page.getByRole("button", { name: "Publish a replacement" }).click();
  await expect(page.getByRole("radio", { name: /Regular listing/ })).toHaveAttribute("aria-checked", "true");
  await expect(page.getByLabel("Product")).toHaveValue(id);

  if (process.env.FLASHKART_E2E_ANALYTICS === "1") {
    await page.getByRole("button", { name: "Analytics", exact: true }).click();
    await expect(page.getByRole("region", { name: "All sales" }).getByText("Confirmed orders × price")).toBeVisible();
    await page.getByRole("combobox", { name: /^Sale/ }).selectOption(sale);
    await expect(page.getByRole("heading", { name: "Window history" })).toBeVisible({ timeout: 115_000 });
  }
});

test("explicitly resolves persisted catalog siblings", async ({ page, request }) => {
  if (!admin) throw new Error("Set the local administrator token.");
  const id = "conflict-" + randomUUID();
  const path = "/api/admin/products/" + id;
  const headers = { Authorization: "Bearer " + admin };
  for (const name of ["Version one", "Version two"]) {
    const response = await request.put(path, { headers,
      data: { operation_id: randomUUID(), product: { product_id: id, name }, context: {} } });
    expect(response.status()).toBe(name === "Version one" ? 200 : 409);
  }
  await signIn(page, "Administrator", admin);
  await page.getByRole("button", { name: "Resolve " + id }).click();
  await expect(page.getByRole("heading", { name: "Replicas disagree about this product" })).toBeVisible();
  await page.getByRole("button", { name: /Version two/ }).click();
  await page.getByRole("button", { name: "Resolve conflict", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Version two", exact: true })).toBeVisible();
  const resolved = await request.get("/api/products/" + id);
  expect(resolved.ok()).toBeTruthy();
  expect((await resolved.json()).product.name).toBe("Version two");
});
