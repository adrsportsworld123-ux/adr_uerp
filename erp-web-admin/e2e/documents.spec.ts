import { test, expect, APIRequestContext, Page } from "@playwright/test";
import { readFileSync } from "node:fs";

// Downloadable documents (erp-core-go internal/documents) and a branch's
// letterhead (branch_print_settings, migrations/049): the receipt, GRN,
// purchase bill and purchase return each download as a real PDF from the
// API, and the print-settings screen saves the header/footer and logo that
// those PDFs carry. A POS User can download but not change the letterhead.

const API = "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";
const BRANCH = "22222222-2222-2222-2222-222222222222";
const TILL = "33333333-3333-3333-3333-333333333333";
const BAT = "99999999-9999-9999-9999-999999999999";
const SUPPLIER = "11111111-2222-3333-4444-555555555555";

async function token(request: APIRequestContext, email: string) {
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email, password: "Passw0rd!" } });
  return (await r.json()).access_token as string;
}

async function login(page: Page, email: string) {
  await page.goto("/login");
  await page.fill("#email", email);
  await page.fill("#password", "Passw0rd!");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

/** Clicks a download control and returns the saved file's name and first bytes. */
async function download(page: Page, click: () => Promise<void>) {
  const [dl] = await Promise.all([page.waitForEvent("download"), click()]);
  const head = readFileSync((await dl.path())!).subarray(0, 5).toString();
  return { name: dl.suggestedFilename(), head };
}

test("sale and purchase documents download as PDFs", async ({ page, request }) => {
  const admin = { Authorization: `Bearer ${await token(request, "arjun@acme-sports.test")}` };
  const cashier = { Authorization: `Bearer ${await token(request, "ravi@acme-sports.test")}` };

  // A received and billed GRN, and a return against it.
  const grn = await (await request.post(`${API}/purchase/grn`, { headers: admin, data: { supplier_id: SUPPLIER, branch_id: BRANCH } })).json();
  await request.post(`${API}/purchase/grn/${grn.grn_id}/lines`, { headers: admin, data: { variant_id: BAT, quantity: 4, unit_cost: 1500 } });
  await request.post(`${API}/purchase/grn/${grn.grn_id}/complete`, { headers: admin });
  const bill = await (await request.post(`${API}/purchase/bills`, { headers: admin, data: { grn_id: grn.grn_id, tax_total: 1080 } })).json();
  const ret = await (
    await request.post(`${API}/purchase/returns`, {
      headers: admin,
      data: { supplier_id: SUPPLIER, branch_id: BRANCH, grn_id: grn.grn_id, reason: "e2e damaged", lines: [{ variant_id: BAT, quantity: 1, unit_cost: 1500 }] },
    })
  ).json();

  // A finalized sale at the till.
  const order = await (
    await request.post(`${API}/sales/orders`, { headers: cashier, data: { branch_id: BRANCH, pos_terminal_id: TILL, idempotency_key: `e2e-docs-${Date.now()}` } })
  ).json();
  await request.post(`${API}/sales/orders/${order.order_id}/lines`, { headers: cashier, data: { variant_id: BAT, quantity: 1 } });
  const cart = await (await request.get(`${API}/sales/orders/${order.order_id}`, { headers: cashier })).json();
  await request.post(`${API}/sales/orders/${order.order_id}/checkout`, { headers: cashier, data: { payments: [{ method: "cash", amount: Number(cart.grand_total) }] } });

  await login(page, "arjun@acme-sports.test");

  await test.step("sale receipt, A4 and 80 mm", async () => {
    await page.goto(`/sales/${order.order_id}`);
    const a4 = await download(page, () => page.getByRole("button", { name: "Receipt PDF" }).click());
    expect(a4.name).toMatch(/^receipt-.+\.pdf$/);
    expect(a4.head).toBe("%PDF-");
    const roll = await download(page, () => page.getByRole("button", { name: "80 mm" }).click());
    expect(roll.head).toBe("%PDF-");
  });

  await test.step("GRN", async () => {
    await page.goto(`/purchase/grn/${grn.grn_id}`);
    const f = await download(page, () => page.getByRole("button", { name: "Download PDF" }).click());
    expect(f.name).toBe(`grn-${grn.grn_number}.pdf`);
    expect(f.head).toBe("%PDF-");
  });

  await test.step("purchase bill", async () => {
    await page.goto(`/purchase/bills/${bill.bill_id}`);
    const f = await download(page, () => page.getByRole("button", { name: "Download PDF" }).click());
    expect(f.name).toBe(`purchase-bill-${bill.bill_number}.pdf`);
    expect(f.head).toBe("%PDF-");
  });

  await test.step("purchase return, from the list", async () => {
    await page.goto("/purchase/returns");
    const f = await download(page, () => page.getByRole("button", { name: `Download ${ret.return_number}` }).click());
    expect(f.name).toBe(`purchase-return-${ret.return_number}.pdf`);
    expect(f.head).toBe("%PDF-");
  });
});

test("a branch's letterhead: header, footer and logo", async ({ page, request }) => {
  const admin = { Authorization: `Bearer ${await token(request, "arjun@acme-sports.test")}` };
  const before = await (await request.get(`${API}/branches/${BRANCH}/print-settings`, { headers: admin })).json();
  const name = `E2E Sports ${Date.now()}`;

  try {
    await login(page, "arjun@acme-sports.test");
    await page.goto(`/branches/${BRANCH}/print-settings`);
    await expect(page.getByTitle("Document preview")).toBeVisible();

    await page.getByLabel("Name on documents").fill(name);
    await page.getByLabel("Header lines").fill("Authorised dealer");
    await page.getByLabel("Sale receipt footer").fill("No exchange without bill.\nThank you!");
    await page.getByLabel("Default receipt paper").selectOption("thermal");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("Print settings saved")).toBeVisible();

    // Any PNG will do as a logo: a screenshot of the page itself.
    const png = await page.screenshot({ clip: { x: 0, y: 0, width: 240, height: 120 } });
    await page.getByLabel("Logo file").setInputFiles({ name: "logo.png", mimeType: "image/png", buffer: png });
    await expect(page.getByText("Logo uploaded")).toBeVisible();
    await expect(page.getByRole("button", { name: "Replace" })).toBeVisible();

    const saved = await (await request.get(`${API}/branches/${BRANCH}/print-settings`, { headers: admin })).json();
    expect(saved).toMatchObject({ display_name: name, header_text: "Authorised dealer", receipt_format: "thermal", has_logo: true });
    expect(saved.receipt_footer).toBe("No exchange without bill.\nThank you!");

    const preview = await request.get(`${API}/branches/${BRANCH}/print-settings/preview`, { headers: admin });
    expect(preview.headers()["content-type"]).toBe("application/pdf");
  } finally {
    await request.put(`${API}/branches/${BRANCH}/print-settings`, { headers: admin, data: before });
    if (!before.has_logo) await request.delete(`${API}/branches/${BRANCH}/print-settings/logo`, { headers: admin });
  }
});

test("a POS User can't change the letterhead", async ({ page, request }) => {
  const cashier = { Authorization: `Bearer ${await token(request, "ravi@acme-sports.test")}` };
  const denied = await request.put(`${API}/branches/${BRANCH}/print-settings`, { headers: cashier, data: { display_name: "nope" } });
  expect(denied.status()).toBe(403);

  await login(page, "ravi@acme-sports.test");
  await page.goto(`/branches/${BRANCH}/print-settings`);
  await expect(page.getByRole("button", { name: "Save" })).toBeDisabled();
  await expect(page.getByText(/Needs the branch.print_settings permission/).first()).toBeVisible();
});
