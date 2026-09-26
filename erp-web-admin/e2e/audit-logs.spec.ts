import { test, expect } from "@playwright/test";

// End-to-end coverage for Phase 4's "immutable audit trail" item
// (erp-core-go's phased_roadmap.md / docs/phase0_1_design.md §3.19) —
// closed 2026-09-22. Gated by audit.view (Merchant Admin only, stricter
// than most reads in this app).
test("audit log lists real entries, chain verifies, and a POS User is denied", async ({ page, request }) => {
  const unique = Date.now() % 1000000;
  await test.step("seed a real audit entry via a direct API call (a stock adjustment)", async () => {
    const login = await request.post("http://localhost:8080/api/v1/auth/login", {
      data: { merchant_code: "acme-sports", email: "arjun@acme-sports.test", password: "Passw0rd!" },
    });
    const { access_token } = await login.json();
    const headers = { Authorization: `Bearer ${access_token}` };
    await request.post("http://localhost:8080/api/v1/inventory/adjustments", {
      headers,
      data: {
        variant_id: "99999999-9999-9999-9999-999999999999",
        branch_id: "22222222-2222-2222-2222-222222222222",
        quantity_delta: 1,
        reason: `E2E audit log seed ${unique}`,
      },
    });
    await request.post("http://localhost:8080/api/v1/inventory/adjustments", {
      headers,
      data: {
        variant_id: "99999999-9999-9999-9999-999999999999",
        branch_id: "22222222-2222-2222-2222-222222222222",
        quantity_delta: -1,
        reason: `E2E audit log seed undo ${unique}`,
      },
    });
  });

  await test.step("login as Merchant Admin (Arjun) and see the seeded entry", async () => {
    await page.goto("/login");
    await page.fill("#email", "arjun@acme-sports.test");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    await page.goto("/audit-logs");
    await page.fill("#entityType", "stock_levels");
    await expect(page.getByText(`E2E audit log seed undo ${unique}`)).toBeVisible();
  });

  await test.step("verify the chain reports valid with legacy rows skipped", async () => {
    await page.getByRole("button", { name: "Verify tamper-evident chain" }).click();
    // Scoped to <span> to avoid a strict-mode collision with the sonner
    // toast (a <div>) that transiently shows the same text — confirmed
    // live: on a database with zero legacy rows, the toast and the
    // persistent result card both say "...0 pre-hardening row(s)
    // skipped" and can both be on screen at once.
    await expect(page.locator("span", { hasText: /pre-hardening row\(s\) skipped/ })).toBeVisible();
  });

  await test.step("POS User (Ravi) is denied the audit log", async () => {
    await page.goto("/dashboard");
    await page.click("text=Log out");
    await page.waitForURL("/login");
    await page.fill("#email", "ravi@acme-sports.test");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    await page.goto("/audit-logs");
    await expect(page.getByText(/audit\.view/)).toBeVisible();
  });
});
