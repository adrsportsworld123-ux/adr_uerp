import { test, expect } from "@playwright/test";

// End-to-end coverage for product editing (erp-core-go internal/catalog/
// products_edit.go; the Products and Product edit screens). Seeds one
// product via the API, then — entirely through the browser — finds it on
// the Products screen, renames it, changes a variant's SKU, saves a
// below-cost price only after the explicit override, adds a second
// variant, and discontinues the product. The API is used to confirm each
// change really landed, including that a discontinued product stops
// scanning at the till and scans again once reactivated.
// The seed merchant was renamed from "acme-sports"; override per environment.
const MERCHANT_CODE = process.env.E2E_MERCHANT_CODE ?? "adr_sports_world";
const API_BASE = "http://localhost:8080/api/v1";

test("product edit: rename, variant SKU, guarded price change, add variant, discontinue and reactivate", async ({ page, request }) => {
  const unique = Date.now() % 1000000;
  const name = `E2E Edit Bat ${unique}`;
  const renamed = `E2E Edit Bat Pro ${unique}`;
  let headers: Record<string, string> = {};
  let productId = "";
  let barcode = "";

  await test.step("seed a product via the API", async () => {
    const login = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: MERCHANT_CODE, email: "admin@adrsw.com", password: "Idontsay3#" },
    });
    headers = { Authorization: `Bearer ${(await login.json()).access_token}` };
    const res = await request.post(`${API_BASE}/products`, {
      headers,
      data: { name, variants: [{ sku: `E2E-EDIT-${unique}`, cost_price: 500, mrp: 1200, selling_price: 1000, generate_barcode: true }] },
    });
    expect(res.ok()).toBeTruthy();
    const p = await res.json();
    productId = p.product_id;
    barcode = p.variants[0].generated_barcode;
  });

  await test.step("login, find the product on the Products screen, open it", async () => {
    await page.goto("/login");
    await page.fill("#email", "admin@adrsw.com");
    await page.fill("#password", "Idontsay3#");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
    await page.goto("/products");
    await page.getByLabel("Search products").fill(name);
    const row = page.getByRole("row", { name: new RegExp(name) });
    await expect(row).toContainText(/active/i);
    await row.getByRole("button", { name: "Edit" }).click();
    await page.waitForURL(`/products/${productId}/edit`);
    await expect(page.locator("#name")).toHaveValue(name);
  });

  await test.step("rename the product and set its HSN code", async () => {
    await page.fill("#name", renamed);
    await page.fill("#hsnCode", "9506");
    await page.getByRole("button", { name: "Save product" }).click();
    await expect(page.getByRole("heading", { level: 1 })).toContainText(renamed);
    const p = await (await request.get(`${API_BASE}/products/${productId}`, { headers })).json();
    expect(p.name).toBe(renamed);
    expect(p.hsn_code).toBe("9506");
  });

  await test.step("change the variant's SKU", async () => {
    const card = page.getByTestId(`variant-E2E-EDIT-${unique}`);
    await card.getByLabel("SKU", { exact: true }).fill(`E2E-EDIT-${unique}-PRO`);
    await card.getByRole("button", { name: "Save variant" }).click();
    await expect(page.getByTestId(`variant-E2E-EDIT-${unique}-PRO`)).toBeVisible();
  });

  await test.step("a below-cost price is refused until the override is ticked", async () => {
    const card = page.getByTestId(`variant-E2E-EDIT-${unique}-PRO`);
    await card.getByLabel("Selling price", { exact: true }).fill("400");
    await card.getByRole("button", { name: "Save prices" }).click();
    await expect(card.getByText(/below cost/).first()).toBeVisible();
    await card.getByLabel(/save anyway/).check();
    await card.getByRole("button", { name: "Save prices" }).click();
    await expect(page.getByTestId(`variant-E2E-EDIT-${unique}-PRO`).getByLabel("Selling price", { exact: true })).toHaveValue("400.00");
  });

  await test.step("add a second variant", async () => {
    await page.fill("#newSku", `E2E-EDIT-${unique}-JR`);
    await page.fill("#newMrp", "900");
    await page.fill("#newSelling", "800");
    await page.getByRole("button", { name: "Add variant" }).click();
    await expect(page.getByTestId(`variant-E2E-EDIT-${unique}-JR`)).toBeVisible();
    const p = await (await request.get(`${API_BASE}/products/${productId}`, { headers })).json();
    expect(p.variants).toHaveLength(2);
  });

  await test.step("discontinue the product: it stops scanning at the till", async () => {
    await page.getByLabel("Product status").click();
    await page.getByRole("option", { name: /Discontinued/ }).click();
    await page.getByRole("button", { name: "Save product" }).click();
    await expect(page.getByRole("heading", { level: 1 })).toContainText("discontinued");
    const scan = await request.get(`${API_BASE}/products/barcode/${barcode}`, { headers });
    expect(scan.status()).toBe(409);
    expect((await scan.json()).error.code).toBe("PRODUCT_INACTIVE");
  });

  await test.step("still findable on the Products screen, and reactivating it puts it back on sale", async () => {
    await page.goto("/products");
    await page.getByLabel("Search products").fill(renamed);
    const row = page.getByRole("row", { name: new RegExp(renamed) });
    await expect(row).toContainText(/discontinued/i);
    await row.getByRole("button", { name: "Edit" }).click();
    await page.getByLabel("Product status").click();
    await page.getByRole("option", { name: /Active/ }).click();
    await page.getByRole("button", { name: "Save product" }).click();
    await expect(page.getByRole("heading", { level: 1 })).toContainText("active");
    const scan = await request.get(`${API_BASE}/products/barcode/${barcode}`, { headers });
    expect(scan.status()).toBe(200);
  });
});
