import { test, expect, APIRequestContext } from "@playwright/test";
import fs from "fs";
import os from "os";
import path from "path";

// End-to-end coverage for Phase 4, sub-area 4's second reconciliation
// type, Payment Gateway (erp-core-go's phased_roadmap.md /
// docs/phase0_1_design.md §3.17), against a real erp-core-go backend.
// Import/match/unmatch are gated by payment_gateway_reconciliation.manage,
// so this runs as Branch Manager (Meera), matching
// bank-reconciliation.spec.ts's precedent.
//
// A real card checkout is seeded via a direct API call first — the report
// and matching only have something to match against once a captured
// card/UPI payment with a gateway reference actually exists.
const API_BASE = "http://localhost:8080/api/v1";
const BRANCH_ID = "22222222-2222-2222-2222-222222222222";
const TERMINAL_ID = "33333333-3333-3333-3333-333333333333";
const VARIANT_ID = "99999999-9999-9999-9999-999999999999";

async function apiLogin(request: APIRequestContext, email: string, password: string): Promise<string> {
  const resp = await request.post(`${API_BASE}/auth/login`, {
    data: { merchant_code: "acme-sports", email, password },
  });
  expect(resp.ok()).toBeTruthy();
  const body = await resp.json();
  return body.access_token as string;
}

test("import a payment gateway settlement CSV, auto-match, unmatch/re-match, and the reconciliation report", async ({ page, request }) => {
  const unique = Date.now() % 1000000;
  const reference = `UTR-E2E-${unique}`;
  const today = new Date().toISOString().slice(0, 10);

  let paymentAmount = "0.00";

  await test.step("seed a real card checkout with a gateway reference via direct API calls", async () => {
    const token = await apiLogin(request, "arjun@acme-sports.test", "Passw0rd!");
    const headers = { Authorization: `Bearer ${token}` };

    const orderResp = await request.post(`${API_BASE}/sales/orders`, {
      headers,
      data: { branch_id: BRANCH_ID, pos_terminal_id: TERMINAL_ID, idempotency_key: `e2e-pgr-${unique}` },
    });
    expect(orderResp.ok()).toBeTruthy();
    const order = await orderResp.json();

    const lineResp = await request.post(`${API_BASE}/sales/orders/${order.order_id}/lines`, {
      headers,
      data: { variant_id: VARIANT_ID, quantity: 2 },
    });
    expect(lineResp.ok()).toBeTruthy();
    const withLine = await lineResp.json();
    paymentAmount = withLine.grand_total;

    const checkoutResp = await request.post(`${API_BASE}/sales/orders/${order.order_id}/checkout`, {
      headers,
      data: { payments: [{ method: "card", amount: Number(paymentAmount), reference }] },
    });
    expect(checkoutResp.ok()).toBeTruthy();
  });

  await test.step("login as Branch Manager (Meera)", async () => {
    await page.goto("/login");
    await page.fill("#email", "meera@acme-sports.test");
    await page.fill("#password", "9999");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
  });

  // Settle net of a small gateway fee, so the posted journal entry has a
  // genuine non-zero Payment Gateway Fees line to verify against.
  const settledAmount = (Number(paymentAmount) - 0.5).toFixed(2);
  const csvPath = path.join(os.tmpdir(), `e2e-pg-settlement-${unique}.csv`);
  fs.writeFileSync(csvPath, `settlement_date,reference,amount\n${today},${reference},${settledAmount}\n`);

  await test.step("import the settlement CSV and see it auto-match", async () => {
    await page.goto("/payment-gateway-reconciliation");
    await page.setInputFiles("input[type='file']", csvPath);
    await expect(page.getByText(/Imported 1 line\(s\), 1 auto-matched/)).toBeVisible();
    // Scoped to the Import section's own lines viewer, not the
    // Reconciliation Report section rendered further down this same
    // page — both can legitimately show the same line at once (e.g.
    // once unmatched, it's real and unmatched in both places), so an
    // unscoped page-wide row lookup is ambiguous once that happens.
    const importLines = page.getByTestId("import-lines");
    await expect(importLines.getByRole("cell", { name: reference })).toBeVisible();
    const row = importLines.getByRole("row", { name: new RegExp(reference) });
    await expect(row.getByText("matched")).toBeVisible();
  });

  await test.step("the reconciliation report reflects a non-zero matched total for today", async () => {
    await page.goto("/payment-gateway-reconciliation");
    await page.fill("#start", today);
    const matchedTotalLine = page.locator("p", { hasText: "Matched total in range" });
    await expect(matchedTotalLine).not.toContainText("₹0.00");
  });

  await test.step("unmatch it, then manually re-match via the candidate picker", async () => {
    await page.goto("/payment-gateway-reconciliation");
    // Re-open the same import to get back to the full lines table (the
    // report view above only shows unmatched/duplicate lines).
    await page.getByRole("button", { name: "View" }).first().click();
    const row = page.getByTestId("import-lines").getByRole("row", { name: new RegExp(reference) });
    await row.getByRole("button", { name: "Unmatch" }).click();
    await expect(row.getByText("unmatched")).toBeVisible();

    await row.getByRole("button", { name: "Match" }).click();
    const candidateText = page.locator("span.font-mono", { hasText: reference });
    await expect(candidateText).toBeVisible();
    const candidateRow = candidateText.locator("xpath=..");
    await candidateRow.getByRole("button", { name: "Select" }).click();
    await expect(row.getByText("matched")).toBeVisible();
  });

  await test.step("POS User (Ravi) is denied importing a settlement but can still view the report", async () => {
    await page.goto("/dashboard");
    await page.click("text=Log out");
    await page.waitForURL("/login");
    await page.fill("#email", "ravi@acme-sports.test");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    await page.goto("/payment-gateway-reconciliation");
    await expect(page.getByText("Reconciliation report")).toBeVisible();
    await page.setInputFiles("input[type='file']", csvPath);
    await expect(page.getByText(/payment_gateway_reconciliation\.manage/)).toBeVisible();
  });
});
