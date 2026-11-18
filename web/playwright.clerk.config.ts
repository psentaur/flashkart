import { defineConfig } from "@playwright/test";

process.loadEnvFile("../.env");

export default defineConfig({
  testDir: "./tests/clerk",
  outputDir: "../build/playwright-clerk",
  workers: 1,
  timeout: 60_000,
  expect: { timeout: 15_000 },
  use: { baseURL: "http://127.0.0.1:5173", trace: "retain-on-failure" },
});
