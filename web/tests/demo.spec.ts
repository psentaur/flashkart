import { expect, test } from "@playwright/test";
import { randomUUID } from "node:crypto";

const admin = process.env.FLASHKART_ADMIN_TOKEN;
const customer = process.env.FLASHKART_CUSTOMER_TOKEN;
const decline = process.env.FLASHKART_PAYMENT_FIXTURE === "decline";

test("publishes a sale and follows real purchase outcomes", async ({ page, request }) => {
  if (!admin || !customer) throw new Error("Set the tokens used by the local marketplace.");
  const id = "browser-" + randomUUID();
  const name = "Browser sale " + id.slice(-8);
  const protectedPath = "/api/admin/replicas";
  expect((await request.get(protectedPath)).status()).toBe(401);
  expect((await request.get(protectedPath, { headers: { Authorization: "Bearer " + customer } })).status()).toBe(403);

  await page.goto("/");
  await page.getByRole("button", { name: "Access tokens" }).click();
  await page.getByLabel("Customer token", { exact: true }).fill(customer);
  await page.getByLabel("Administrator token").fill(admin);
  await page.getByRole("button", { name: "Access tokens" }).click();
  await page.getByRole("button", { name: "Administration", exact: true }).click();
  await page.getByLabel("Product ID", { exact: true }).fill(id);
  await page.getByRole("button", { name: "Load product" }).click();
  await page.getByLabel("Name", { exact: true }).fill(name);
  await page.getByRole("button", { name: "Save product" }).click();
  await expect(page.getByText("Product saved.")).toBeVisible();
  await page.getByLabel("Sale product ID").fill(id);
  await page.getByLabel("Price in minor units").fill("2499");
  await page.getByRole("button", { name: "Create draft" }).click();
  await expect(page.getByText(/Draft saved/)).toBeVisible();
  await page.getByRole("button", { name: "Publish sale" }).click();
  await expect(page.getByRole("heading", { name: "Published terms" })).toBeVisible({ timeout: 30_000 });
  const sale = (await page.locator("dt").filter({ hasText: "Sale ID" }).locator("..").locator("dd").textContent())!;
  expect((await request.patch("/api/admin/sale-drafts/" + sale, {
    headers: { Authorization: "Bearer " + admin },
    data: { product_id: id, price_minor: 1, currency: "USD", stock: 1,
      starts_at: new Date(Date.now() - 60_000).toISOString(), ends_at: new Date(Date.now() + 60_000).toISOString() },
  })).status()).toBe(409);

  await page.getByRole("button", { name: "Replicas", exact: true }).click();
  await page.getByLabel("Inspect product ID").fill(id);
  await page.getByRole("button", { name: "Inspect product" }).click();
  await expect(page.getByText("Preferred owners")).toBeVisible();
  await expect(page.getByText(name, { exact: true })).toBeVisible();
  await expect(page.getByText(/pending hints/)).toHaveCount(4);

  await page.getByRole("button", { name: "Storefront", exact: true }).click();
  await page.locator("article").filter({ has: page.getByRole("heading", { name, exact: true }) })
    .getByRole("button", { name: "View sale" }).click();
  const details = page.getByRole("region", { name: "Sale and product details" });
  const posts: string[] = [];
  let lost = false;
  let releasePolling!: () => void;
  const allowPolling = new Promise<void>(resolve => { releasePolling = resolve; });
  // Forward the first request to the real server, then lose its response.
  await page.route("**/api/attempts", async route => {
    posts.push(route.request().headers()["idempotency-key"]);
    if (!lost) {
      lost = true;
      const accepted = await route.fetch();
      expect(accepted.status()).toBe(202);
      await route.abort();
    } else {
      await route.continue();
    }
  });
  await page.route("**/api/attempts/*", async route => {
    await allowPolling;
    await route.continue();
  });
  const reserve = details.getByRole("button", { name: "Reserve one unit" });
  await reserve.evaluate(button => { (button as HTMLButtonElement).click(); (button as HTMLButtonElement).click(); });
  await expect(details.getByRole("alert")).toContainText("could not be completed");
  expect(posts).toHaveLength(1);
  await details.getByRole("button", { name: "Retry same attempt" }).click();
  await expect(details.getByText("QUEUED", { exact: true })).toBeVisible();
  releasePolling();
  await expect(details.getByText(decline ? "CANCELLED" : "CONFIRMED", { exact: true })).toBeVisible({ timeout: 35_000 });
  expect(posts).toEqual([posts[0], posts[0]]);
  if (decline) await expect(details.getByText("payment declined", { exact: true })).toBeVisible();
  await details.getByRole("button", { name: "View order" }).click();
  await expect(details.getByText(/^Order /)).toContainText(posts[0]);
  expect((await request.get("/api/attempts/" + posts[0], { headers: { Authorization: "Bearer " + admin } })).status()).toBe(401);

  await page.getByRole("button", { name: "Activity", exact: true }).click();
  await expect(page.getByText("Attempt " + posts[0], { exact: true })).toBeVisible();

  if (!decline) {
    await page.getByRole("button", { name: "Storefront", exact: true }).click();
    await page.locator("article").filter({ has: page.getByRole("heading", { name, exact: true }) })
      .getByRole("button", { name: "View sale" }).click();
    await details.getByRole("button", { name: "Start another attempt" }).click();
    await details.getByRole("button", { name: "Reserve one unit" }).click();
    await expect(details.getByText("REJECTED", { exact: true })).toBeVisible({ timeout: 35_000 });
    await expect(details.getByText("sold out", { exact: true })).toBeVisible();
    expect(posts[2]).not.toBe(posts[0]);
  }

  if (process.env.FLASHKART_E2E_ANALYTICS === "1") {
    await page.getByRole("button", { name: "Analytics", exact: true }).click();
    await expect(page.getByLabel("Analytics sale").locator("option").filter({ hasText: sale })).toHaveCount(1);
    await page.getByLabel("Analytics sale").selectOption(sale);
    await expect(page.getByRole("heading", { name: "Window history" })).toBeVisible({ timeout: 115_000 });
    await expect(page.getByText(/Latest finalized window ended/)).toBeVisible();
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
  await page.goto("/");
  await page.getByRole("button", { name: "Access tokens" }).click();
  await page.getByLabel("Administrator token").fill(admin);
  await page.getByRole("button", { name: "Administration", exact: true }).click();
  await page.getByLabel("Product ID", { exact: true }).fill(id);
  await page.getByRole("button", { name: "Load product" }).click();
  await expect(page.getByRole("heading", { name: "Choose a version to keep" })).toBeVisible();
  await page.getByRole("button", { name: /Version two/ }).click();
  await page.getByRole("button", { name: "Resolve conflict" }).click();
  await expect(page.getByText("Conflict resolved.")).toBeVisible();
  const resolved = await request.get("/api/products/" + id);
  expect(resolved.ok()).toBeTruthy();
  expect((await resolved.json()).product.name).toBe("Version two");
});
