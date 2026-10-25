const { defineConfig } = require('@playwright/test');
const path = require('node:path');
const report = process.env.BROWSER_EVIDENCE_DIR || path.resolve(__dirname, '../.cache/evidence/browser');
module.exports = defineConfig({
  testDir: './tests/browser',
  timeout: 60000,
  expect: { timeout: 5000 },
  workers: 1,
  retries: 0,
  outputDir: path.join(report, 'artifacts'),
  reporter: [['list'], ['json', { outputFile: path.join(report, 'report.json') }]],
  use: {
    browserName: 'chromium',
    headless: true,
    baseURL: process.env.GATEWAY_URL || 'http://127.0.0.1:8080',
    viewport: { width: 1365, height: 900 },
    timezoneId: 'UTC',
    // Local tokens must not be retained in network traces.
    trace: 'off',
    screenshot: 'only-on-failure',
  },
});
