import { test, expect } from "@playwright/test";

// End-to-end coverage for barcode manual-entry/generate, both at product
// creation time and as a retrofit from the product list (erp-core-go's
// phased_roadmap.md; internal/catalog/barcode_assign.go's
// assignBarcodeInTx, migrations/032_barcode_source.sql's source column).
// The core requirement under test: a product's real, original barcode and
// this system's own generated one are kept as two separate slots — never
// one overwriting the other.
const API_BASE = "http://localhost:8080/api/v1";

// Mirrors erp-core-go's computeEAN13CheckDigit (internal/catalog/ean13.go)
// exactly, so this test can hand-build a fresh, always-valid, always-unique
// EAN-13 per run rather than reusing one fixed code that would collide
// with a previous run's leftover product.
function ean13(payload12: string): string {
  let sum = 0;
  for (let i = 0; i < 12; i++) {
    const d = Number(payload12[i]);
    sum += i % 2 === 0 ? d : d * 3;
  }
  const check = (10 - (sum % 10)) % 10;
  return payload12 + check;
}

test("Barcode: New Product's original-barcode entry + generate checkbox keep both, distinctly, from creation", async ({ page, request }) => {
  const unique = Date.now() % 1000000;
  const productName = `E2E Barcode New ${unique}`;
  const sku = `E2E-BC-NEW-${unique}`;
  // A real, valid EAN-13, unique per test run — see ean13() above.
  const originalCode = ean13("89" + String(unique).padStart(10, "0"));

  await page.goto("/login");
  await page.fill("#email", "admin@adrsw.com");
  await page.fill("#password", "Idontsay3#");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");

  await test.step("create a product with both an original barcode and 'also generate' checked", async () => {
    await page.goto("/products/new");
    await page.fill("#name", productName);
    await page.fill("#sku", sku);
    await page.fill("#mrp", "300");
    await page.fill("#sellingPrice", "250");
    await page.fill("#originalBarcode", originalCode);
    await page.getByText("Also generate a system barcode").click();
    await page.getByRole("button", { name: "Create product" }).click();
    await expect(page).toHaveURL(/\/pricing/, { timeout: 10_000 });
    await expect(page.getByText(new RegExp(`barcode: original ${originalCode}, system \\d{13}`))).toBeVisible();
  });

  await test.step("the product list shows both, distinctly labeled, and never merges or overwrites either", async () => {
    const row = page.locator("tr", { hasText: productName });
    await expect(row.getByText("Original")).toBeVisible();
    await expect(row.getByText(originalCode)).toBeVisible();
    await expect(row.getByText("System")).toBeVisible();
    // The generated code is real (not the original repeated) — assert a
    // second, different 13-digit mono-font value is present in the row.
    const monoValues = await row.locator(".font-mono").allTextContents();
    expect(monoValues).toContain(originalCode);
    expect(monoValues.some((v) => v !== originalCode && /^\d{13}$/.test(v))).toBe(true);
  });

  await test.step("both codes independently resolve via GET /products/barcode/{code}, to the same variant", async () => {
    const login = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "admin@adrsw.com", password: "Idontsay3#" },
    });
    const token = (await login.json()).access_token;
    const headers = { Authorization: `Bearer ${token}` };

    const listed = await (await request.get(`${API_BASE}/products?q=${encodeURIComponent(productName)}`, { headers })).json();
    const variant = listed.products[0].variants[0];
    expect(variant.original_barcode).toBe(originalCode);
    expect(variant.generated_barcode).toMatch(/^\d{13}$/);

    const originalLookup = await request.get(`${API_BASE}/products/barcode/${variant.original_barcode}`, { headers });
    expect(originalLookup.ok()).toBeTruthy();
    expect((await originalLookup.json()).variant_id).toBe(variant.variant_id);

    const generatedLookup = await request.get(`${API_BASE}/products/barcode/${variant.generated_barcode}`, { headers });
    expect(generatedLookup.ok()).toBeTruthy();
    expect((await generatedLookup.json()).variant_id).toBe(variant.variant_id);
  });
});

test("Barcode: manual-enter and generate both work as a retrofit from the product list, on separate products", async ({ page, request }) => {
  const unique = Date.now() % 1000000 + 1; // offset from the other test's unique, run in the same second
  const manualProductName = `E2E Barcode Manual ${unique}`;
  const generatedProductName = `E2E Barcode Generated ${unique}`;
  // A valid EAN-13 (see ean13() above) — the backend now auto-detects and
  // checksum-validates any 13-digit manually-entered code.
  const manualCode = ean13("89" + String(unique).padStart(10, "0"));

  const login = await request.post(`${API_BASE}/auth/login`, {
    data: { merchant_code: "acme-sports", email: "admin@adrsw.com", password: "Idontsay3#" },
  });
  const token = (await login.json()).access_token;
  const headers = { Authorization: `Bearer ${token}` };

  async function createProduct(name: string, sku: string) {
    const res = await request.post(`${API_BASE}/products`, {
      headers,
      data: { name, variants: [{ sku, cost_price: 100, mrp: 200, selling_price: 150 }] },
    });
    return (await res.json()).variants[0].variant_id as string;
  }

  const manualVariantId = await createProduct(manualProductName, `E2E-BC-M-${unique}`);
  const generatedVariantId = await createProduct(generatedProductName, `E2E-BC-G-${unique}`);

  await page.goto("/login");
  await page.fill("#email", "admin@adrsw.com");
  await page.fill("#password", "Idontsay3#");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
  await page.goto("/pricing");

  await test.step("manual entry (the 'original' slot): type a code, click Assign, it displays", async () => {
    const row = page.locator("tr", { hasText: manualProductName });
    await row.getByPlaceholder("Enter code").fill(manualCode);
    await row.getByRole("button", { name: "Assign" }).click();
    await expect(page.getByText("Barcode assigned")).toBeVisible();
    await expect(page.locator("tr", { hasText: manualProductName }).getByText(manualCode)).toBeVisible();
  });

  await test.step("generate (the 'system' slot): click Generate with no code typed, a real EAN-13 displays", async () => {
    const row = page.locator("tr", { hasText: generatedProductName });
    await row.getByRole("button", { name: "Generate" }).click();
    await expect(page.getByText("Barcode assigned").last()).toBeVisible();
    await expect(page.locator("tr", { hasText: generatedProductName }).locator(".font-mono").last()).toBeVisible();
  });

  await test.step("both codes are real, resolvable barcodes via GET /products/barcode/{code}", async () => {
    const manualLookup = await request.get(`${API_BASE}/products/barcode/${manualCode}`, { headers });
    expect(manualLookup.ok()).toBeTruthy();
    expect((await manualLookup.json()).variant_id).toBe(manualVariantId);

    const listed = await (await request.get(`${API_BASE}/products?q=${encodeURIComponent(generatedProductName)}`, { headers })).json();
    const generatedCode = listed.products[0].variants[0].generated_barcode;
    expect(generatedCode).toBeTruthy();
    const generatedLookup = await request.get(`${API_BASE}/products/barcode/${generatedCode}`, { headers });
    expect(generatedLookup.ok()).toBeTruthy();
    expect((await generatedLookup.json()).variant_id).toBe(generatedVariantId);
  });
});
