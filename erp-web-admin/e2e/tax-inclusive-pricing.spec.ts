import { test, expect, Page, APIRequestContext } from "@playwright/test";

// GST-inclusive vs GST-exclusive pricing (erp-core-go migrations/033):
// merchant default -> New Product pre-fill -> per-product badge, net price
// and switch on the Pricing screen. The billing math itself (3 x ₹1000
// inclusive bills exactly ₹3000.00, etc.) is covered by erp-core-go's
// internal/taxcalc unit tests and live API verification.

const API = "http://localhost:8080/api/v1";

async function apiToken(request: APIRequestContext) {
  const r = await request.post(`${API}/auth/login`, {
    data: { merchant_code: "acme-sports", email: "admin@adrsw.com", password: "Idontsay3#" },
  });
  return (await r.json()).access_token as string;
}

async function login(page: Page) {
  await page.goto("/login");
  await page.fill("#email", "admin@adrsw.com");
  await page.fill("#password", "Idontsay3#");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

test("merchant default pre-fills New Product, and Pricing shows/switches the GST basis", async ({ page, request }) => {
  const unique = Date.now() % 1000000;
  const name = `ZZ GST Incl ${unique}`;
  const token = await apiToken(request);
  const headers = { Authorization: `Bearer ${token}` };
  const slabs = (await (await request.get(`${API}/tax-slabs`, { headers })).json()).tax_slabs as { tax_slab_id: string; name: string }[];
  const gst18 = slabs.find((s) => s.name.includes("18"));
  expect(gst18).toBeTruthy();

  await login(page);
  await page.goto("/pricing");

  try {
    await test.step("turn on the merchant-wide inclusive default", async () => {
      const box = page.locator("#pricesIncludeTaxDefault");
      await expect(box).toBeEnabled();
      if (!(await box.isChecked())) await box.click();
      await expect(page.getByText("New products will default to GST-inclusive prices")).toBeVisible();
    });

    await test.step("New Product's checkbox is pre-filled from the default", async () => {
      await page.goto("/products/new");
      await expect(page.locator("#priceIncludesTax")).toBeChecked();
      await expect(page.getByText("Selling price (₹, incl. GST)")).toBeVisible();
    });
  } finally {
    // Never leave the shared dev merchant's default flipped for other specs.
    await request.put(`${API}/pricing/settings`, { headers, data: { prices_include_tax_default: false } });
  }

  await test.step("an inclusive product shows its badge and net price on Pricing", async () => {
    const created = await request.post(`${API}/products`, {
      headers,
      data: {
        name,
        tax_slab_id: gst18!.tax_slab_id,
        price_includes_tax: true,
        variants: [{ sku: `ZZGST-${unique}`, mrp: 1180, selling_price: 1180, cost_price: 500 }],
      },
    });
    expect(created.status()).toBe(201);

    await page.goto("/pricing");
    const row = page.getByRole("row").filter({ hasText: name });
    await expect(row).toContainText("Incl. GST 18%");
    await expect(row).toContainText("net ₹1000.00");
    // Margin on the pre-GST price: (1000 - 500) / 1000 = 50%, not (1180-500)/1180.
    await expect(row).toContainText("50%");
  });

  await test.step("switching the product to exclusive re-reads the same price with GST on top", async () => {
    const row = page.getByRole("row").filter({ hasText: name });
    page.once("dialog", (d) => d.accept());
    await row.getByRole("button", { name: "Switch to excl." }).click();
    await expect(row).toContainText("Excl. GST 18%");
    await expect(row).toContainText("customer pays ₹1392.40");
  });

  const products = (await (await request.get(`${API}/products?q=${encodeURIComponent(name)}`, { headers })).json()).products;
  await request.patch(`${API}/products/${products[0].product_id}`, { headers, data: { status: "inactive" } });
});
