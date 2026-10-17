import { test, expect, Page } from "@playwright/test";

// The uniform master lifecycle (erp-core-go migrations/054) — create, edit,
// deactivate, delete into the Recycle Bin, restore — and several check-in
// sessions a day (migrations/053), through the real browser against the
// real API. Signs in as a Merchant Admin: Arjun on the seed data, or
// E2E_EMAIL / E2E_PASSWORD.

const unique = Date.now();
const EMAIL = process.env.E2E_EMAIL ?? "arjun@acme-sports.test";
const PASSWORD = process.env.E2E_PASSWORD ?? "Passw0rd!";

async function login(page: Page) {
  await page.goto("/login");
  await page.fill("#email", EMAIL);
  await page.fill("#password", PASSWORD);
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

test("chart of accounts: add, edit, deactivate, delete, restore", async ({ page }) => {
  const code = `9${String(unique).slice(-3)}`;
  const name = `E2E Account ${unique}`;
  await login(page);
  await page.goto("/accounting/chart-of-accounts");
  await page.fill("#code", code);
  await page.fill("#name", name);
  await page.getByRole("button", { name: "Add account" }).click();
  await expect(page.getByRole("cell", { name, exact: true })).toBeVisible();

  await page.getByRole("button", { name: `Edit ${code} ${name}` }).click();
  await page.getByRole("dialog").getByLabel("Name").fill(`${name} R`);
  await page.getByRole("dialog").getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("cell", { name: `${name} R`, exact: true })).toBeVisible();

  await page.getByRole("button", { name: `Deactivate ${code} ${name} R` }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "Deactivate" }).click();
  await expect(page.getByText(`Deactivated “${code} ${name} R”`)).toBeVisible();

  await page.getByRole("button", { name: `Delete ${code} ${name} R` }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "Move to Recycle Bin" }).click();
  await expect(page.getByRole("cell", { name: `${name} R`, exact: true })).toHaveCount(0);

  await page.goto("/recycle-bin");
  await page.getByRole("button", { name: `Restore ${code} ${name} R` }).click();
  await expect(page.getByText(/Restored account/)).toBeVisible();
});

test("a system account can't be deactivated or deleted", async ({ page }) => {
  await login(page);
  await page.goto("/accounting/chart-of-accounts");
  const row = page.getByRole("row", { name: /1001/ });
  await expect(row.getByRole("button", { name: /^Delete 1001/ })).toBeDisabled();
  await expect(row.getByRole("button", { name: /^Deactivate 1001/ })).toBeDisabled();
});

test("shift: add, edit, delete", async ({ page }) => {
  const name = `E2E Lifecycle Shift ${unique}`;
  await login(page);
  await page.goto("/hr/shifts");
  await page.fill("#name", name);
  await page.getByRole("button", { name: "Add shift" }).click();
  await expect(page.getByRole("cell", { name, exact: true })).toBeVisible();
  await page.getByRole("button", { name: `Edit ${name}` }).click();
  await page.getByRole("dialog").getByLabel("End (HH:MM)").fill("17:30");
  await page.getByRole("dialog").getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("row", { name: new RegExp(name) })).toContainText("17:30");
  await page.getByRole("button", { name: `Delete ${name}` }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "Move to Recycle Bin" }).click();
  await expect(page.getByRole("cell", { name, exact: true })).toHaveCount(0);
});

test("customer: an unused one deletes; a branch that's in use offers deactivate instead", async ({ page }) => {
  const name = `E2E Lifecycle Cust ${unique}`;
  await login(page);
  await page.goto("/customers/new");
  await page.fill("#name", name);
  await page.fill("#phone", `6${String(unique).slice(-9)}`);
  await page.click('button[type="submit"]');
  await page.waitForURL(/\/customers\/[0-9a-f-]+/);
  await page.goto(`/customers?q=${encodeURIComponent(name)}`);
  await page.getByRole("button", { name: `Delete ${name}` }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "Move to Recycle Bin" }).click();
  await expect(page.getByText(`Deleted customer “${name}”`)).toBeVisible();

  // The branch the seed's sales were made at (BRANCH_IN_USE overrides it).
  await page.goto("/branches");
  await page.getByRole("button", { name: `Delete ${process.env.BRANCH_IN_USE ?? "MG Road"}`, exact: true }).click();
  const dialog = page.getByRole("alertdialog");
  await expect(dialog).toContainText("can't be deleted");
  await expect(dialog.getByRole("button", { name: "Move to Recycle Bin" })).toHaveCount(0);
  await dialog.getByRole("button", { name: "Cancel" }).click();
});

test("attendance: check in, out, and in again the same day", async ({ page, context }) => {
  await context.grantPermissions(["geolocation"]);
  await context.setGeolocation({ latitude: 12.834, longitude: 80.17, accuracy: 20 });
  await login(page);
  const header = page.locator("header");
  for (const step of ["first", "second"]) {
    const button = header.getByRole("button", { name: /Check in|In since/ });
    if ((await button.textContent())?.includes("In since")) {
      await button.click();
      await page.getByRole("dialog").getByRole("button", { name: "Check out now" }).click();
      await expect(page.getByText(/Checked out at/)).toBeVisible();
    }
    await header.getByRole("button", { name: /Check in/ }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Check in now" }).click();
    await expect(header.getByRole("button", { name: /In since/ })).toBeVisible();
    if (step === "first") {
      await header.getByRole("button", { name: /In since/ }).click();
      await page.getByRole("dialog").getByRole("button", { name: "Check out now" }).click();
      await expect(header.getByRole("button", { name: /Check in again/ })).toBeVisible();
    }
  }
  await page.goto("/hr/attendance");
  await expect(page.getByText("#2").first()).toBeVisible();
});
