import { test, expect, APIRequestContext } from "@playwright/test";
import fs from "fs";
import os from "os";
import path from "path";

// End-to-end coverage for Phase 4's third sub-area, Bank Reconciliation
// (erp-core-go's phased_roadmap.md), against a real erp-core-go backend.
// Import/match/unmatch are gated by bank_reconciliation.manage, so this
// runs as Branch Manager (Meera), matching pricing.spec.ts's precedent.
//
// A real CSV file is written to a temp path and uploaded via
// page.setInputFiles — Playwright needs an actual file on disk for file
// inputs, it can't hand the browser an in-memory string the way the API
// tests in this suite hand JSON bodies to `request`.
const API_BASE = "http://localhost:8080/api/v1";
const BRANCH_ID = "22222222-2222-2222-2222-222222222222";

async function apiLogin(request: APIRequestContext, email: string, password: string): Promise<string> {
  const resp = await request.post(`${API_BASE}/auth/login`, {
    data: { merchant_code: "acme-sports", email, password },
  });
  expect(resp.ok()).toBeTruthy();
  const body = await resp.json();
  return body.access_token as string;
}

test("import a bank statement CSV, auto-match, manual match, and the reconciliation report", async ({ page, request }) => {
  const unique = Date.now() % 1000000;
  const amount = 10000 + unique / 100; // a distinctive amount nothing else in this repo's test data will collide with
  const today = new Date().toISOString().slice(0, 10);

  await test.step("seed a matching ledger entry via a direct manual journal entry", async () => {
    const token = await apiLogin(request, "ravi@acme-sports.test", "Passw0rd!");
    const resp = await request.post(`${API_BASE}/accounting/journal-entries`, {
      headers: { Authorization: `Bearer ${token}` },
      data: {
        branch_id: BRANCH_ID,
        description: `E2E bank recon test ${unique}`,
        lines: [
          { account_code: "1002", debit: amount },
          { account_code: "1001", credit: amount },
        ],
      },
    });
    expect(resp.ok()).toBeTruthy();
  });

  await test.step("login as Branch Manager (Meera)", async () => {
    await page.goto("/login");
    await page.fill("#email", "meera@acme-sports.test");
    await page.fill("#password", "9999");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
  });

  const csvPath = path.join(os.tmpdir(), `e2e-bank-statement-${unique}.csv`);
  fs.writeFileSync(csvPath, `date,description,reference,amount\n${today},E2E deposit,REF${unique},${amount.toFixed(2)}\n`);

  await test.step("import the CSV and see it auto-match", async () => {
    await page.goto("/bank-reconciliation");
    await page.setInputFiles("input[type='file']", csvPath);
    await expect(page.getByText(/Imported 1 line\(s\), 1 auto-matched/)).toBeVisible();
    await expect(page.getByRole("cell", { name: `REF${unique}` })).toBeVisible();
    // Scoped to the Import section's own lines viewer, not the
    // Reconciliation Report section rendered further down this same
    // page — both can legitimately show the same line at once (e.g.
    // once unmatched, it's real and unmatched in both places), so an
    // unscoped page-wide row lookup is ambiguous once that happens.
    const row = page.getByTestId("import-lines").getByRole("row", { name: new RegExp(`REF${unique}`) });
    await expect(row.getByText("matched")).toBeVisible();
  });

  await test.step("unmatch it, then manually re-match via the candidate picker", async () => {
    const row = page.getByTestId("import-lines").getByRole("row", { name: new RegExp(`REF${unique}`) });
    await row.getByRole("button", { name: "Unmatch" }).click();
    await expect(row.getByText("unmatched")).toBeVisible();

    await row.getByRole("button", { name: "Match" }).click();
    const candidateText = page.locator("span", { hasText: `E2E bank recon test ${unique}` });
    await expect(candidateText).toBeVisible();
    // The candidate row is the flex container two levels up from the
    // description span (span -> its flex row div) — button and text are
    // siblings within it.
    const candidateRow = candidateText.locator("xpath=..");
    await candidateRow.getByRole("button", { name: "Select" }).click();
    await expect(row.getByText("matched")).toBeVisible();
  });

  await test.step("the reconciliation report reflects a non-zero matched total for today", async () => {
    await page.goto("/bank-reconciliation");
    await page.fill("#start", today);
    const matchedTotalLine = page.locator("p", { hasText: "Matched total in range" });
    await expect(matchedTotalLine).not.toContainText("₹0.00");
  });

  await test.step("POS User (Ravi) is denied importing a statement", async () => {
    await page.goto("/dashboard");
    await page.click("text=Log out");
    await page.waitForURL("/login");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    await page.goto("/bank-reconciliation");
    await page.setInputFiles("input[type='file']", csvPath);
    await expect(page.getByText(/bank_reconciliation\.manage/)).toBeVisible();
  });
});
