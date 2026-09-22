import { test, expect, Page } from "@playwright/test";

// End-to-end coverage for Phase 4, sub-area 4's third and last
// reconciliation type, Inventory (erp-core-go's phased_roadmap.md /
// docs/phase0_1_design.md §3.18), against a real erp-core-go backend.
// A non-zero variance needs an inline Branch Manager/Merchant Admin PIN
// check, same shape as cash-reconciliation.spec.ts — this runs as Ravi
// (POS User) throughout, since submitting a count (even one with a
// variance, once a valid approver PIN is supplied) is open to any
// authenticated user; only the approval itself is role-gated, checked
// inline rather than by route.
//
// Each step's real effect is verified by re-reading the variant's live
// system quantity through the UI's own "add a line" flow afterward —
// not by trusting the inline result badge's text alone, which stays on
// screen from a previous successful submission if a later one silently
// no-ops, and so can't tell "it changed" apart from "it didn't."
const SEED_VARIANT_LABEL = "SG Cricket Bat";

async function addSeedVariantLine(page: Page) {
  // Combobox order on this page: Branch, Type, then the variant picker.
  await page.getByRole("combobox").nth(2).click();
  await page.getByRole("option", { name: new RegExp(SEED_VARIANT_LABEL) }).click();
  const addButton = page.getByRole("button", { name: "Add line" });
  await expect(addButton).toBeEnabled();
  await addButton.click();
  const row = page.getByRole("row", { name: new RegExp(SEED_VARIANT_LABEL) });
  await expect(row).toBeVisible();
  await expect(row.locator("td").nth(1)).not.toHaveText("—");
  return row;
}

async function readSystemQty(page: Page): Promise<number> {
  const row = await addSeedVariantLine(page);
  const text = await row.locator("td").nth(1).innerText();
  return Number(text);
}

test("a zero-variance count auto-closes, a variance needs a manager PIN, and history reflects both", async ({ page }) => {
  await test.step("login as POS User (Ravi)", async () => {
    await page.goto("/login");
    await page.fill("#email", "ravi@acme-sports.test");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
  });

  await page.goto("/inventory-reconciliation");

  let systemQty = 0;

  await test.step("add the seed variant, read its current system quantity, and count it exactly — no approval needed", async () => {
    const row = await addSeedVariantLine(page);
    systemQty = Number(await row.locator("td").nth(1).innerText());
    expect(Number.isFinite(systemQty)).toBeTruthy();

    // A freshly-added line's counted-qty input starts empty, which reads
    // as 0 and would itself compute as a (large, spurious) variance — the
    // "no approval needed" check only means anything once the counted
    // value is actually filled in to match the system quantity.
    await row.locator("input[type='number']").fill(String(systemQty));
    await expect(page.getByText("Approver PIN")).toHaveCount(0);
    const submit = page.getByRole("button", { name: "Submit reconciliation" });
    await expect(submit).toBeEnabled();
    await submit.click();
    await expect(page.getByText(/variance value ₹0\.00/)).toBeVisible();
  });

  await test.step("a shortage requires a reason and rejects a wrong PIN, then succeeds with the correct one", async () => {
    const row = await addSeedVariantLine(page);
    await row.locator("input[type='number']").fill(String(systemQty - 3));

    await expect(page.locator("#reason")).toBeVisible();
    await page.fill("#reason", "E2E cycle count shortage");
    await page.fill("#authorizedBy", "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"); // Arjun, Merchant Admin
    await page.fill("#authorizedPin", "0000"); // wrong PIN (real one is 1234, set for backend verification)
    await page.getByRole("button", { name: "Submit reconciliation" }).click();
    await expect(page.getByText(/VARIANCE_NOT_AUTHORIZED|Branch Manager or Merchant Admin/i)).toBeVisible();

    await page.fill("#authorizedPin", "1234");
    await page.getByRole("button", { name: "Submit reconciliation" }).click();
    // The badge only proves *a* submission completed — the real proof
    // this specific one changed the right thing by the right amount is
    // the live system-quantity re-read in the next step, not this text.
    await expect(page.getByText(/variance value ₹-?\d/)).toBeVisible();
  });

  await test.step("the shortage actually reduced the live system quantity by 3", async () => {
    const nowQty = await readSystemQty(page);
    expect(nowQty).toBeCloseTo(systemQty - 3, 3);
  });

  await test.step("restore the seed variant's stock back to its original quantity", async () => {
    const row = page.getByRole("row", { name: new RegExp(SEED_VARIANT_LABEL) });
    await row.locator("input[type='number']").fill(String(systemQty));
    await page.fill("#reason", "E2E restore after test");
    await page.fill("#authorizedBy", "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee");
    await page.fill("#authorizedPin", "1234");
    await page.getByRole("button", { name: "Submit reconciliation" }).click();
    await expect(page.getByText(/variance value ₹-?\d/)).toBeVisible();
  });

  await test.step("the restore actually brought the live system quantity back to its original value", async () => {
    const finalQty = await readSystemQty(page);
    expect(finalQty).toBeCloseTo(systemQty, 3);
  });

  await test.step("history reflects at least one zero-variance and one non-zero, approved reconciliation today", async () => {
    await page.goto("/inventory-reconciliation");
    await expect(page.getByRole("cell", { name: "₹0.00" }).first()).toBeVisible();
    await expect(page.getByRole("row", { name: /Yes/ }).first()).toBeVisible();
  });
});
