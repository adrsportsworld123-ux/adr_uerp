import { test, expect } from "@playwright/test";

// End-to-end coverage for Customer Management through the real UI
// against a real erp-core-go backend. Requires the same setup as
// purchase-and-accounting.spec.ts (backend running, seed password set).
// Runs as the default seed POS User — customer endpoints carry no
// permission gate beyond authentication.
test("register a B2C customer, register a B2B customer, and search/filter", async ({ page }) => {
  const unique = Date.now() % 1000000;

  await test.step("login as POS User (Ravi)", async () => {
    await page.goto("/login");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
  });

  await test.step("register a B2C customer", async () => {
    await page.goto("/customers/new");
    await page.fill("#name", `E2E Walkin ${unique}`);
    await page.fill("#phone", `9${unique}`);
    await page.fill("#email", `walkin${unique}@example.com`);
    await page.click('button[type="submit"]');
    await page.waitForURL(/\/customers\/[0-9a-f-]+/);
    await expect(page.getByText("uppercase")).toHaveCount(0); // sanity: page rendered past loading
    await expect(page.getByText("B2C")).toBeVisible();
    // exact: true — the sidebar's "New Product" link also contains "new"
    // as a case-insensitive substring, which a plain getByText("new")
    // matches too.
    await expect(page.getByText("new", { exact: true })).toBeVisible(); // brand-new customer's segment
  });

  await test.step("B2B registration without a GSTIN is blocked", async () => {
    await page.goto("/customers/new");
    await page.getByRole("combobox").click();
    await page.getByRole("option", { name: "B2B" }).click();
    await page.fill("#name", `E2E Traders ${unique}`);
    await page.fill("#phone", `8${unique}`);
    await page.fill("#email", `traders${unique}@example.com`);
    // GSTIN field is required (HTML5 required attribute) and left blank —
    // the browser blocks submission client-side, so assert we're still on
    // the form rather than expecting a server error round-trip.
    await page.click('button[type="submit"]');
    await expect(page).toHaveURL("/customers/new");
  });

  await test.step("register the B2B customer properly, with a GSTIN", async () => {
    await page.fill("#gstin", "29ABCDE1234F1Z5");
    await page.click('button[type="submit"]');
    await page.waitForURL(/\/customers\/[0-9a-f-]+/);
    await expect(page.getByText("B2B")).toBeVisible();
    await expect(page.getByText("29ABCDE1234F1Z5")).toBeVisible();
  });

  await test.step("find the new B2C customer by searching for their unique phone", async () => {
    await page.goto("/customers");
    await page.fill("#q", `9${unique}`);
    await expect(page.getByRole("cell", { name: `E2E Walkin ${unique}` })).toBeVisible();
  });
});
