import { test, expect, Page, APIRequestContext } from "@playwright/test";
import { readFileSync } from "node:fs";

// Phase 14 JV workflow in the browser: post and reverse with approval off,
// then maker-checker with approval on (a cashier submits, the Merchant
// Admin approves and posts; the admin can't approve their own), the journal
// list with CSV export, and the period lock in the voucher form.

const API = "http://localhost:8081/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";
const OFF = { jv_approval_required: false, locked_through: null };

async function login(page: Page, email: string) {
  await page.evaluate(() => localStorage.clear()).catch(() => {});
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

async function fillVoucher(page: Page, description: string, amount: string) {
  await page.getByLabel("Description").fill(description);
  await page.getByLabel("Line 1 account").selectOption("5004");
  await page.getByLabel("Line 1 debit").fill(amount);
  await page.getByLabel("Line 2 account").selectOption("1001");
  await page.getByLabel("Line 2 credit").fill(amount);
  await expect(page.getByTestId("jv-balance")).toHaveText("Balanced");
}

test("Phase 14: journal vouchers post, reverse, and go through maker-checker approval", async ({ page, request }) => {
  const headers = { Authorization: `Bearer ${await token(request)}` };
  const tag = `E2E-JV-${Date.now()}`;
  await request.put(`${API}/accounting/settings`, { headers, data: OFF });

  try {
    await test.step("approval off: a cashier posts a voucher, then reverses it", async () => {
      await login(page, "ravi@acme-sports.test");
      await page.goto("/accounting/vouchers/new");
      await fillVoucher(page, `${tag} petty cash`, "150");
      await page.getByRole("button", { name: "Post now" }).click();
      await page.waitForURL(/\/accounting\/vouchers\/[0-9a-f-]{36}$/);
      await expect(page.getByText("Posted", { exact: true }).first()).toBeVisible();
      await page.getByRole("link", { name: /^JE-/ }).click();
      await expect(page.getByRole("heading", { name: /^Entry JE-/ })).toBeVisible();
      await expect(page.getByText("5004 · Petty Cash Expenses")).toBeVisible();
      await page.getByRole("link", { name: "Open the voucher" }).click();

      await page.getByRole("button", { name: "Reverse" }).click();
      const dialog = page.getByRole("dialog");
      await dialog.getByLabel("Reason").fill("Duplicate entry");
      await dialog.getByRole("button", { name: "Reverse" }).click();
      await expect(page.getByText(/Reversed by .* \(Duplicate entry\)/)).toBeVisible();
    });

    let voucherUrl = "";
    await test.step("approval on: the cashier can only submit", async () => {
      await request.put(`${API}/accounting/settings`, { headers, data: { jv_approval_required: true, locked_through: null } });
      await page.goto("/accounting/vouchers/new");
      await fillVoucher(page, `${tag} rent accrual`, "4000");
      await expect(page.getByRole("button", { name: "Post now" })).toHaveCount(0);
      await page.getByRole("button", { name: "Submit for approval" }).click();
      await page.waitForURL(/\/accounting\/vouchers\/[0-9a-f-]{36}$/);
      voucherUrl = page.url();
      await expect(page.getByText("Waiting for someone with JV approval rights.")).toBeVisible();
      await expect(page.getByRole("button", { name: "Approve" })).toHaveCount(0);
    });

    await test.step("the admin approves and posts it", async () => {
      await login(page, "arjun@acme-sports.test");
      await page.goto(voucherUrl);
      await page.getByRole("button", { name: "Approve" }).click();
      await expect(page.getByText("Approved", { exact: true }).first()).toBeVisible();
      await page.getByRole("button", { name: "Post" }).click();
      await expect(page.getByText(/Posted as JE-/).first()).toBeVisible();
    });

    await test.step("the admin can't approve their own voucher", async () => {
      await page.goto("/accounting/vouchers/new");
      await fillVoucher(page, `${tag} admin own`, "10");
      await page.getByRole("button", { name: "Submit for approval" }).click();
      await page.waitForURL(/\/accounting\/vouchers\/[0-9a-f-]{36}$/);
      await expect(page.getByRole("button", { name: "Approve" })).toBeDisabled();
    });

    await test.step("the journal finds them, and exports CSV", async () => {
      await page.goto("/accounting/journal-entries");
      await page.getByRole("searchbox", { name: "Search the journal" }).fill(tag);
      await expect(page.getByRole("row").filter({ hasText: "Voucher reversal" })).toHaveCount(1);
      await expect(page.getByRole("row").filter({ hasText: "Journal voucher" })).toHaveCount(2);
      const [download] = await Promise.all([page.waitForEvent("download"), page.getByRole("button", { name: "CSV" }).click()]);
      expect(download.suggestedFilename()).toMatch(/^journal-\d{8}\.csv$/);
      const csv = readFileSync((await download.path())!, "utf8");
      expect(csv.split("\n")[0]).toBe("entry_number,entry_date,source_type,description,branch,account_code,account_name,debit,credit,party_type,party_id,party_name");
      expect(csv).toContain(`${tag} rent accrual`);
    });

    await test.step("a locked period is refused in the form", async () => {
      const yesterday = new Date(Date.now() - 86_400_000).toISOString().slice(0, 10);
      await request.put(`${API}/accounting/settings`, { headers, data: { jv_approval_required: false, locked_through: yesterday } });
      await page.goto("/accounting/vouchers/new");
      await page.getByLabel("Date").fill(yesterday);
      await expect(page.getByText(`The books are locked through ${yesterday}`)).toBeVisible();
    });
  } finally {
    await request.put(`${API}/accounting/settings`, { headers, data: OFF });
  }
});
