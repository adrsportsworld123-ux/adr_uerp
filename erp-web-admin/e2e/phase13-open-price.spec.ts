import { test, expect, Page, APIRequestContext } from "@playwright/test";

// Phase 13: open-price items, set on the product edit page. The till side
// (typing the price, approval above the threshold) is covered by
// erp-core-go's live checks and the Flutter tests; this spec checks the
// admin form saves what the POS will read, validates the range before
// saving, and is read-only without pricing.manage.

const API = "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";
const GST18 = "66666666-6666-6666-6666-666666666666";

async function login(page: Page, email: string) {
  await page.goto("/login");
  await page.fill("#email", email);
  await page.fill("#password", "Passw0rd!");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

async function token(request: APIRequestContext) {
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email: "arjun@acme-sports.test", password: "Passw0rd!" } });
  return (await r.json()).access_token as string;
}

test("Phase 13: a Merchant Admin makes a variant open-price; a POS user can't", async ({ page, request }) => {
  const headers = { Authorization: `Bearer ${await token(request)}` };
  // Its own product, so the spec never depends on data other suites change.
  const sku = `E2E-OP-${Date.now()}`;
  const created = await (
    await request.post(`${API}/products`, {
      headers,
      data: { name: `Open Price ${sku}`, tax_slab_id: GST18, variants: [{ sku, cost_price: 50, mrp: 500, selling_price: 120, generate_barcode: true }] },
    })
  ).json();
  const BARCODE = created.variants[0].generated_barcode as string;
  const item = { product_id: created.product_id as string, variant_id: created.variants[0].variant_id as string, sku };

  try {
    await login(page, "arjun@acme-sports.test");
    await page.goto(`/products/${item.product_id}/edit`);
    const card = page.getByTestId(`variant-${item.sku}`);
    await card.getByRole("checkbox", { name: "Open price at the till" }).check();
    const id = item.variant_id;
    await card.locator(`#op-min-${id}`).fill("600");
    await card.locator(`#op-max-${id}`).fill("500");
    await expect(card.getByText("The minimum must be 0 or more and not above the maximum")).toBeVisible();
    await expect(card.getByRole("button", { name: "Save open price" })).toBeDisabled();

    await card.locator(`#op-min-${id}`).fill("10");
    await card.locator(`#op-approval-${id}`).fill("200");
    await card.getByRole("button", { name: "Save open price" }).click();
    await expect(page.getByText("Open price saved")).toBeVisible();

    const after = await (await request.get(`${API}/products/barcode/${BARCODE}`, { headers })).json();
    expect(after.open_price).toEqual({ min: "10.00", max: "500.00", approval_above: "200.00" });

    await page.evaluate(() => localStorage.clear());
    await login(page, "ravi@acme-sports.test");
    await page.goto(`/products/${item.product_id}/edit`);
    const roCard = page.getByTestId(`variant-${item.sku}`);
    await expect(roCard.getByRole("checkbox", { name: "Open price at the till" })).toBeChecked();
    await expect(roCard.getByRole("checkbox", { name: "Open price at the till" })).toBeDisabled();
    await expect(roCard.getByText(/Needs the pricing.manage permission/)).toBeVisible();
  } finally {
    await request.patch(`${API}/products/variants/${item.variant_id}`, { headers, data: { open_price: { enabled: false }, status: "discontinued" } });
  }
});
