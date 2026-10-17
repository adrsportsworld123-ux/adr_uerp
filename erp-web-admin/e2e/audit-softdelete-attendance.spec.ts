import { test, expect, Page } from "@playwright/test";

// Optional customer email, soft delete with the Recycle Bin, the activity
// log with its merchant settings, and attendance (erp-core-go migrations
// 050-052), driven through the real browser against the real API.
// Logs in as Arjun (Merchant Admin, Passw0rd!).

const unique = Date.now();

async function login(page: Page) {
  await page.goto("/login");
  await page.fill("#email", "arjun@acme-sports.test");
  await page.fill("#password", "Passw0rd!");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

test("a customer can be registered without an email", async ({ page }) => {
  await login(page);
  await page.goto("/customers/new");
  await expect(page.getByText("(optional)")).toBeVisible();
  await page.fill("#name", `E2E NoMail ${unique}`);
  await page.fill("#phone", `7${unique % 1000000000}`);
  await page.click('button[type="submit"]');
  await page.waitForURL(/\/customers\/[0-9a-f-]+/);
});

test("a deleted brand goes to the Recycle Bin and can be restored", async ({ page }) => {
  const brand = `E2E Bin ${unique}`;
  await login(page);
  await page.goto("/catalog/brands");
  await page.getByRole("button", { name: "New brand" }).first().click();
  await page.fill("#master-name", brand);
  await page.getByRole("button", { name: "Create brand" }).click();
  await page.getByPlaceholder("Search brands").fill(brand);
  await page.getByRole("button", { name: `Delete ${brand}` }).click();
  const dialog = page.getByRole("alertdialog");
  await expect(dialog).toContainText("Recycle Bin");
  await dialog.getByRole("button", { name: "Move to Recycle Bin" }).click();
  await expect(page.getByText(`Deleted brand “${brand}”`)).toBeVisible();

  await page.goto("/recycle-bin");
  const row = page.getByRole("row", { name: new RegExp(brand) });
  await expect(row).toContainText("Arjun");
  await row.getByRole("button", { name: `Restore ${brand}` }).click();
  await expect(page.getByText(`Restored brand “${brand}”`)).toBeVisible();

  await page.goto("/catalog/brands");
  await page.getByPlaceholder("Search brands").fill(brand);
  await expect(page.getByRole("cell", { name: brand, exact: true })).toBeVisible();
});

test("the activity log shows web-admin actions and its settings", async ({ page }) => {
  await login(page);
  await page.goto("/audit-logs");
  await expect(page.getByRole("tab", { name: "Activity", selected: true })).toBeVisible();
  await page.locator("#act-module").selectOption("auth");
  await expect(page.getByRole("row", { name: /POST \/auth\/login/ }).first()).toContainText("Web admin");

  await page.getByRole("tab", { name: "Settings" }).click();
  await expect(page.getByLabel(/Record activity/)).toBeChecked();
  await expect(page.getByText(/can.t be switched off/)).toBeVisible();
});

test("check in from the header, then see it on the register", async ({ page, context }) => {
  await context.grantPermissions(["geolocation"]);
  await context.setGeolocation({ latitude: 12.834, longitude: 80.17, accuracy: 20 });
  await login(page);
  const header = page.locator("header");
  const button = header.getByRole("button", { name: /Check in|Check out|Done for today/ });
  await expect(button).toBeVisible();
  if ((await button.textContent())?.includes("Check in")) {
    await button.click();
    const dialog = page.getByRole("dialog", { name: "Check in" });
    await expect(dialog).toContainText("Location found");
    await dialog.getByRole("button", { name: "Check in now" }).click();
    await expect(page.getByText(/Checked in at/)).toBeVisible();
  }
  await expect(header.getByRole("button", { name: /In since|Done for today/ })).toBeVisible();

  await page.goto("/hr/attendance");
  await expect(page.getByRole("heading", { name: "Attendance" })).toBeVisible();
  await expect(page.getByText("Check-in rules")).toBeVisible();
  await expect(page.getByText("Branch areas (geofences)")).toBeVisible();
});
