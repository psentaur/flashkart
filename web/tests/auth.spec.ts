import { expect, test } from "@playwright/test";

for (const role of ["guest", "customer", "admin", "owner"] as const) {
  test(`${role} sees only permitted tabs and routes`, async ({ page }) => {
    if (role !== "guest") await page.addInitScript(role => {
      sessionStorage.setItem("flashkart-demo", JSON.stringify({ customer: "test-session", admin: "test-session" }));
    }, role);
    let privateRequests = 0;
    await page.route("**/api/**", async route => {
      const path = new URL(route.request().url()).pathname;
      if (path === "/api/auth/me") await route.fulfill({ json: { user_id: `user_${role}`, role } });
      else if (path.startsWith("/api/admin/team")) { privateRequests++; await route.fulfill({ json: { data: [], total_count: 0 } }); }
      else { if (path.startsWith("/api/analytics") || path.startsWith("/api/admin")) privateRequests++; await route.fulfill({ json: [] }); }
    });
    await page.goto("/");
    const nav = page.getByRole("navigation", { name: "Main navigation" });
    await expect(nav.getByRole("button", { name: "Shop" })).toBeVisible();
    if (role !== "guest") await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
    for (const label of ["Products", "Sales", "Rush test", "Analytics", "Replicas"]) {
      if (role === "admin" || role === "owner") await expect(nav.getByRole("button", { name: label })).toBeVisible();
      else await expect(nav.getByRole("button", { name: label })).toHaveCount(0);
    }
    for (const label of ["Cart", "My orders"]) {
      await expect(nav.getByRole("button", { name: label })).toHaveCount(role === "guest" ? 0 : 1);
    }
    await expect(nav.getByRole("button", { name: "Team" })).toHaveCount(role === "owner" ? 1 : 0);
    if (role === "guest") {
      await page.goto("/analytics");
      await expect(page).toHaveURL(/\/login\?returnTo=%2Fanalytics/);
    } else if (role === "customer") {
      await page.goto("/analytics"); await expect(page.getByRole("heading", { name: "Access restricted" })).toBeVisible();
      await page.goto("/admin"); await expect(page.getByRole("heading", { name: "Access restricted" })).toBeVisible();
    } else if (role === "admin") {
      await page.goto("/team"); await expect(page.getByRole("heading", { name: "Access restricted" })).toBeVisible();
    } else {
      await page.goto("/team"); await expect(page.getByRole("heading", { name: "Team", exact: true })).toBeVisible();
    }
    if (role !== "owner") expect(privateRequests).toBe(0);
  });
}

test("owner confirms access changes and can revoke an invitation", async ({ page }) => {
  await page.addInitScript(() => sessionStorage.setItem("flashkart-demo", JSON.stringify({ admin: "owner", customer: "" })));
  const changes: unknown[] = [];
  let invited = false;
  await page.route("**/api/**", async route => {
    const path = new URL(route.request().url()).pathname;
    const method = route.request().method();
    if (path === "/api/auth/me") return route.fulfill({ json: { user_id: "owner", role: "owner" } });
    if (path === "/api/admin/team/users" ) return route.fulfill({ json: { data: [
      { id: "owner", email: "owner@example.com", role: "owner", verified: true },
      { id: "customer", email: "customer@example.com", role: "customer", verified: true },
    ], total_count: 2 } });
    if (path === "/api/admin/team/users/customer") {
      changes.push(route.request().postDataJSON()); return route.fulfill({ json: { id: "customer", role: "admin" } });
    }
    if (path === "/api/admin/team/invitations" && method === "POST") { invited = true; return route.fulfill({ status: 201, json: { id: "invite" } }); }
    if (path === "/api/admin/team/invitations/invite") { invited = false; return route.fulfill({ json: { status: "revoked" } }); }
    if (path === "/api/admin/team/invitations") return route.fulfill({ json: {
      data: invited ? [{ id: "invite", email: "new@example.com", status: "pending" }] : [], total_count: invited ? 1 : 0,
    } });
    return route.fulfill({ json: [] });
  });
  await page.goto("/team");
  await expect(page.getByText("Protected owner")).toBeVisible();
  await page.getByRole("button", { name: "Grant admin access" }).click();
  expect(changes).toEqual([]);
  await page.getByRole("button", { name: "Confirm access change" }).click();
  await expect.poll(() => changes).toEqual([{ role: "admin" }]);
  await page.getByLabel("Email address", { exact: true }).fill("new@example.com");
  await page.getByRole("button", { name: "Send invitation" }).click();
  await page.getByRole("button", { name: "Revoke invitation" }).click();
  await expect(page.getByRole("button", { name: "Revoke invitation" })).toHaveCount(0);
});

test("session renewal gets a fresh token without changing a purchase attempt", async ({ page }) => {
  const received: { authorization?: string; attempt?: string }[] = [];
  await page.route("**/api/**", async route => {
    received.push({ authorization: route.request().headers().authorization, attempt: route.request().headers()["idempotency-key"] });
    await route.fulfill({ json: { state: "QUEUED" } });
  });
  await page.goto("/login");
  await page.evaluate(async () => {
    // Exercise the actual API client with a renewing session, without Clerk network calls.
    const modulePath = "/src/api.ts";
    const { api } = await import(/* @vite-ignore */ modulePath);
    let serial = 0;
    const token = { userID: "customer", getToken: async () => `session-${++serial}` };
    for (let i = 0; i < 2; i++) await api("/api/attempts", { method: "POST", token, attemptID: "same-attempt", body: { attempt_id: "same-attempt", sale_id: "sale" } });
  });
  expect(received).toEqual([
    { authorization: "Bearer session-1", attempt: "same-attempt" },
    { authorization: "Bearer session-2", attempt: "same-attempt" },
  ]);
});

test("signed-in customers can add a Phase 13b deal to the cart and see its outcome", async ({ page }) => {
  await page.addInitScript(() => sessionStorage.setItem("flashkart-demo", JSON.stringify({ customer: "shopper", admin: "" })));
  let attemptID = "";
  const sale = { sale_id: "deal", terms: { product_id: "speaker", price_minor: 2500, currency: "USD", stock: 2,
    starts_at: new Date(Date.now() - 60_000).toISOString(), ends_at: new Date(Date.now() + 600_000).toISOString() } };
  await page.route("**/api/**", async route => {
    switch (new URL(route.request().url()).pathname) {
      case "/api/auth/me": return route.fulfill({ json: { user_id: "shopper", role: "customer" } });
      case "/api/products": return route.fulfill({ json: [{ product_id: "speaker", name: "Speaker" }] });
      case "/api/sales": return route.fulfill({ json: [sale] });
      case "/api/inventory/sales": return route.fulfill({ json: [{ sale_id: "deal", stock: 2, available: 2, sold: 0, held: 0, active: true }] });
      case "/api/attempts":
        expect(route.request().headers().authorization).toBe("Bearer shopper");
        attemptID = route.request().postDataJSON().attempt_id;
        expect(route.request().headers()["idempotency-key"]).toBe(attemptID);
        return route.fulfill({ status: 202, json: { attempt_id: attemptID, sale_id: "deal", state: "QUEUED" } });
      case "/api/notifications": return route.fulfill({ json: attemptID ? [{ event_id: "settled", attempt: {
        attempt_id: attemptID, sale_id: "deal", state: "CONFIRMED", order_id: attemptID,
      } }] : [] });
      default: return route.fulfill({ status: 404, json: {} });
    }
  });
  await page.goto("/");
  const card = page.getByRole("article", { name: "Speaker" });
  await expect(card.getByText("2 of 2 left")).toBeVisible();
  await expect(page.getByRole("navigation").getByRole("button", { name: "Cart" })).toBeVisible();
  await card.getByRole("button", { name: "Add to cart" }).click();
  await page.getByRole("navigation").getByRole("button", { name: /^Cart/ }).click();
  await page.getByRole("button", { name: "Place order" }).click();
  await expect(page.getByRole("heading", { name: "My orders" })).toBeVisible();
  await expect(page.getByText("Paid and confirmed. You got it!")).toBeVisible();
});
