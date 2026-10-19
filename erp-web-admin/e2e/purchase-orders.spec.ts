import { test, expect, APIRequestContext, Page } from "@playwright/test";
import { readFileSync } from "node:fs";

// Purchase orders (erp-core-go migrations/057): PO → several GRNs → a
// purchase bill per GRN. A delivery may be smaller than the order; the PO
// is partially received until every ordered unit has arrived, then
// completes by itself. Receiving more than is pending is refused.

const API = process.env.E2E_API_URL ?? "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";
const BRANCH = "22222222-2222-2222-2222-222222222222";
const BAT = "99999999-9999-9999-9999-999999999999";
const SUPPLIER = "11111111-2222-3333-4444-555555555555";

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

async function download(page: Page, click: () => Promise<void>) {
  const [dl] = await Promise.all([page.waitForEvent("download"), click()]);
  return { name: dl.suggestedFilename(), head: readFileSync((await dl.path())!).subarray(0, 5).toString() };
}

test("PO → two partial GRNs → bills; PO completes when everything has arrived", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };

  // A draft PO for 10 bats at ₹1,500, made through the API (the form is covered below).
  const po = await (
    await request.post(`${API}/purchase/orders`, {
      headers: auth,
      data: { supplier_id: SUPPLIER, branch_id: BRANCH, expected_date: "2030-01-15", lines: [{ variant_id: BAT, quantity: 10, unit_cost: 1500 }] },
    })
  ).json();
  expect(po.status).toBe("draft");

  await login(page);
  await page.goto(`/purchase/orders/${po.po_id}`);
  await expect(page.getByTestId("po-subtotal")).toHaveText("₹15,000.00");

  await test.step("draft PDF, then issue", async () => {
    const f = await download(page, () => page.getByRole("button", { name: "Draft PDF" }).click());
    expect(f.name).toBe(`purchase-order-${po.po_number}.pdf`);
    expect(f.head).toBe("%PDF-");
    await page.getByRole("button", { name: "Issue to supplier" }).click();
    await expect(page.getByText("Open", { exact: true })).toBeVisible();
  });

  await test.step("first delivery: 6 of 10", async () => {
    await page.getByRole("button", { name: "Receive goods" }).click();
    const qty = page.getByLabel("Received quantity of SG Cricket Bat");
    await expect(qty).toHaveValue("10"); // prefilled with what is pending
    await qty.fill("6");
    await page.getByRole("button", { name: "Create GRN" }).click();
    await page.waitForURL(/\/purchase\/grn\//);
    await expect(page.getByTestId("grn-po-link")).toHaveText(`Against ${po.po_number}`);
    await page.getByRole("button", { name: "Complete GRN" }).click();
    await expect(page.getByRole("link", { name: "Enter purchase bill" })).toBeVisible();
  });

  await test.step("bill it from the GRN; the bill form starts on that GRN", async () => {
    await page.getByRole("link", { name: "Enter purchase bill" }).click();
    await page.waitForURL(/\/purchase\/bills\/new\?grn_id=/);
    await page.fill("#tax", "1620");
    await page.getByRole("button", { name: "Create bill" }).click();
    await page.waitForURL(/\/purchase\/bills\/[0-9a-f-]{36}$/);
  });

  await test.step("PO is partially received, 4 pending", async () => {
    await page.goto(`/purchase/orders/${po.po_id}`);
    await expect(page.getByText("Partially received", { exact: true })).toBeVisible();
    const row = page.getByTestId("po-lines").getByRole("row", { name: /SG Cricket Bat/ });
    await expect(row.getByRole("cell").nth(2)).toHaveText("6");
    await expect(row.getByRole("cell").nth(3)).toHaveText("4");
    await expect(page.getByTestId("po-grns").getByRole("link", { name: /^BILL-/ })).toBeVisible();
  });

  await test.step("receiving more than pending is refused", async () => {
    await page.getByRole("button", { name: "Receive goods" }).click();
    await page.getByLabel("Received quantity of SG Cricket Bat").fill("5");
    await page.getByRole("button", { name: "Create GRN" }).click();
    await expect(page.getByText("quantity exceeds what is still pending on the purchase order")).toBeVisible();
  });

  await test.step("second delivery: the remaining 4 completes the PO", async () => {
    await page.getByLabel("Received quantity of SG Cricket Bat").fill("4");
    await page.getByRole("button", { name: "Create GRN" }).click();
    await page.waitForURL(/\/purchase\/grn\//);
    await page.getByRole("button", { name: "Complete GRN" }).click();
    await expect(page.getByRole("link", { name: "Enter purchase bill" })).toBeVisible();
    await page.getByTestId("grn-po-link").click();
    await expect(page.getByText("Completed", { exact: true }).first()).toBeVisible();
    await expect(page.getByRole("button", { name: "Receive goods" })).toHaveCount(0);
    await expect(page.getByTestId("po-grns").getByRole("row")).toHaveCount(3); // header + 2 GRNs
    await expect(page.getByTestId("po-grns").getByRole("link", { name: "Enter bill" })).toBeVisible();
  });

  await test.step("a GRN is billed only once", async () => {
    const grns = (await (await request.get(`${API}/purchase/grn?purchase_order_id=${po.po_id}`, { headers: auth })).json()).grns;
    const billed = grns.find((g: { billed: boolean }) => g.billed);
    const r = await request.post(`${API}/purchase/bills`, { headers: auth, data: { grn_id: billed.grn_id, tax_total: 0 } });
    expect(r.status()).toBe(409);
    expect((await r.json()).error.code).toBe("GRN_ALREADY_BILLED");
  });
});

test("new PO form saves a draft; a partly received PO can be closed short", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  await login(page);

  await test.step("form", async () => {
    await page.goto("/purchase/orders/new");
    await page.selectOption("#poSupplier", SUPPLIER);
    await page.getByLabel("Line 1 product").click();
    await page.getByRole("option", { name: /SG Cricket Bat/ }).first().click();
    await page.getByLabel("Line 1 quantity").fill("3");
    await page.getByLabel("Line 1 unit cost").fill("1450");
    await page.getByRole("button", { name: "Save draft" }).click();
    await page.waitForURL(/\/purchase\/orders\/[0-9a-f-]{36}$/);
    await expect(page.getByTestId("po-subtotal")).toHaveText("₹4,350.00");
  });

  await test.step("close short with a reason", async () => {
    const id = page.url().split("/").pop()!;
    await request.post(`${API}/purchase/orders/${id}/issue`, { headers: auth });
    const po = await (await request.get(`${API}/purchase/orders/${id}`, { headers: auth })).json();
    const grn = await (await request.post(`${API}/purchase/orders/${id}/grn`, { headers: auth, data: { lines: [{ po_line_id: po.lines[0].line_id, quantity: 1 }] } })).json();
    await request.post(`${API}/purchase/grn/${grn.grn_id}/complete`, { headers: auth });

    await page.reload();
    await page.getByRole("button", { name: "Close short" }).click();
    await page.fill("#poEndReason", "Supplier discontinued the size");
    await page.getByRole("dialog").getByRole("button", { name: "Close short" }).click();
    await expect(page.getByText("Supplier discontinued the size")).toBeVisible();
    await expect(page.getByRole("button", { name: "Receive goods" })).toHaveCount(0);
  });
});

test("a draft GRN and then its PO can be deleted, and come back from the Recycle Bin", async ({ page, request }) => {
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const po = await (
    await request.post(`${API}/purchase/orders`, { headers: auth, data: { supplier_id: SUPPLIER, branch_id: BRANCH, lines: [{ variant_id: BAT, quantity: 5, unit_cost: 1500 }] } })
  ).json();
  await request.post(`${API}/purchase/orders/${po.po_id}/issue`, { headers: auth });
  const grn = await (await request.post(`${API}/purchase/orders/${po.po_id}/grn`, { headers: auth, data: { lines: [{ po_line_id: po.lines[0].line_id, quantity: 5 }] } })).json();

  await login(page);

  await test.step("the PO can't be deleted while a GRN exists against it", async () => {
    await page.goto(`/purchase/orders/${po.po_id}`);
    await expect(page.getByRole("button", { name: "Receive goods" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Delete" })).toHaveCount(0);
  });

  await test.step("delete the draft GRN; back on the PO, everything is pending again", async () => {
    await page.goto(`/purchase/grn/${grn.grn_id}`);
    await page.getByRole("button", { name: "Delete" }).click();
    await page.fill("#deleteReason", "Wrong delivery");
    await page.getByRole("button", { name: "Delete GRN" }).click();
    await page.waitForURL(`/purchase/orders/${po.po_id}`);
    await expect(page.getByText("Nothing received yet.")).toBeVisible();
  });

  await test.step("delete the open PO", async () => {
    await page.getByRole("button", { name: "Delete" }).click();
    await page.getByRole("button", { name: "Delete purchase order" }).click();
    await page.waitForURL("/purchase/orders");
    expect((await request.get(`${API}/purchase/orders/${po.po_id}`, { headers: auth })).status()).toBe(404);
  });

  await test.step("restore from the Recycle Bin", async () => {
    await page.goto("/recycle-bin");
    await page.getByRole("row", { name: new RegExp(po.po_number) }).getByRole("button", { name: "Restore" }).click();
    await expect(page.getByRole("row", { name: new RegExp(po.po_number) })).toHaveCount(0);
    const back = await (await request.get(`${API}/purchase/orders/${po.po_id}`, { headers: auth })).json();
    expect(back.status).toBe("open");
  });

  await test.step("a completed GRN has no Delete", async () => {
    const g = await (await request.post(`${API}/purchase/orders/${po.po_id}/grn`, { headers: auth, data: { lines: [{ po_line_id: po.lines[0].line_id, quantity: 5 }] } })).json();
    await request.post(`${API}/purchase/grn/${g.grn_id}/complete`, { headers: auth });
    await page.goto(`/purchase/grn/${g.grn_id}`);
    await expect(page.getByRole("link", { name: "Enter purchase bill" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Delete" })).toHaveCount(0);
  });
});
