import { defineConfig } from "@playwright/test";

// Uses the system Chrome install (channel: "chromium" resolved via
// executablePath below is not needed once Playwright's own browser is
// installed) — see e2e/README note if `npx playwright install` can't
// reach its CDN from this network; channel: "chrome" is the fallback.
export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "http://localhost:3000",
    channel: "chrome",
  },
  webServer: {
    command: "npm run dev",
    url: "http://localhost:3000",
    reuseExistingServer: true,
    timeout: 30_000,
  },
});
