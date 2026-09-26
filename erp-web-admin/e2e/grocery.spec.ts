import { test, expect } from "@playwright/test";

// End-to-end coverage for Phase 8: Vertical Expansion — Grocery/FMCG
// (erp-core-go's phased_roadmap.md; internal/inventory/batches.go).
// Creates a batch-tracked product through the real New Product screen,
// receives two batches with different expiry dates through the real GRN
// screen, confirms the Expiring Batches report surfaces the sooner one,
// then confirms — via the API, since this app has no checkout UI (see
// credit.spec.ts's own note) — that a sale correctly draws FIFO from the
// soonest-expiring batch first.
const API_BASE = "http://localhost:8080/api/v1";
const BRANCH_ID = "22222222-2222-2222-2222-222222222222";
const TERMINAL_ID = "33333333-3333-3333-3333-333333333333";
const SUPPLIER_ID = "11111111-2222-3333-4444-555555555555";

test("Grocery/FMCG: batch-tracked product, GRN batch receipt, expiring-batches report, and FIFO sale", async ({ page, request }) => {
  const unique = Date.now() % 1000000;
  const productName = `E2E Grocery Item ${unique}`;
  let variantId = "";
  let barcode = "";
  let adminToken = "";

  await page.goto("/login");
  await page.fill("#email", "admin@adrsw.com");
  await page.fill("#password", "Idontsay3#");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");

  await test.step("create a batch-tracked product through the real UI", async () => {
    await page.goto("/products/new");
    await page.fill("#name", productName);
    await page.fill("#sku", `E2E-GROC-${unique}`);
    await page.fill("#mrp", "60");
    await page.fill("#sellingPrice", "55");
    await page.getByText("Track batch/lot number and expiry").click();
    await page.getByRole("button", { name: "Create product" }).click();
    await expect(page).toHaveURL(/\/pricing/, { timeout: 10_000 });
  });

  await test.step("resolve the new variant's id via the API", async () => {
    const login = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "admin@adrsw.com", password: "Idontsay3#" },
    });
    adminToken = (await login.json()).access_token;
    const headers = { Authorization: `Bearer ${adminToken}` };
    const products = await (await request.get(`${API_BASE}/products?q=${encodeURIComponent(productName)}`, { headers })).json();
    variantId = products.products[0].variants[0].variant_id;
    expect(variantId).toBeTruthy();

    // GRN's barcode lookup only resolves a real, registered barcode
    // (barcodes table) — not the SKU. New Product doesn't auto-assign
    // one, so this generates the same way the Barcode Label screen would.
    const barcodeResp = await request.post(`${API_BASE}/products/variants/${variantId}/barcodes`, { headers, data: {} });
    barcode = (await barcodeResp.json()).code;
    expect(barcode).toBeTruthy();
  });

  let grnId = "";

  await test.step("receive two batches with different expiry dates through the real GRN screen", async () => {
    await page.goto("/purchase/grn");
    // The GRN list page has its own "New GRN" flow — simplest robust path
    // here is creating the draft via the API (this step is about proving
    // the ADD-LINE batch fields work through the UI, not re-testing GRN
    // creation itself, which purchase-and-accounting.spec.ts already
    // covers) and driving the rest through the browser.
    const headers = { Authorization: `Bearer ${adminToken}` };
    const grn = await request.post(`${API_BASE}/purchase/grn`, { headers, data: { supplier_id: SUPPLIER_ID, branch_id: BRANCH_ID } });
    grnId = (await grn.json()).grn_id;

    await page.goto(`/purchase/grn/${grnId}`);
    await page.fill("#barcode", barcode);
    await page.getByRole("button", { name: "Look up" }).click();
    await expect(page.getByText(productName)).toBeVisible();
    await page.fill("#quantity", "5");
    await page.fill("#unitCost", "40");
    await page.fill("#batchNo", "E2E-A");
    await page.fill("#expiryDate", "2026-09-30");
    await page.getByRole("button", { name: "Add line" }).click();
    await expect(page.getByText("E2E-A")).toBeVisible();

    await page.fill("#barcode", barcode);
    await page.getByRole("button", { name: "Look up" }).click();
    await page.fill("#quantity", "8");
    await page.fill("#unitCost", "42");
    await page.fill("#batchNo", "E2E-B");
    await page.fill("#expiryDate", "2026-10-15");
    await page.getByRole("button", { name: "Add line" }).click();
    await expect(page.getByText("E2E-B")).toBeVisible();

    await page.getByRole("button", { name: "Complete GRN" }).click();
    await expect(page.getByText("GRN completed")).toBeVisible();
  });

  await test.step("the Expiring Batches report surfaces the sooner batch, not the later one", async () => {
    await page.goto("/inventory/expiring-batches");
    await page.fill("#days", "10");
    await page.getByRole("button", { name: "Refresh" }).click();
    await expect(page.getByRole("row", { name: /E2E-A/ })).toBeVisible();
    await expect(page.getByRole("row", { name: /E2E-B/ })).toHaveCount(0);
  });

  await test.step("a sale of 6 units draws FIFO: all 5 from the sooner batch, 1 from the later one", async () => {
    const headers = { Authorization: `Bearer ${adminToken}` };
    const order = await request.post(`${API_BASE}/sales/orders`, {
      headers, data: { branch_id: BRANCH_ID, pos_terminal_id: TERMINAL_ID, idempotency_key: `e2e-groc-${unique}` },
    });
    const { order_id } = await order.json();
    await request.post(`${API_BASE}/sales/orders/${order_id}/lines`, { headers, data: { variant_id: variantId, quantity: 6 } });

    const batchesResp = await request.get(`${API_BASE}/inventory/expiring-batches?days=60`, { headers });
    const batches = (await batchesResp.json()).batches;
    // E2E-A should be fully drained (0 remaining -> not listed, since the
    // report only lists quantity_remaining > 0); E2E-B should show
    // 8 - 1 = 7 remaining (only 1 of the 6 units needed came from it).
    expect(batches.find((b: { batch_no: string }) => b.batch_no === "E2E-A")).toBeUndefined();
    const batchB = batches.find((b: { batch_no: string }) => b.batch_no === "E2E-B");
    expect(batchB.quantity_remaining).toBe("7.000");
  });
});
