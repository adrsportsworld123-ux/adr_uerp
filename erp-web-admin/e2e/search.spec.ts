import { test, expect } from "@playwright/test";

// End-to-end coverage for Product Search through the real UI against a
// real erp-core-go + OpenSearch backend. Requires the same setup as
// purchase-and-accounting.spec.ts (backend running, seed password set),
// plus docker-compose's opensearch service healthy and the catalog
// reindexed at least once (POST /search/reindex, or just having run the
// backend's live-verification pass, which already indexed the seed
// catalog). Runs as the default seed POS User — GET /products/search
// carries no permission gate beyond being authenticated.
test("free-text search, fuzzy typo tolerance, and price filtering", async ({ page }) => {
  await test.step("login as POS User (Ravi)", async () => {
    await page.goto("/login");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
  });

  await page.goto("/search");

  await test.step("exact query matches the seed product", async () => {
    await page.fill("#q", "cricket");
    await expect(page.getByRole("cell", { name: "SG Cricket Bat" })).toBeVisible();
  });

  await test.step("a typo'd query still fuzzy-matches the same product", async () => {
    await page.fill("#q", "crikcet");
    await expect(page.getByRole("cell", { name: "SG Cricket Bat" })).toBeVisible();
  });

  await test.step("a price filter that excludes the seed product returns no results", async () => {
    await page.fill("#q", "");
    await page.fill("#minPrice", "999999");
    await expect(page.getByText("No matching products")).toBeVisible();
  });
});
