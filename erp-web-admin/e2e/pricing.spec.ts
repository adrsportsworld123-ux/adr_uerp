import { test, expect } from "@playwright/test";

// End-to-end coverage for Pricing Management through the real UI against
// a real erp-core-go backend. Requires the same setup as
// purchase-and-accounting.spec.ts (backend running, seed password set) —
// see that file's header comment. Runs as the seed Branch Manager
// (password "9999") since pricing changes are gated by the
// pricing.manage permission, which POS User doesn't have.
test("calculator, single-variant negative-margin override, and bulk update", async ({ page }) => {
  await test.step("login as Branch Manager (Meera)", async () => {
    await page.goto("/login");
    await page.fill("#email", "meera@acme-sports.test");
    await page.fill("#password", "9999");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
  });

  await test.step("calculator: cost 1200, target margin 40% -> selling price 2000.00", async () => {
    await page.goto("/pricing");
    await page.fill("#cost", "1200");
    await page.fill("#pct", "40");
    await page.click("text=Calculate");
    await expect(page.getByText("₹2000")).toBeVisible();
  });

  await test.step("editing a variant to sell below cost is blocked, then allowed with override", async () => {
    // First row's Edit button — whichever variant it is, the same
    // block-then-override flow applies.
    await page.getByRole("button", { name: "Edit" }).first().click();
    const priceInput = page.locator("input[type='number']").first();
    await priceInput.fill("1"); // well below any real cost price in the seed catalog
    await page.getByRole("button", { name: "Save" }).click();

    // Blocked: the override checkbox should now appear instead of saving.
    await expect(page.getByText(/override anyway/i)).toBeVisible();
    await page.getByText(/override anyway/i).click();
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("Price updated")).toBeVisible();
    await expect(page.getByText("₹1.00")).toBeVisible();
  });

  await test.step("bulk update: preview then apply a +5% change with override", async () => {
    await page.locator("#value").fill("5");
    await page.getByRole("button", { name: "Preview" }).click();
    await expect(page.getByText("skipped_negative_margin").or(page.getByText("would_update")).first()).toBeVisible();

    // The variant just repriced to ₹1 will still be below any real cost —
    // override to make sure Apply actually has something to commit.
    const overrideCheckbox = page.getByText(/Some items sell below cost/i);
    if (await overrideCheckbox.isVisible()) {
      await overrideCheckbox.click();
    }
    await page.getByRole("button", { name: "Apply" }).click();
    await expect(page.getByText("Bulk price update applied")).toBeVisible();
  });
});
