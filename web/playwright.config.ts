import { defineConfig } from "@playwright/test";

const port = Number(process.env.FLASHKART_E2E_PORT || 5173);

export default defineConfig({
  testDir: "./tests",
  testIgnore: "**/clerk/**",
  outputDir: "../build/playwright",
  workers: 1,
  timeout: 240_000,
  expect: { timeout: 15_000 },
  use: { baseURL: `http://127.0.0.1:${port}`, trace: "retain-on-failure" },
  webServer: {
    command: `npm run dev -- --port ${port} --strictPort`,
    env: { VITE_AUTH_MODE: "demo" },
    url: `http://127.0.0.1:${port}`,
    reuseExistingServer: false,
  },
});
