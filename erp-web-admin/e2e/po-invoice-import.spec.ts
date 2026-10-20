import { test, expect, APIRequestContext, Page } from "@playwright/test";

// A supplier invoice imported against a purchase order (erp-core-go §3.74):
// more than ordered on one item, an item not on the PO (created as a new
// product), an ordered item that didn't come. The differences are accepted,
// the PO is closed short, and the PO page shows the extra and the unplanned
// item.

const API = process.env.E2E_API_URL ?? "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";
const BRANCH = "22222222-2222-2222-2222-222222222222";
const BAT = "99999999-9999-9999-9999-999999999999";
const SUPPLIER = "11111111-2222-3333-4444-555555555555";
const HEAD = "item_name,quantity,unit_cost,gst_rate,mrp,selling_price,line_total,sku,barcode,hsn_code,category,brand,sale_gst_rate,batch_no,expiry_date\n";

async function token(request: APIRequestContext) {
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email: "arjun@acme-sports.test", password: "Passw0rd!" } });
  return (await r.json()).access_token as string;
}

async function login(page: Page) {
  await page.goto("/login");
  await page.fill("#email", "arjun@acme-sports.test");
  await page.fill("#password", "Passw0rd!");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

test("invoice against a PO: over, not on the PO, not delivered; accept and close short", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const tag = Date.now().toString().slice(-7);
  const grip = `E2E GRIP ${tag}`;
  const towel = `E2E TOWEL GRIP ${tag}`;

  // The ordered grip exists already (a direct import makes it).
  const seed = await request.post(`${API}/purchase/imports/commit`, {
    headers: auth,
    data: {
      supplier_id: SUPPLIER,
      branch_id: BRANCH,
      invoice_number: `E2E-SEED-${tag}`,
      invoice_date: "2026-10-01",
      csv: HEAD + `${grip},1,100,18,200,180,,,,,,,18,,\n`,
    },
  });
  expect(seed.status()).toBe(201);
  const products = await (await request.get(`${API}/products?q=${encodeURIComponent(grip)}`, { headers: auth })).json();
  const gripVariant = (products.products ?? products.items)[0].variants[0].variant_id as string;

  const po = await (
    await request.post(`${API}/purchase/orders`, {
      headers: auth,
      data: { supplier_id: SUPPLIER, branch_id: BRANCH, lines: [{ variant_id: gripVariant, quantity: 5, unit_cost: 100 }, { variant_id: BAT, quantity: 2, unit_cost: 1500 }] },
    })
  ).json();
  await request.post(`${API}/purchase/orders/${po.po_id}/issue`, { headers: auth });

  await login(page);
  await page.goto(`/purchase/orders/${po.po_id}`);
  await page.locator(`a[href="/purchase/import?po=${po.po_id}"]`).click(); // not the sidebar's Import invoice
  await page.waitForURL(/\/purchase\/import\?po=/);

  await test.step("preview: the PO is chosen and the lines are compared with it", async () => {
    await expect(page.locator("#imp-po")).toContainText(po.po_number);
    await page.fill("#imp-no", `E2E-INV-${tag}`);
    await page.setInputFiles("#imp-file", {
      name: "invoice.csv",
      mimeType: "text/csv",
      buffer: Buffer.from(HEAD + `${grip},7,100,18,,,,,,,,,,,\n${towel},4,120,18,180,180,,,,,,,18,,\n`),
    });
    await page.getByRole("button", { name: "Preview" }).click();
    const poLines = page.getByTestId("import-po-lines");
    await expect(poLines.getByRole("row", { name: new RegExp(grip) })).toContainText("+2 extra");
    await expect(poLines.getByRole("row", { name: /SG Cricket Bat/ })).toContainText("Not delivered");
    await expect(page.getByText("Over the PO")).toBeVisible();
    await expect(page.getByText("Not on the PO")).toBeVisible();
    await expect(page.getByText(/the invoice differs from purchase order/)).toBeVisible();
    await expect(page.getByRole("button", { name: "Import invoice" })).toBeDisabled();
  });

  await test.step("accept the differences and close the PO short", async () => {
    await page.getByLabel(/Accept the differences/).check();
    await expect(page.getByRole("button", { name: "Import invoice" })).toBeEnabled();
    await page.getByLabel(/Close the PO short/).check();
    await expect(page.getByText(/give a reason for closing/)).toBeVisible();
    await page.fill("#imp-close-reason", "Bats not available this season");
    await page.getByRole("button", { name: "Apply" }).click();
    await expect(page.getByText(/give a reason for closing/)).toBeHidden();
    await page.getByRole("button", { name: "Import invoice" }).click();
    await expect(page.getByRole("link", { name: `Purchase order ${po.po_number}` })).toBeVisible();
    await expect(page.getByText("Closed", { exact: true })).toBeVisible();
  });

  await test.step("the PO shows the extra quantity and the item that wasn't ordered", async () => {
    await page.getByRole("link", { name: `Purchase order ${po.po_number}` }).click();
    await page.waitForURL(new RegExp(`/purchase/orders/${po.po_id}`));
    await expect(page.getByTestId("po-lines").getByRole("row", { name: new RegExp(grip) })).toContainText("+2 extra");
    await expect(page.getByTestId("po-unplanned")).toContainText(towel);
    await expect(page.getByText("Bats not available this season")).toBeVisible();
  });
});
