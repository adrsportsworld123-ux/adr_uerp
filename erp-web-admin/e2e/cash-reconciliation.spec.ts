import { test, expect } from "@playwright/test";

// End-to-end coverage for Phase 4's Reconciliation & Audit sub-area's
// first piece, Cash Reconciliation (erp-core-go's phased_roadmap.md),
// against a real erp-core-go backend. Unlike every other gated screen in
// this app, there's no route-level permission to deny here — approval is
// an inline PIN check against whichever user ID is entered (mirroring
// ApplyDiscount's own manual-discount authorization), open to any
// authenticated user for the ordinary zero-variance case. So this test
// covers the PIN check itself (wrong PIN rejected, right PIN accepted)
// rather than a permission-denied path.
//
// Uses dates far in the past (no real sales data exists there) so
// system-expected cash sales are reliably zero and the test's own math
// is fully deterministic, and a fresh date per test.step so this test
// can be re-run without colliding with `cash_reconciliations`' one-
// reconciliation-per-branch-per-day uniqueness constraint.
test("zero-variance count auto-closes, a variance needs a manager PIN, and history reflects both", async ({ page }) => {
  const unique = Date.now() % 1000;
  // A distinctive, far-past date derived from `unique` so repeated test
  // runs never collide with each other or with real data.
  const zeroDate = new Date(2020, 0, 1 + (unique % 300)).toISOString().slice(0, 10);
  const varianceDate = new Date(2020, 0, 301 + (unique % 300)).toISOString().slice(0, 10);

  await test.step("login", async () => {
    await page.goto("/login");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
  });

  await test.step("a zero-variance count auto-closes with no approval fields", async () => {
    await page.goto("/cash-reconciliation");
    await page.fill("#date", zeroDate);
    // GET /reports/eod-cash returns "0" (not "0.00") for a date with no
    // cash orders — a pre-existing Phase 1 formatting quirk, not
    // something this pass touches; match loosely rather than assume
    // "0.00".
    await expect(page.getByText(/System cash sales for this date: ₹0/)).toBeVisible();

    await page.fill("#openingFloat", "500");
    // One 500-rupee note — counted total 500, matching opening float
    // exactly since system cash sales for this date are 0. Target the
    // ₹500 count input by its own label's sibling div, not a fragile
    // index guess into the denomination grid.
    const fiveHundredInput = page.locator("div").filter({ hasText: /^₹500$/ }).locator("input");
    await fiveHundredInput.fill("1");

    await expect(page.getByText("Variance: ₹0.00")).toBeVisible();
    await expect(page.getByText(/needs a reason/)).toHaveCount(0);

    await page.getByRole("button", { name: "Submit reconciliation" }).click();
    await expect(page.getByText("Reconciled — no variance")).toBeVisible();
  });

  await test.step("a variance shows the approval box and rejects a wrong PIN", async () => {
    await page.goto("/cash-reconciliation");
    await page.fill("#date", varianceDate);
    await expect(page.getByText(/System cash sales for this date: ₹0/)).toBeVisible();

    await page.fill("#openingFloat", "0");
    const hundredInput = page.locator("div").filter({ hasText: /^₹100$/ }).locator("input");
    await hundredInput.fill("1");

    await expect(page.getByText(/Variance: ₹100\.00 \(overage\)/)).toBeVisible();
    await expect(page.getByText(/needs a reason/)).toBeVisible();

    await page.fill("#reason", "E2E test variance");
    await page.fill("#authorizedBy", "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"); // Arjun, Merchant Admin
    await page.fill("#authorizedPin", "0000"); // wrong PIN (real one is 1234, set for backend verification)
    await page.getByRole("button", { name: "Submit reconciliation" }).click();
    // .text-red-600 scopes to the actual API error, not the always-shown
    // amber hint paragraph that also contains "Branch Manager or Merchant
    // Admin" and would otherwise cause a strict-mode collision.
    await expect(page.locator(".text-red-600", { hasText: /VARIANCE_NOT_AUTHORIZED|Branch Manager or Merchant Admin/i })).toBeVisible();
  });

  await test.step("the same variance succeeds with the correct PIN", async () => {
    await page.fill("#authorizedPin", "1234");
    await page.getByRole("button", { name: "Submit reconciliation" }).click();
    await expect(page.getByText("Reconciled with variance, adjustment posted")).toBeVisible();
  });

  await test.step("history shows both reconciled dates", async () => {
    await page.fill("#histStart", zeroDate < varianceDate ? zeroDate : varianceDate);
    await page.fill("#histEnd", zeroDate > varianceDate ? zeroDate : varianceDate);
    await expect(page.getByRole("cell", { name: zeroDate })).toBeVisible();
    await expect(page.getByRole("cell", { name: varianceDate })).toBeVisible();
  });
});
