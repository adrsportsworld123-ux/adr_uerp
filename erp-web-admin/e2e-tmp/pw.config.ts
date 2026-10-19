import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: ".",
  timeout: 120000,
  workers: 1,
  use: { baseURL: "http://localhost:3001", channel: "chrome", headless: true },
});
