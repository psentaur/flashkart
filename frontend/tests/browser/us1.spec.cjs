const { test, expect } = require('@playwright/test');
const { readFileSync } = require('node:fs');
const { randomUUID } = require('node:crypto');
const path = require('node:path');

test('operator publishes, replaces, cancels and freezes; shoppers browse authoritative offers', async ({ page, request }, testInfo) => {
  const manifest = JSON.parse(readFileSync(process.env.SEEDED_TOKENS_FILE, 'utf8'));
  const operator = manifest.identities.find((p) => p.role === 'OPERATOR').token;
  const shopper = manifest.identities.find((p) => p.role === 'SHOPPER').token;
  const headers = { Authorization: `Bearer ${operator}` };
  const errors = []; page.on('pageerror', (error) => errors.push(error.message));
  const baseTime = Date.now(); const start = new Date(baseTime + 10 * 60000); const end = new Date(baseTime + 20 * 60000);
  const timestamp = (date) => date.toISOString().slice(0, 19);
  const product = randomUUID(); const accepted = new Set();
  await page.clock.install({ time: new Date(baseTime) });
  async function fillSale(productID, from = start, through = end) {
    const form = page.locator('#sale-form');
    await form.getByLabel('Product ID', { exact: true }).fill(productID);
    await form.getByLabel('Starts at (local time)', { exact: true }).fill(timestamp(from));
    await form.getByLabel('Ends at (local time)', { exact: true }).fill(timestamp(through));
  }
  async function save(method = 'POST', status = 201) {
    const waiting = page.waitForResponse((r) => r.request().method() === method && r.url().includes('/v1/admin/sales'));
    await page.getByRole('button', { name: method === 'POST' ? 'Publish sale' : 'Replace configuration', exact: true }).click();
    const response = await waiting; expect(response.status()).toBe(status); const sale = await response.json(); if (status < 400) accepted.add(sale.sale_id); return sale;
  }
  try {
    await page.goto('/operator/sales');
    await expect(page.locator('#operator-workspace')).toBeHidden();
    await page.getByLabel('Operator token', { exact: true }).fill(shopper);
    await page.getByRole('button', { name: 'Use token', exact: true }).click();
    await expect(page.locator('#operator-workspace')).toBeHidden();
    const denied = await request.post('/v1/admin/sales', { headers: { Authorization: `Bearer ${shopper}` }, data: {} }); expect(denied.status()).toBe(403);
    await page.getByLabel('Operator token', { exact: true }).fill(operator);
    await page.getByRole('button', { name: 'Use token', exact: true }).click();
    await expect(page.getByLabel('Operator token', { exact: true })).toHaveValue('');
    await expect(page.locator('#sale-form input[name=per_shopper_unit_cap]')).toHaveValue('1');
    await fillSale(product); const first = await save(); expect(first.per_shopper_unit_cap).toBe(1);
    await expect.poll(async () => { const response = await request.get(`/v1/admin/sales/${first.sale_id}`, { headers }); return (await response.json()).ready_participants; }).toContain('admission');
    await page.getByRole('button', { name: 'Refresh readiness', exact: true }).click();
    await expect(page.locator('#readiness')).toContainText('admission: Ready for this version');
    await expect(page.locator('#readiness')).toContainText('catalog: Awaiting matching confirmation');
    await page.getByRole('button', { name: 'New sale', exact: true }).click(); await fillSale(product); await save('POST', 409);
    await expect(page.locator('#sale-message')).toContainText('overlaps this window');
    await page.getByRole('button', { name: 'New sale', exact: true }).click(); await fillSale(product, end, new Date(baseTime + 30 * 60000)); await save();
    await page.getByRole('button', { name: 'New sale', exact: true }).click(); await fillSale(randomUUID()); await save();
    // Cancel the original sale and reuse its now-free interval.
    await page.locator('#load-sale input[name=sale_id]').fill(first.sale_id);
    await page.getByRole('button', { name: 'Load sale & readiness', exact: true }).click();
    await expect(page.locator('#sale-form input[name=sale_id]')).toHaveValue(first.sale_id);
    await page.getByRole('button', { name: 'Cancel sale', exact: true }).click();
    await expect(page.locator('#sale-message')).toContainText('Sale cancelled');
    await page.getByRole('button', { name: 'New sale', exact: true }).click(); await fillSale(product); const reused = await save();
    await page.locator('#sale-form input[name=per_shopper_unit_cap]').fill('4');
    // Move the window later; the original start must still freeze the UI.
    await page.locator('#sale-form input[name=starts_at]').fill(timestamp(new Date(baseTime + 40 * 60000)));
    await page.locator('#sale-form input[name=ends_at]').fill(timestamp(new Date(baseTime + 50 * 60000)));
    const revised = await save('PUT', 200); expect(revised.per_shopper_unit_cap).toBe(4); expect(revised.ready_participants).toEqual([]);
    await expect(page.locator('#sale-summary')).toContainText('Up to 4');
    await page.screenshot({ path: testInfo.outputPath('operator-desktop.png'), fullPage: true });
    await page.clock.fastForward(11 * 60000);
    await expect(page.locator('#sale-form input[name=per_shopper_unit_cap]')).toBeDisabled();
    await expect(page.locator('#sale-form input[name=starts_at]')).toBeDisabled();
    await expect(page.getByRole('button', { name: 'Replace configuration', exact: true })).toBeDisabled();
    await page.setViewportSize({ width: 390, height: 844 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath('operator-mobile.png'), fullPage: true });
    await page.getByRole('button', { name: 'Sign out', exact: true }).click();
    await expect(page.locator('#operator-workspace')).toBeHidden();
    // The Catalog boundary is a browser fixture; sale terms still come from live Flash Sale.
    let conflict = false; let unavailable = false;
    const value = { name: 'Limited <b>release</b>', description: 'A controlled Catalog fixture', image_refs: [] };
    await page.route('**/v1/products?*', (route) => route.fulfill({ status: unavailable ? 503 : 200, json: unavailable ? { code: 'QUORUM_UNAVAILABLE', message: 'Fixture quorum unavailable' } : { items: [{ product_id: product, metadata_status: conflict ? 'CONFLICT' : 'AVAILABLE', ...(conflict ? {} : { value }) }], next_cursor: null } }));
    await page.route(`**/v1/products/${product}`, (route) => route.fulfill({ status: conflict ? 409 : 200, json: conflict ? { code: 'METADATA_CONFLICT', message: 'Fixture conflict' } : { product_id: product, value, context: { 'node-a': 1 }, offers: [{ ...revised, unit_price_minor: 999 }] } }));
    await page.goto('/');
    await expect(page.getByRole('heading', { name: value.name, exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'View release', exact: true }).click();
    await expect(page.locator('#product-detail')).toContainText('2500 INR minor units');
    await expect(page.locator('#product-detail')).toContainText('Up to 4');
    await expect(page.locator('#product-detail')).not.toContainText('999 INR');
    expect(await page.locator('#product-detail b').count()).toBe(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath('storefront-mobile.png'), fullPage: true });
    await page.setViewportSize({ width: 1365, height: 900 });
    await page.screenshot({ path: testInfo.outputPath('storefront-desktop.png'), fullPage: true });
    conflict = true; await page.getByRole('button', { name: 'View release', exact: true }).click();
    await expect(page.locator('#product-detail')).toContainText('conflicting versions');
    unavailable = true; await page.getByRole('button', { name: 'Refresh products', exact: true }).click();
    await expect(page.locator('#catalog-message')).toContainText('temporarily unavailable');
    expect(errors).toEqual([]);
  } finally {
    // Only these unique, pre-start browser fixtures are cancelled. Keep their audit records.
    for (const id of accepted) { const response = await request.get(`/v1/admin/sales/${id}`, { headers }); if (response.ok()) { const current = await response.json(); if (current.status === 'UPCOMING') { const cancelled = await request.post(`/v1/admin/sales/${id}/cancel`, { headers, data: { expected_version: current.version } }); expect(cancelled.ok()).toBe(true); } } }
  }
});
