import { test, expect } from "@playwright/test";

// End-to-end coverage for the Inventory screen (erp-core-go's
// phased_roadmap.md Phase 1 "stock adjustments with audit trail") — GET
// /inventory and POST /inventory/adjustments existed since Phase 1, but
// no client ever surfaced a general stock-lookup/manual-correction
// screen; PATCH /inventory/reorder-point ended up on the Notifications
// screen as a documented workaround. This closes the gap. Runs as
// Branch Manager (Meera) since the adjustment write is gated by
// inventory.adjust.
const SEED_VARIANT_LABEL = "SG Cricket Bat";

test("stock lookup and a manual adjustment round-trip through the real backend", async ({ page }) => {
  await test.step("login as Branch Manager (Meera)", async () => {
    await page.goto("/login");
    await page.fill("#email", "meera@acme-sports.test");
    await page.fill("#password", "9999");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
  });

  await page.goto("/inventory");

  let onHandBefore = 0;
  const onHandValue = () => page.locator("div", { hasText: /^On hand:/ }).last().locator("span");

  await test.step("look up the seed variant's current stock", async () => {
    await page.getByRole("combobox").nth(1).click();
    await page.getByRole("option", { name: new RegExp(SEED_VARIANT_LABEL) }).click();
    await page.getByRole("button", { name: "Look up" }).click();
    const onHandText = await onHandValue().innerText();
    onHandBefore = Number(onHandText);
    expect(Number.isFinite(onHandBefore)).toBeTruthy();
  });

  await test.step("apply a +2 manual adjustment and see the lookup reflect it", async () => {
    await page.fill("#delta", "2");
    await page.fill("#reason", "E2E inventory screen test");
    await page.getByRole("button", { name: "Apply" }).click();
    await expect(page.getByText("Stock adjusted")).toBeVisible();
    await expect
      .poll(async () => Number(await onHandValue().innerText()))
      .toBe(onHandBefore + 2);
  });

  await test.step("restore stock with a matching -2 adjustment", async () => {
    await page.fill("#delta", "-2");
    await page.fill("#reason", "E2E cleanup");
    await page.getByRole("button", { name: "Apply" }).click();
    await expect
      .poll(async () => Number(await onHandValue().innerText()))
      .toBe(onHandBefore);
  });

  await test.step("POS User (Ravi) is denied adjusting stock", async () => {
    await page.goto("/dashboard");
    await page.click("text=Log out");
    await page.waitForURL("/login");
    await page.fill("#email", "ravi@acme-sports.test");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    await page.goto("/inventory");
    await page.getByRole("combobox").nth(1).click();
    await page.getByRole("option", { name: new RegExp(SEED_VARIANT_LABEL) }).click();
    await page.fill("#delta", "1");
    await page.fill("#reason", "should be denied");
    await page.getByRole("button", { name: "Apply" }).click();
    await expect(page.getByText("you don't have the inventory.adjust permission")).toBeVisible();
  });
});
