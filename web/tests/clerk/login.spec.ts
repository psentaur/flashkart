import { expect, test } from "@playwright/test";
import { clerk, clerkSetup, setupClerkTestingToken } from "@clerk/testing/playwright";
import { createClerkClient } from "@clerk/backend";
import { randomBytes } from "node:crypto";
import { chmod } from "node:fs/promises";
import { spawn } from "node:child_process";

const backend = createClerkClient({ secretKey: process.env.CLERK_SECRET_KEY });
const created: string[] = [];
let customer: { email: string; password: string; id: string };
let admin: typeof customer;

test.beforeAll(async () => {
  await clerkSetup();
  for (const role of ["customer", "admin"] as const) {
    const email = `flashkart-${role}-${randomBytes(5).toString("hex")}+clerk_test@example.com`;
    const password = randomBytes(24).toString("base64url");
    const user = await backend.users.createUser({ emailAddress: [email], password, publicMetadata: { role } });
    created.push(user.id);
    const account = { email, password, id: user.id };
    if (role === "customer") customer = account; else admin = account;
  }
});
test.afterAll(async () => {
  for (const id of created) await backend.users.deleteUser(id);
});

test("login and registration offer Google and email", async ({ page }) => {
  await setupClerkTestingToken({ page });
  await page.goto("/login");
  await expect(page.getByRole("button", { name: /Google/ })).toBeVisible();
  await expect(page.getByLabel("Email address", { exact: true })).toBeVisible();
  await page.screenshot({ path: "../build/clerk-login.png", fullPage: true });
  await page.goto("/sign-up");
  await expect(page.getByRole("button", { name: /Google/ })).toBeVisible();
  await expect(page.getByLabel("Email address", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Password", { exact: true })).toBeVisible();
});

test("email registration verifies ownership and creates a customer", async ({ page }) => {
  await setupClerkTestingToken({ page });
  const email = `flashkart-signup-${randomBytes(5).toString("hex")}+clerk_test@example.com`;
  await page.goto("/sign-up");
  await page.getByLabel("Email address", { exact: true }).fill(email);
  await page.getByLabel("Password", { exact: true }).fill(randomBytes(24).toString("base64url"));
  await Promise.all([
    page.waitForResponse(r => r.url().includes("/prepare_verification") && r.status() === 200),
    page.getByRole("button", { name: "Continue", exact: true }).click(),
  ]);
  await page.locator('input[autocomplete="one-time-code"]').fill("424242");
  await expect(page.getByRole("button", { name: "Activity", exact: true })).toBeVisible();
  const id = await page.evaluate(() => window.Clerk.user!.id);
  created.push(id);
  const result = await page.evaluate(async () => {
    const token = await window.Clerk.session!.getToken();
    return fetch("/api/auth/me", { headers: { Authorization: `Bearer ${token}` } }).then(r => r.json());
  });
  expect(result).toEqual({ user_id: id, role: "customer" });
});

test("password sign-in completes Clerk's new-device verification", async ({ page }) => {
  await setupClerkTestingToken({ page });
  await page.goto("/login");
  await page.getByLabel("Email address", { exact: true }).fill(customer.email);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await page.getByLabel("Password", { exact: true }).fill(customer.password);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  const code = page.locator('input[autocomplete="one-time-code"]');
  const outcome = await Promise.race([
    page.getByRole("button", { name: "Activity", exact: true }).waitFor().then(() => "complete"),
    code.waitFor().then(() => "verify"),
  ]);
  if (outcome === "verify") await code.fill("424242");
  await expect(page.getByRole("button", { name: "Activity", exact: true })).toBeVisible();
});

test("password reset verifies email before changing the password", async ({ page }) => {
  await setupClerkTestingToken({ page });
  await page.goto("/login");
  await page.getByLabel("Email address", { exact: true }).fill(customer.email);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await page.getByText("Forgot password?", { exact: true }).click();
  await Promise.all([
    page.waitForResponse(r => r.url().includes("/prepare_first_factor") && r.status() === 200),
    page.getByText("Reset your password", { exact: true }).click(),
  ]);
  const code = page.locator('input[autocomplete="one-time-code"]');
  await code.fill("424242");
  const password = randomBytes(24).toString("base64url");
  const fields = page.locator('input[type="password"]');
  await fields.first().fill(password);
  if (await fields.count() > 1) await fields.nth(1).fill(password);
  await page.getByRole("button", { name: /Reset password|Continue/i }).click();
  await expect(page.getByRole("button", { name: "Activity", exact: true })).toBeVisible();
  customer.password = password;
});

test("real Clerk customer stays out of admin APIs and tabs", async ({ page }) => {
  await page.goto("/");
  await clerk.signIn({ page, emailAddress: customer.email });
  const diagnostic = await page.evaluate(async () => {
    const token = await window.Clerk.session!.getToken();
    const decoded = JSON.parse(atob(token!.split(".")[1].replace(/-/g, "+").replace(/_/g, "/")));
    const r = await fetch("/api/auth/me", { headers: { Authorization: `Bearer ${token}` } });
    return { status: r.status, body: await r.json(), claims: { iss: decoded.iss, azp: decoded.azp, role: decoded.role, sid: !!decoded.sid, nbf: !!decoded.nbf, exp: !!decoded.exp } };
  });
  expect(diagnostic.status, JSON.stringify(diagnostic)).toBe(200);
  await expect(page.getByRole("button", { name: "Activity", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Analytics", exact: true })).toHaveCount(0);
  const result = await page.evaluate(async () => {
    const token = await window.Clerk.session!.getToken();
    const headers = { Authorization: `Bearer ${token}` };
    const me = await fetch("/api/auth/me", { headers }).then(r => r.json());
    const catalog = await fetch("/api/admin/replicas", { headers });
    const team = await fetch("/api/admin/team/users", { headers });
    return { me, catalog: catalog.status, team: team.status };
  });
  expect(result.me).toEqual({ user_id: customer.id, role: "customer" });
  expect(result.catalog).toBe(403); expect(result.team).toBe(403);
  await page.goto("/analytics");
  await expect(page.getByRole("heading", { name: "Access restricted" })).toBeVisible();
  await clerk.signOut({ page });
  await expect(page.getByRole("button", { name: "Activity", exact: true })).toHaveCount(0);
});

test("real Clerk admin claim permits catalog management and blocks Team", async ({ page }) => {
  await page.goto("/");
  await clerk.signIn({ page, emailAddress: admin.email });
  await expect(page.getByRole("button", { name: "Analytics", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Team", exact: true })).toHaveCount(0);
  const result = await page.evaluate(async () => {
    const token = await window.Clerk.session!.getToken();
    const headers = { Authorization: `Bearer ${token}` };
    const me = await fetch("/api/auth/me", { headers }).then(r => r.json());
    const catalog = await fetch("/api/admin/replicas", { headers });
    const team = await fetch("/api/admin/team/users", { headers });
    return { me, catalog: catalog.status, team: team.status };
  });
  expect(result.me).toEqual({ user_id: admin.id, role: "admin" });
  expect(result.catalog).toBe(200); expect(result.team).toBe(403);
  const sessionID = await page.evaluate(() => window.Clerk.session!.id);
  const renewed = await backend.sessions.getToken(sessionID);
  const response = await page.request.get("/api/auth/me", { headers: { Authorization: `Bearer ${renewed.jwt}` } });
  expect(response.status()).toBe(401); // Backend-issued tokens without an allowed browser azp remain rejected.
});

test("deployment helper renews genuine browser sessions", async ({ browser }) => {
  test.setTimeout(120_000);
  const contexts = [];
  try {
    for (const [role, account] of [["customer", customer], ["admin", admin]] as const) {
      const context = await browser.newContext(); contexts.push(context);
      const page = await context.newPage(); await page.goto("http://127.0.0.1:5173/");
      await clerk.signIn({ page, emailAddress: account.email });
      await expect(page.getByRole("button", { name: "Activity", exact: true })).toBeVisible();
      const path = `../build/clerk-${role}-state.json`;
      await context.storageState({ path }); await chmod(path, 0o600);
    }
    const code = [
      "import json,os,urllib.request",
      "from pathlib import Path",
      "with urllib.request.urlopen(os.environ['FLASHKART_TEST_TOKEN_URL']) as r: tokens=json.load(r)",
      "for role,token in tokens.items():",
      " r=urllib.request.Request(os.environ['BASE_URL']+'/api/auth/me',headers={'Authorization':'Bearer '+token})",
      " with urllib.request.urlopen(r) as response: assert json.load(response)['role']==role",
    ].join("\n");
    const result = await new Promise<number>((resolve, reject) => {
      const child = spawn(process.execPath, ["tests/clerk/run-with-sessions.mjs", "--", "python3", "-c", code], {
        env: { ...process.env, BASE_URL: "http://127.0.0.1:5173", CLERK_TEST_ADMIN_STATE: "../build/clerk-admin-state.json", CLERK_TEST_CUSTOMER_STATE: "../build/clerk-customer-state.json" },
        stdio: "inherit",
      });
      child.once("error", reject); child.once("exit", code => resolve(code ?? 1));
    });
    expect(result).toBe(0);
  } finally { for (const context of contexts) await context.close(); }
});
