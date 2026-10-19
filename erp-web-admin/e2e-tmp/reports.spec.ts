import { test, expect } from "@playwright/test";

// End-to-end coverage for the Reports screen (erp-core-go's
// phased_roadmap.md Phase 1 "Basic reports" / Phase 2 "consolidated
// cross-branch reporting") — backend endpoints existed since those
// phases, but no client ever surfaced them until this pass. Not
// permission-gated, so no denial path to test.
test("daily sales, EOD cash, stock summary, and consolidated reports all render real data", async ({ page }) => {
  await test.step("login", async () => {
    await page.goto("/login");
    await page.fill("#email", "ravi@acme-sports.test");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
  });

  await page.goto("/reports");

  await test.step("branch/date reports render", async () => {
    await expect(page.getByText("Daily sales, EOD cash, and stock summary")).toBeVisible();
    await expect(page.getByText(/^Orders:/)).toBeVisible();
    await expect(page.getByText(/^EOD cash:/)).toBeVisible();
  });

  await test.step("consolidated sales renders a row per branch", async () => {
    await expect(page.getByText("Consolidated sales (all branches)")).toBeVisible();
    await expect(page.getByRole("cell", { name: "MG Road" })).toBeVisible();
  });

  await test.step("consolidated stock renders at least the seed variant", async () => {
    await expect(page.getByText("Consolidated stock (all branches)")).toBeVisible();
    // Stock Summary above also lists the same product in a table cell —
    // scope to the paragraph consolidated stock actually renders it in.
    await expect(page.locator("p", { hasText: /SG Cricket Bat/ })).toBeVisible();
  });
});
