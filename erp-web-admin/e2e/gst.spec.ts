import { test, expect } from "@playwright/test";

// End-to-end coverage for Phase 4's second sub-area, GST Returns
// (erp-core-go's phased_roadmap.md), against a real erp-core-go backend.
// Not permission-gated on the backend (matches the rest of
// internal/accounting's reports), so this runs as the default POS User —
// unlike Credit/Notifications, there's no permission-gate path to cover.
// The seeded merchant's data (from this repo's own prior verification
// runs, all dated the current month) already produces a non-trivial
// GSTR-1/3B, so this is a real read against real numbers, not a
// synthetic zero-state.
test("GST Returns screen loads a real period's summary and reconciliation", async ({ page }) => {
  await test.step("login", async () => {
    await page.goto("/login");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
  });

  await test.step("GSTR-3B and GSTR-1 both render with a reconciliation status", async () => {
    await page.goto("/gst");
    await expect(page.getByText("GSTR-3B — monthly summary")).toBeVisible();
    await expect(page.getByText("GSTR-1 — outward supplies")).toBeVisible();
    await expect(page.getByText(/Reconciled|Mismatch/).first()).toBeVisible();
  });

  await test.step("the raw JSON panel expands and can be copied", async () => {
    await page.getByText("gstr1_json", { exact: true }).click();
    await expect(page.locator("pre").first()).toContainText('"gstin"');
  });

  await test.step("switching to a period with no data shows empty totals, not an error", async () => {
    await page.fill("#period", "2020-01");
    await expect(page.getByText("₹0.00").first()).toBeVisible();
  });
});
