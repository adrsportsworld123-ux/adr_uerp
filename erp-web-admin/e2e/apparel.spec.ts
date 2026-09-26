import { test, expect } from "@playwright/test";

// End-to-end coverage for Phase 8: Vertical Expansion — Apparel
// (erp-core-go's phased_roadmap.md; internal/catalog/collections.go).
// Creates a collection through the real New Product screen's inline
// "Add" flow, tags a product with it, and confirms GET /products?
// collection_id= actually filters by it (not just that the UI shows a
// dropdown value).
const API_BASE = "http://localhost:8080/api/v1";

test("Apparel: create a collection inline on New Product, tag a product, and confirm it filters", async ({ page, request }) => {
  const unique = Date.now() % 1000000;
  const collectionName = `E2E Collection ${unique}`;
  const productName = `E2E Jacket ${unique}`;

  await page.goto("/login");
  await page.fill("#email", "admin@adrsw.com");
  await page.fill("#password", "Idontsay3#");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");

  await test.step("create the collection inline and create a product tagged with it", async () => {
    await page.goto("/products/new");
    await page.fill("#name", productName);
    await page.fill("#sku", `E2E-APP-${unique}`);
    await page.fill("#mrp", "2500");
    await page.fill("#sellingPrice", "1800");
    await page.getByPlaceholder("New collection").fill(collectionName);
    await page.getByRole("button", { name: "Add" }).last().click();
    await expect(page.locator("button", { hasText: collectionName })).toBeVisible();
    await page.getByRole("button", { name: "Create product" }).click();
    await expect(page).toHaveURL(/\/pricing/, { timeout: 10_000 });
  });

  await test.step("the product is filterable by collection_id via the API", async () => {
    const login = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "admin@adrsw.com", password: "Idontsay3#" },
    });
    const token = (await login.json()).access_token;
    const headers = { Authorization: `Bearer ${token}` };

    const collections = await (await request.get(`${API_BASE}/collections`, { headers })).json();
    const collection = collections.collections.find((c: { name: string }) => c.name === collectionName);
    expect(collection).toBeTruthy();

    const filtered = await (await request.get(`${API_BASE}/products?collection_id=${collection.collection_id}`, { headers })).json();
    expect(filtered.products.some((p: { name: string }) => p.name === productName)).toBe(true);
  });
});
