import { test, expect } from "@playwright/test";

// End-to-end coverage for Purchase Management + Accounting through the
// real UI against a real erp-core-go backend — not mocked. Requires the
// backend from erp-core-go's docker-compose to be running on :8080 with
// the seed data from migrations/002_seed.sql, and the seed user's password
// set via POST /dev/set-password to "Passw0rd!" (see erp-core-go's
// README). This is the E2E layer docs/tech_stack_decision.md's testing
// section already named for this app ("Playwright for the back-office web
// app") — written after the fact, once the screens existed, rather than
// before; a real gap in the process, not just in coverage.
//
// One long sequential test rather than isolated per-step tests: later
// steps genuinely depend on state earlier steps create (a GRN must exist
// and be completed before it can be billed), so splitting them would just
// mean re-deriving that state in every test.
test("supplier → GRN → bill → payment → day book, end to end", async ({ page }) => {
  const uniqueSuffix = Date.now();
  const supplierName = `E2E Test Supplier ${uniqueSuffix}`;

  await test.step("login", async () => {
    await page.goto("/login");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
    await expect(page.locator("h1")).toContainText("Dashboard");
  });

  await test.step("create a supplier", async () => {
    await page.goto("/suppliers/new");
    await page.fill("#name", supplierName);
    await page.fill("#gstin", "29TESTGSTIN1Z5");
    await page.click('button[type="submit"]');
    await page.waitForURL("/suppliers");
    await expect(page.getByText(supplierName)).toBeVisible();
  });

  await test.step("chart of accounts shows seeded system accounts", async () => {
    await page.goto("/accounting/chart-of-accounts");
    await expect(page.getByRole("cell", { name: "1001" })).toBeVisible();
    await expect(page.getByRole("cell", { name: "Cash", exact: true })).toBeVisible();
    await expect(page.getByRole("cell", { name: "4001" })).toBeVisible();
  });

  let grnNumber = "";
  await test.step("create a GRN and add a line via barcode lookup", async () => {
    await page.goto("/purchase/grn/new");
    await page.click("[data-slot='select-trigger']");
    await page.click(`[data-slot='select-item']:has-text("${supplierName}")`);
    await page.fill("#freight", "20");
    await page.click('button[type="submit"]');
    await page.waitForURL(/\/purchase\/grn\/[0-9a-f-]+$/);
    grnNumber = (await page.locator("h1").textContent())?.trim() ?? "";
    expect(grnNumber).toMatch(/^GRN-/);

    await page.fill("#barcode", "8901234567890");
    await page.click("text=Look up");
    await page.waitForSelector("#quantity");
    await page.fill("#quantity", "3");
    await page.fill("#unitCost", "1000");
    await page.click("text=Add line");
    await expect(page.getByText("SG-BAT-SH")).toBeVisible();
  });

  await test.step("complete the GRN", async () => {
    await page.click("text=Complete GRN");
    // Scoped to the status badge specifically — a plain getByText("completed")
    // also matches the toast's "GRN completed — stock and cost updated",
    // a strict-mode violation once both are on screen at once.
    await expect(page.locator("[data-slot='badge']", { hasText: "completed" })).toBeVisible();
  });

  await test.step("bill the GRN and confirm landed-cost math", async () => {
    await page.goto("/purchase/bills/new");
    await page.click("[data-slot='select-trigger']");
    await page.click(`[data-slot='select-item']:has-text("${grnNumber}")`);
    await page.fill("#tax", "540");
    await page.click('button[type="submit"]');
    await page.waitForURL(/\/purchase\/bills\/[0-9a-f-]+$/);
    // 3 units @ 1000 + 20 freight + 540 GST = 3560.00 — shown twice
    // (grand total and, before any payment, the equal balance).
    await expect(page.getByText("Grand total")).toBeVisible();
    await expect(page.getByText("3560.00").first()).toBeVisible();
  });

  await test.step("pay the bill in full", async () => {
    await page.fill("#amount", "3560");
    await page.click("text=Record payment");
    await expect(page.getByText("paid", { exact: true })).toBeVisible();
  });

  await test.step("day book reflects both journal entries", async () => {
    await page.goto("/accounting/day-book");
    // .last(): the day book accumulates every entry ever posted today
    // across repeated runs of this spec against the same dev database —
    // the most recently posted entry is this run's.
    await expect(page.getByText("purchase_bill").last()).toBeVisible();
    await expect(page.getByText("bill_payment").last()).toBeVisible();
  });

  await test.step("logout", async () => {
    await page.goto("/dashboard");
    await page.click("text=Log out");
    await page.waitForURL("/login");
  });
});
