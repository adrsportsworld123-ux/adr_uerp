import { test, expect, Page, APIRequestContext } from "@playwright/test";

// Promotions apply as items go into the cart (erp-core-go
// promotions.Reapply) and an unfinished sale can be released
// (POST /sales/orders/{id}/release):
// - on New sale, the item shows its offer as soon as it's added, and
//   "Discard sale" releases the cart;
// - a till's cart left open is found under "Open carts & held" and
//   released from its sale page.
// The spec creates its own product and promotion and switches the
// promotion off at the end.

const API = "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";
const BRANCH = "22222222-2222-2222-2222-222222222222";
const TILL = "33333333-3333-3333-3333-333333333333";
const GST18 = "66666666-6666-6666-6666-666666666666";

async function login(page: Page, email: string) {
  await page.goto("/login");
  await page.fill("#email", email);
  await page.fill("#password", "Passw0rd!");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

async function token(request: APIRequestContext, email: string) {
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email, password: "Passw0rd!" } });
  return (await r.json()).access_token as string;
}

test("promotion shows on add; a discarded or abandoned cart is released", async ({ page, request }) => {
  const admin = { Authorization: `Bearer ${await token(request, "arjun@acme-sports.test")}` };
  const sku = `E2E-RC-${Date.now()}`;
  const product = await (
    await request.post(`${API}/products`, {
      headers: admin,
      data: { name: `Release Cart ${sku}`, tax_slab_id: GST18, variants: [{ sku, cost_price: 400, mrp: 1200, selling_price: 1000 }] },
    })
  ).json();
  const variant = product.variants[0].variant_id as string;
  await request.post(`${API}/inventory/adjustments`, { headers: admin, data: { variant_id: variant, branch_id: BRANCH, quantity_delta: 5, reason: "e2e stock" } });
  const promo = await (
    await request.post(`${API}/promotions`, {
      headers: admin,
      data: { name: `E2E 20% ${sku}`, promo_type: "percent", application_level: "product", product_id: product.product_id, config: { value_percent: 20 } },
    })
  ).json();
  const available = async () => Number((await (await request.get(`${API}/inventory?branch_id=${BRANCH}&variant_id=${variant}`, { headers: admin })).json()).available);

  try {
    await login(page, "arjun@acme-sports.test");

    await test.step("New sale: the offer shows as the item goes in; Discard sale releases it", async () => {
      await page.goto("/sales/new");
      await page.getByRole("button", { name: /Search by name, SKU or barcode/ }).click();
      await page.getByLabel("Search products").fill(sku);
      await page.getByRole("option", { name: new RegExp(sku) }).first().click();
      await page.getByRole("button", { name: "Add", exact: true }).click();
      await expect(page.getByTestId("line-discount")).toHaveText("Offer −₹200.00");
      expect(await available()).toBe(4);

      await page.getByRole("button", { name: "Discard sale" }).click();
      await page.getByRole("dialog").getByRole("button", { name: "Discard sale" }).click();
      await expect(page.getByText("Sale discarded — its items are back on sale")).toBeVisible();
      await expect(page.getByText("Add a product to start the sale.")).toBeVisible();
      expect(await available()).toBe(5);
    });

    await test.step("a till cart left open: found under Open carts & held, released from its page", async () => {
      const cashier = { Authorization: `Bearer ${await token(request, "ravi@acme-sports.test")}` };
      const cart = await (await request.post(`${API}/sales/orders`, { headers: cashier, data: { branch_id: BRANCH, pos_terminal_id: TILL, idempotency_key: `e2e-rc-${Date.now()}` } })).json();
      const added = await (await request.post(`${API}/sales/orders/${cart.order_id}/lines`, { headers: cashier, data: { variant_id: variant, quantity: 1 } })).json();
      expect(added.discount_total).toBe("200.00"); // the till's cart got the offer on add too

      // Ravi rang it up a moment ago, so for anyone else it's still in use.
      const tooSoon = await request.post(`${API}/sales/orders/${cart.order_id}/release`, { headers: admin, data: { reason: "e2e" } });
      expect(tooSoon.status()).toBe(409);
      expect((await tooSoon.json()).error.code).toBe("CART_IN_USE");

      await page.goto("/sales?status=open");
      await expect(page.getByText(cart.order_number)).toBeVisible();
      await page.goto(`/sales/${cart.order_id}`);
      await page.getByRole("button", { name: "Release cart" }).click();
      const dialog = page.getByRole("dialog");
      await dialog.getByLabel("Reason").fill("Abandoned at the till");
      await dialog.getByRole("button", { name: "Release cart" }).click();
      await expect(page.getByText(/this cart was worked on in the last 15 minutes/)).toBeVisible();

      // The cashier can release their own cart straight away.
      const own = await request.post(`${API}/sales/orders/${cart.order_id}/release`, { headers: cashier, data: { reason: "Customer left" } });
      expect(own.status()).toBe(200);
      expect((await own.json()).status).toBe("held_voided");
      expect(await available()).toBe(5);
      await page.goto(`/sales/${cart.order_id}`);
      await expect(page.getByText("Released").first()).toBeVisible();
    });
  } finally {
    await request.patch(`${API}/promotions/${promo.promotion_id}`, { headers: admin, data: { active: false } });
  }
});
