import { test, expect } from "@playwright/test";

// End-to-end coverage for Phase 6 v1 — AI Insights (erp-core-go's
// phased_roadmap.md; internal/ai). Both features are real SQL over this
// merchant's own transaction history, not a trained model: reorder
// suggestions (sales velocity vs. a caller-supplied lead time) and a
// market-basket "frequently bought together" recommendation engine.
// Seeds two real products and three real finalized orders via direct API
// calls (two orders containing both products together, one containing
// only the first) so the recommendation engine has real co-occurrence
// data to compute against, then drives the actual /ai-insights screen.
const API_BASE = "http://localhost:8080/api/v1";
const BRANCH_ID = "22222222-2222-2222-2222-222222222222";
const TERMINAL_ID = "33333333-3333-3333-3333-333333333333";

test("AI Insights: reorder suggestions and frequently-bought-together both reflect real sales data", async ({ page, request }) => {
  const unique = Date.now() % 1000000;
  const productAName = `E2E AI Bat ${unique}`;
  const productBName = `E2E AI Grip ${unique}`;
  let variantAId = "";
  let variantBId = "";

  await test.step("seed two products and three real finalized orders via the API", async () => {
    const login = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "admin@adrsw.com", password: "Idontsay3#" },
    });
    const { access_token } = await login.json();
    const headers = { Authorization: `Bearer ${access_token}` };

    const catRes = await request.get(`${API_BASE}/categories`, { headers });
    const { categories } = await catRes.json();
    const categoryId = categories[0]?.category_id;

    const prodA = await request.post(`${API_BASE}/products`, {
      headers,
      data: { name: productAName, hsn_code: "9506", category_id: categoryId, variants: [{ sku: `E2E-BAT-${unique}`, cost_price: 500, mrp: 1200, selling_price: 999 }] },
    });
    variantAId = (await prodA.json()).variants[0].variant_id;

    const prodB = await request.post(`${API_BASE}/products`, {
      headers,
      data: { name: productBName, hsn_code: "9506", category_id: categoryId, variants: [{ sku: `E2E-GRIP-${unique}`, cost_price: 50, mrp: 150, selling_price: 99 }] },
    });
    variantBId = (await prodB.json()).variants[0].variant_id;

    // Exactly enough stock for the three orders below to each take 1 unit
    // (found live: seeding only 2 silently starved the third order's
    // add-line call with STOCK_UNAVAILABLE, so it never finalized and the
    // recommendation math below was computed against 2 source orders
    // instead of 3 — a test bug, not an internal/ai bug). Zero left over
    // afterward, which is itself still a real "below reorder point" case
    // once the reorder point is set above that.
    await request.post(`${API_BASE}/inventory/adjustments`, {
      headers,
      data: { variant_id: variantAId, branch_id: BRANCH_ID, quantity_delta: 3, reason: `e2e ai-insights seed ${unique}` },
    });
    await request.patch(`${API_BASE}/inventory/reorder-point`, {
      headers,
      data: { variant_id: variantAId, branch_id: BRANCH_ID, reorder_point: 10 },
    });
    await request.post(`${API_BASE}/inventory/adjustments`, {
      headers,
      data: { variant_id: variantBId, branch_id: BRANCH_ID, quantity_delta: 10, reason: `e2e ai-insights seed ${unique}` },
    });

    const finalize = async (variantIds: string[]) => {
      const order = await request.post(`${API_BASE}/sales/orders`, {
        headers,
        data: { branch_id: BRANCH_ID, pos_terminal_id: TERMINAL_ID, idempotency_key: `e2e-ai-${unique}-${variantIds.join("-")}-${Math.random()}` },
      });
      const { order_id } = await order.json();
      for (const v of variantIds) {
        await request.post(`${API_BASE}/sales/orders/${order_id}/lines`, { headers, data: { variant_id: v, quantity: 1 } });
      }
      const loaded = await (await request.get(`${API_BASE}/sales/orders/${order_id}`, { headers })).json();
      await request.post(`${API_BASE}/sales/orders/${order_id}/checkout`, {
        headers,
        data: { payments: [{ method: "cash", amount: Number(loaded.grand_total) }] },
      });
    };
    await finalize([variantAId, variantBId]);
    await finalize([variantAId, variantBId]);
    await finalize([variantAId]);
  });

  await test.step("login as the superadmin and open AI Insights", async () => {
    await page.goto("/login");
    await page.fill("#email", "admin@adrsw.com");
    await page.fill("#password", "Idontsay3#");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
    await page.goto("/ai-insights");
  });

  await test.step("the new product shows up as a reorder suggestion", async () => {
    await page.getByRole("button", { name: "Refresh" }).click();
    const row = page.getByRole("row", { name: new RegExp(productAName) });
    await expect(row).toBeVisible();
    await expect(row).toContainText("below reorder point");
  });

  await test.step("frequently-bought-together correctly surfaces the second product", async () => {
    // Combobox order on this page: Branch (reorder-suggestions section),
    // then Product (this section) — the second one, not the first.
    await page.getByRole("combobox").nth(1).click();
    await page.getByRole("option", { name: new RegExp(productAName) }).click();
    await page.getByRole("button", { name: "Get recommendations" }).click();

    const row = page.getByRole("row", { name: new RegExp(productBName) });
    await expect(row).toBeVisible();
    await expect(row).toContainText("2x"); // bought together in 2 of the 3 finalized orders
    await expect(row).toContainText("67%"); // 2/3 confidence, rounded
  });
});
