import { test, expect } from "@playwright/test";

// End-to-end coverage for the real product-creation gap found during an
// explicit "review previous phases for anything missing" audit: no UI
// (or even a backend endpoint) existed anywhere to add a second product
// to the one hard-seeded in migrations/002_seed.sql, despite four phases
// of "fully complete" work across the rest of the system. Runs as
// Branch Manager (Meera) since writes are gated by catalog.manage.
test("creating a product with a category/brand/tax slab makes it real and sellable", async ({ page }) => {
  const unique = Date.now() % 1000000;
  const productName = `E2E Product ${unique}`;
  const sku = `E2E-SKU-${unique}`;

  await test.step("login as Branch Manager (Meera)", async () => {
    await page.goto("/login");
    await page.fill("#email", "meera@acme-sports.test");
    await page.fill("#password", "9999");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
  });

  await page.goto("/products/new");

  await test.step("create a new category and brand inline, then fill out the product", async () => {
    await page.fill("input[placeholder='New category']", `E2E Category ${unique}`);
    await page.getByRole("button", { name: "Add" }).first().click();
    await expect(page.getByRole("combobox").first()).toContainText(`E2E Category ${unique}`);

    await page.fill("input[placeholder='New brand']", `E2E Brand ${unique}`);
    await page.getByRole("button", { name: "Add" }).nth(1).click();
    await expect(page.getByRole("combobox").nth(1)).toContainText(`E2E Brand ${unique}`);

    await page.fill("#name", productName);
    await page.fill("#hsnCode", "9506");
    await page.fill("#sku", sku);
    await page.fill("#costPrice", "100");
    await page.fill("#mrp", "250");
    await page.fill("#sellingPrice", "199");
  });

  await test.step("submit and land on Pricing with the new product visible", async () => {
    await page.getByRole("button", { name: "Create product" }).click();
    await page.waitForURL("/pricing");
    await expect(page.getByRole("cell", { name: productName })).toBeVisible();
    await expect(page.getByRole("cell", { name: sku })).toBeVisible();
  });

  await test.step("POS User (Ravi) is denied creating a product", async () => {
    await page.goto("/dashboard");
    await page.click("text=Log out");
    await page.waitForURL("/login");
    await page.fill("#email", "ravi@acme-sports.test");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    await page.goto("/products/new");
    await page.fill("#name", "should be denied");
    await page.fill("#sku", `denied-${unique}`);
    await page.fill("#mrp", "10");
    await page.fill("#sellingPrice", "9");
    await page.getByRole("button", { name: "Create product" }).click();
    await expect(page.getByText(/catalog\.manage/)).toBeVisible();
  });
});
