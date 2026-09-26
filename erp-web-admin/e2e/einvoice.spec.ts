import { test, expect } from "@playwright/test";

// End-to-end coverage for Phase 4's last sub-area: E-Invoicing (IRN
// generation, QR code) and E-Way Bill (erp-core-go's phased_roadmap.md;
// migrations/022_einvoice.sql) — closed 2026-09-23 behind a vendor-
// agnostic stub GSP client (no real GST Suvidha Provider chosen yet).
//
// Seeds an interstate B2B order via direct API calls (same pattern
// audit-logs.spec.ts uses), then drives the actual /e-invoicing UI page
// for generation and cancellation — the part a browser-driven test adds
// over hitting the API directly. Temporarily bumps the seed variant's
// price/stock to clear the e-way-bill interstate >Rs.50k threshold, and
// restores both afterward (same restore-after-test discipline
// inventory.spec.ts already uses for this variant).
const API_BASE = "http://localhost:8080/api/v1";
const VARIANT_ID = "99999999-9999-9999-9999-999999999999";
const BRANCH_ID = "22222222-2222-2222-2222-222222222222";
const TERMINAL_ID = "33333333-3333-3333-3333-333333333333";

test("e-invoice and e-way bill: generate and cancel an interstate B2B order's compliance documents", async ({ page, request }) => {
  const unique = Date.now();
  let orderId = "";
  let adjustedOnHand = false;

  await test.step("seed: bump price/stock and create a finalized interstate B2B order via the API", async () => {
    const login = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "arjun@acme-sports.test", password: "Passw0rd!" },
    });
    const { access_token } = await login.json();
    const headers = { Authorization: `Bearer ${access_token}` };

    const customer = await request.post(`${API_BASE}/customers`, {
      headers,
      data: {
        name: `E2E Interstate Buyer ${unique}`,
        phone: `9${unique.toString().slice(-9)}`,
        email: `einvoice-e2e-${unique}@example.com`,
        customer_type: "b2b",
        gstin: "27AAAPL1234C1ZV", // Maharashtra (27) — interstate vs. the seed branch's Karnataka (29) merchant GSTIN
      },
    });
    const { customer_id } = await customer.json();

    await request.patch(`${API_BASE}/pricing/variants/${VARIANT_ID}`, { headers, data: { selling_price: 15000 } });
    await request.post(`${API_BASE}/inventory/adjustments`, {
      headers,
      data: { variant_id: VARIANT_ID, branch_id: BRANCH_ID, quantity_delta: 10, reason: `e2e einvoice threshold bump ${unique}` },
    });
    adjustedOnHand = true;

    const order = await request.post(`${API_BASE}/sales/orders`, {
      headers,
      data: { branch_id: BRANCH_ID, pos_terminal_id: TERMINAL_ID, idempotency_key: `e2e-einvoice-${unique}` },
    });
    const orderBody = await order.json();
    orderId = orderBody.order_id;

    await request.post(`${API_BASE}/sales/orders/${orderId}/customer`, { headers, data: { customer_id } });
    await request.post(`${API_BASE}/sales/orders/${orderId}/lines`, {
      headers,
      data: { variant_id: VARIANT_ID, quantity: 5 },
    });
    await request.post(`${API_BASE}/sales/orders/${orderId}/checkout`, {
      headers,
      data: { payments: [{ method: "cash", amount: 88500 }] },
    });
  });

  await test.step("login as Merchant Admin and load the order on the e-invoicing page", async () => {
    await page.goto("/login");
    await page.fill("#email", "arjun@acme-sports.test");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    await page.goto("/e-invoicing");
    await page.fill("#orderId", orderId);
    await page.click("text=Load");
    await expect(page.getByText("No e-invoice generated for this order yet.")).toBeVisible();
  });

  await test.step("generate the e-invoice and see a real-shaped IRN", async () => {
    await page.click("text=Generate e-invoice");
    await expect(page.getByText(/IRN:/)).toBeVisible();
    await expect(page.getByText("generated", { exact: true })).toBeVisible();
  });

  await test.step("generate the e-way bill and see the interstate/required-by-rule badges", async () => {
    await page.fill("#vehicleNo", "KA01AB1234");
    await page.fill("#distanceKm", "850");
    await page.click("text=Generate e-way bill");
    await expect(page.getByText(/EWB No:/)).toBeVisible();
    await expect(page.getByText("Interstate", { exact: true })).toBeVisible();
    await expect(page.getByText(/Required by rule/)).toBeVisible();
  });

  await test.step("cancel both documents", async () => {
    await page.fill("#cancelReasonEwb", "e2e test cancel");
    await page.click("text=Cancel e-way bill");
    await expect(page.getByText("Cancelled.")).toBeVisible();

    await page.fill("#cancelReasonEi", "e2e test cancel");
    await page.click("text=Cancel e-invoice");
    await expect(page.getByText(/a cancelled IRN can never be reused/)).toBeVisible();
  });

  await test.step("cleanup: restore the seed variant's price and stock", async () => {
    const login = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "arjun@acme-sports.test", password: "Passw0rd!" },
    });
    const { access_token } = await login.json();
    const headers = { Authorization: `Bearer ${access_token}` };

    await request.patch(`${API_BASE}/pricing/variants/${VARIANT_ID}`, { headers, data: { selling_price: 2299.0 } });
    if (adjustedOnHand) {
      // Net effect: +10 (seed bump) - 5 (this test's own checkout) - 1 (already owed
      // from an earlier manual verification pass this same session) = need +... this
      // is intentionally symmetric per-run: +10 bump, -5 consumed by this test's own
      // checkout, so a -5 adjustment here returns on_hand to exactly where it was
      // before this test ran, regardless of what any other test left behind.
      await request.post(`${API_BASE}/inventory/adjustments`, {
        headers,
        data: { variant_id: VARIANT_ID, branch_id: BRANCH_ID, quantity_delta: -5, reason: `e2e einvoice cleanup ${unique}` },
      });
    }
  });
});
