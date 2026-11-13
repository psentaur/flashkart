import { expect, test } from "@playwright/test";
import type { Terms } from "../src/api";

test("locks draft terms during save and retries a lost publication response", async ({ page }) => {
  let releaseSave!: () => void;
  const allowSave = new Promise<void>(resolve => { releaseSave = resolve; });
  let saved: Terms | undefined;
  const operations: string[] = [];

  await page.route("**/api/**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/admin/sale-drafts") {
      saved = route.request().postDataJSON();
      await allowSave;
      await route.fulfill({ status: 201, json: { sale_id: "sale", terms: saved, state: "DRAFT" } });
    } else if (path === "/api/admin/sale-drafts/sale/publish") {
      operations.push(route.request().postDataJSON().operation_id);
      if (operations.length === 1) await route.abort();
      else await route.fulfill({ json: { sale_id: "sale", operation_id: operations[0], state: "PUBLISHED" } });
    } else if (path.startsWith("/api/admin/publications/")) {
      await route.fulfill({ json: { sale_id: "sale", operation_id: operations[0], state: "PUBLISHED" } });
    } else if (path === "/api/sales/sale") {
      await route.fulfill({ json: { sale_id: "sale", terms: saved } });
    } else {
      await route.fulfill({ json: [] });
    }
  });

  await page.goto("/");
  await page.getByRole("button", { name: "Access tokens" }).click();
  await page.getByLabel("Administrator token").fill("test-admin");
  await page.getByRole("button", { name: "Access tokens" }).click();
  await page.getByRole("button", { name: "Administration", exact: true }).click();
  await page.getByLabel("Sale product ID").fill("product");
  const stock = page.getByLabel("Stock limit");
  await stock.fill("10");
  await page.getByRole("button", { name: "Create draft" }).click();
  try {
    await expect(stock).toBeDisabled();
  } finally {
    releaseSave();
  }
  await expect(stock).toBeEnabled();
  await expect(stock).toHaveValue("10");
  expect(saved?.stock).toBe(10);
  await page.getByRole("button", { name: "Publish sale", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("could not be completed");
  await expect(stock).toBeDisabled();
  await expect(page.getByRole("button", { name: "Save draft changes" })).toBeDisabled();
  await page.getByRole("button", { name: "Retry publication" }).click();
  await expect(page.getByRole("heading", { name: "Published terms" })).toBeVisible();
  expect(operations).toEqual([operations[0], operations[0]]);
  expect(operations[0]).toBeTruthy();
});
