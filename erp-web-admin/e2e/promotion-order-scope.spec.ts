import { test, expect, APIRequestContext } from "@playwright/test";

// A whole-order promotion limited by categories, brands and products
// (erp-core-go migrations/069): each list optional and multi-select; an
// item must match every list that is set; none = the whole order. The
// cart behaviour is covered by the API flow. Here: the dialog's chips save,
// reopen, show in the list, and come off again.

const API = process.env.E2E_API_URL ?? "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";

async function token(request: APIRequestContext) {
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email: "arjun@acme-sports.test", password: "Passw0rd!" } });
  return (await r.json()).access_token as string;
}

test("whole-order promotion with category, brand and product filters", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const tag = Date.now().toString().slice(-7);
  const catA = `E2E Scope Cat A ${tag}`;
  const catB = `E2E Scope Cat B ${tag}`;
  const brand = `E2E Scope Brand ${tag}`;
  const promoName = `E2E Scoped ${tag}`;
  for (const name of [catA, catB]) expect((await request.post(`${API}/categories`, { headers: auth, data: { name } })).status()).toBe(201);
  expect((await request.post(`${API}/brands`, { headers: auth, data: { name: brand } })).status()).toBe(201);
  const product = `E2E SCOPE ITEM ${tag}`;
  const imp = await request.post(`${API}/purchase/imports/commit`, {
    headers: auth,
    data: {
      supplier_id: "11111111-2222-3333-4444-555555555555",
      branch_id: "22222222-2222-2222-2222-222222222222",
      invoice_number: `E2E-SCOPE-${tag}`,
      invoice_date: "2026-10-01",
      csv: `item_name,quantity,unit_cost,gst_rate,mrp,sale_gst_rate\n${product},1,10,18,20,18\n`,
    },
  });
  expect(imp.status()).toBe(201);

  let promoId = "";
  try {
    await page.goto("/login");
    await page.fill("#email", "meera@acme-sports.test");
    await page.fill("#password", "9999");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    await page.goto("/promotions");
    await page.getByRole("button", { name: "New promotion" }).click();
    await page.fill("#promoName", promoName);
    await page.fill("#valuePercent", "10");
    const scope = page.getByTestId("promo-order-scope");
    await expect(scope).toContainText("All categories");
    await expect(scope).toContainText("All brands");
    await expect(scope).toContainText("All products");

    for (const name of [catA, catB]) {
      await page.locator("#promoScopeCategories").click();
      await page.keyboard.type(name);
      await page.getByRole("option", { name }).click();
    }
    await page.locator("#promoScopeBrands").click();
    await page.keyboard.type(brand);
    await page.getByRole("option", { name: brand }).click();
    await page.locator("#promoScopeProducts").click();
    await page.getByLabel("Search products").fill(product);
    await page.getByRole("option", { name: new RegExp(product) }).click();

    await expect(scope.getByRole("list", { name: "Chosen categories" }).getByRole("listitem")).toHaveCount(2);
    await expect(scope.getByRole("list", { name: "Chosen brands" })).toContainText(brand);
    await expect(scope.getByRole("list", { name: "Chosen products" })).toContainText(product);

    const created = page.waitForResponse((r) => r.url().endsWith("/api/v1/promotions") && r.request().method() === "POST");
    await page.click('button[type="submit"]:has-text("Create promotion")');
    promoId = (await (await created).json()).promotion_id;
    await expect(page.getByText("Promotion created")).toBeVisible();
    const row = page.getByRole("row", { name: new RegExp(promoName) });
    await expect(row).toContainText(`categories: ${catA}, ${catB}`);
    await expect(row).toContainText(`brands: ${brand}`);
    await expect(row).toContainText(`products: ${product}`);

    // Edit: the chips come back; remove the brand and a category.
    await row.getByRole("button", { name: "Edit" }).click();
    await expect(scope.getByRole("list", { name: "Chosen categories" }).getByRole("listitem")).toHaveCount(2);
    await scope.getByRole("button", { name: `Remove ${brand}` }).click();
    await scope.getByRole("button", { name: `Remove ${catB}` }).click();
    await expect(scope).toContainText("All brands");
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByText("Promotion updated")).toBeVisible();
    await expect(row).toContainText(`categories: ${catA}`);
    await expect(row).not.toContainText("brands:");
    await expect(row).not.toContainText(catB);

    // Switching to one product hides the filters (they're whole-order only).
    await row.getByRole("button", { name: "Edit" }).click();
    await page.getByRole("combobox", { name: "Applies to" }).click();
    await page.getByRole("option", { name: "One product" }).click();
    await expect(page.getByTestId("promo-order-scope")).toHaveCount(0);
    await page.keyboard.press("Escape");
  } finally {
    // Never leave a running 10% promotion behind for other specs' carts.
    if (promoId) await request.patch(`${API}/promotions/${promoId}`, { headers: auth, data: { active: false } });
  }
});
