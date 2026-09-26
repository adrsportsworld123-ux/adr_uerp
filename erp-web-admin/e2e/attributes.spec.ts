import { test, expect } from "@playwright/test";

// End-to-end coverage for Phase 8: Vertical Expansion (erp-core-go's
// phased_roadmap.md; internal/catalog/attributes.go). attributes/
// attribute_values existed in the schema since Phase 1 specifically for
// this ("validates the configurable masters, not industry-specific code
// promise") but had no management API or UI until now, and
// product_variants.attribute_combo accepted any key/value with zero
// validation. This drives the real Product Attributes screen to define a
// select-type attribute with controlled values, assigns it to a category
// as required, then confirms New Product both renders the right field
// dynamically and enforces the requirement server-side.
const API_BASE = "http://localhost:8080/api/v1";

test("vertical expansion: define an attribute, assign it to a category, and New Product enforces/renders it", async ({ page, request }) => {
  const unique = Date.now() % 1000000;
  const attrName = `E2E Purity ${unique}`;
  const categoryName = `E2E Jewelry ${unique}`;

  await test.step("login and create a category to attach the attribute set to", async () => {
    await page.goto("/login");
    await page.fill("#email", "admin@adrsw.com");
    await page.fill("#password", "Idontsay3#");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    const login = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "admin@adrsw.com", password: "Idontsay3#" },
    });
    const adminToken = (await login.json()).access_token;
    await request.post(`${API_BASE}/categories`, { headers: { Authorization: `Bearer ${adminToken}` }, data: { name: categoryName } });
  });

  await test.step("define a select attribute with two controlled values through the UI", async () => {
    await page.goto("/attributes");
    await page.getByLabel("New attribute name").fill(attrName);
    await page.getByRole("button", { name: "Create" }).click();
    await expect(page.getByText(attrName)).toBeVisible();

    const attrCard = page.locator("div.border.rounded", { hasText: attrName });
    await attrCard.getByPlaceholder("New value").fill("18K");
    await attrCard.getByRole("button", { name: "+ Add value" }).click();
    // A Badge's own text run includes its "Remove" button glyph as part of
    // the same accessible text node (e.g. "18K×"), so an exact match on
    // just "18K" never matches — the button's own accessible name is the
    // real, precise signal that the value was actually added.
    await expect(attrCard.getByRole("button", { name: "Remove 18K" })).toBeVisible();
    await attrCard.getByPlaceholder("New value").fill("22K");
    await attrCard.getByRole("button", { name: "+ Add value" }).click();
    await expect(attrCard.getByRole("button", { name: "Remove 22K" })).toBeVisible();
  });

  await test.step("assign the attribute to the category as required", async () => {
    await page.locator("button", { hasText: "Select a category" }).click();
    await page.getByRole("option", { name: categoryName }).click();
    await expect(page.getByText("No attributes assigned to this category yet")).toBeVisible();

    await page.locator("button", { hasText: "Select an attribute" }).click();
    await page.getByRole("option", { name: attrName }).click();
    await page.getByLabel("Required").check();
    await page.getByRole("button", { name: "Add to category" }).click();
    await expect(page.getByRole("row", { name: new RegExp(attrName) })).toContainText("Required");
  });

  await test.step("New Product renders the field and refuses to submit without it", async () => {
    await page.goto("/products/new");
    await page.fill("#name", `E2E Ring ${unique}`);
    await page.fill("#sku", `E2E-RING-${unique}`);
    await page.fill("#mrp", "50000");
    await page.fill("#sellingPrice", "48000");
    await page.locator("button", { hasText: "None" }).first().click();
    await page.getByRole("option", { name: categoryName }).click();

    // The attribute now renders as a real select field, not a free-form box.
    await expect(page.getByText(`${attrName} *`)).toBeVisible();

    await page.getByRole("button", { name: "Create product" }).click();
    await expect(page.getByText(/required attribute is missing/i)).toBeVisible();
  });

  await test.step("filling the required field lets the product actually save", async () => {
    await page.locator("button", { hasText: /Required|Optional/ }).last().click();
    await page.getByRole("option", { name: "22K" }).click();
    await page.getByRole("button", { name: "Create product" }).click();
    await expect(page).toHaveURL(/\/pricing/, { timeout: 10_000 });
  });
});
