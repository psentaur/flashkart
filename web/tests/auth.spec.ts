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
    await expect(nav.getByRole("button", { name: "Storefront" })).toBeVisible();
    if (role !== "guest") await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
    for (const label of ["Analytics", "Administration", "Replicas"]) {
      if (role === "admin" || role === "owner") await expect(nav.getByRole("button", { name: label })).toBeVisible();
      else await expect(nav.getByRole("button", { name: label })).toHaveCount(0);
    }
    await expect(nav.getByRole("button", { name: "Activity" })).toHaveCount(role === "guest" ? 0 : 1);
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
    return route.fulfill({ json: { data: invited ? [{ id: "invite", email: "new@example.com", status: "pending" }] : [], total_count: invited ? 1 : 0 } });
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
