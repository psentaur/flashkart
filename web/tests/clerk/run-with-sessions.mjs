// Run deployment checks with renewable tokens from existing authenticated browser states.
// State files contain credentials: keep them outside version control, mode 0600.
import { chromium } from "@playwright/test";
import { createServer } from "node:http";
import { randomBytes } from "node:crypto";
import { spawn } from "node:child_process";

const args = process.argv.slice(2);
if (args[0] === "--") args.shift();
if (!args.length || !process.env.BASE_URL || !process.env.CLERK_TEST_ADMIN_STATE || !process.env.CLERK_TEST_CUSTOMER_STATE) {
  console.error("Set BASE_URL, CLERK_TEST_ADMIN_STATE, CLERK_TEST_CUSTOMER_STATE, then pass -- <test command>.");
  process.exit(2);
}
const browser = await chromium.launch();
const pages = {};
let child;
const path = "/" + randomBytes(24).toString("hex");
const server = createServer(async (request, response) => {
  response.setHeader("Cache-Control", "no-store");
  if (request.method !== "GET" || request.url !== path) { response.writeHead(404).end(); return; }
  try {
    const tokens = {};
    for (const [role, page] of Object.entries(pages)) {
      tokens[role] = await page.evaluate(() => window.Clerk.session?.getToken());
      if (!tokens[role]) throw new Error("Session expired");
    }
    response.setHeader("Content-Type", "application/json");
    response.end(JSON.stringify(tokens));
  } catch {
    response.writeHead(503).end("Test sessions expired; capture fresh browser states.");
  }
});
try {
  for (const role of ["admin", "customer"]) {
    const context = await browser.newContext({ storageState: process.env[`CLERK_TEST_${role.toUpperCase()}_STATE`] });
    const page = await context.newPage();
    await page.goto(process.env.BASE_URL);
    await page.waitForFunction(() => window.Clerk?.loaded && window.Clerk.session, undefined, { timeout: 30_000 });
    const identity = await page.evaluate(async () => {
      const token = await window.Clerk.session.getToken();
      const response = await fetch("/api/auth/me", { headers: { Authorization: `Bearer ${token}` } });
      if (!response.ok) throw new Error("Test session rejected");
      return response.json();
    });
    if (role === "admin" ? !["admin", "owner"].includes(identity.role) : identity.role !== "customer") {
      throw new Error(`The ${role} browser state has the wrong role.`);
    }
    pages[role] = page;
  }
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  child = spawn(args[0], args.slice(1), { stdio: "inherit", env: { ...process.env,
    FLASHKART_TEST_TOKEN_URL: `http://127.0.0.1:${server.address().port}${path}` } });
  process.once("SIGINT", () => child.kill("SIGINT"));
  process.once("SIGTERM", () => child.kill("SIGTERM"));
  const result = await new Promise((resolve, reject) => {
    child.once("error", reject); child.once("exit", (code, signal) => resolve(code ?? (signal ? 1 : 0)));
  });
  process.exitCode = result;
} catch (error) {
  console.error(error instanceof Error ? error.message : "Clerk session helper failed.");
  process.exitCode = 1;
} finally {
  server.close();
  await browser.close();
}
