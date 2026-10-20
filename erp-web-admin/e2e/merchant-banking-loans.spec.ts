import { test, expect, APIRequestContext, Page } from "@playwright/test";

// erp-core-go migrations/065-067: the Company Profile, promotions on MRP
// with Copy and the state filter, a bank account with a cheque book, a
// cheque written, printed and cleared, a received post-dated cheque, an
// employee loan with its schedule and a repayment, and an employee as a
// voucher party. Creates its own data; leaves the merchant's profile as
// it found it.

const API = process.env.E2E_API_URL ?? "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";

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

async function pick(page: Page, trigger: string, search: string, text: string, option: RegExp) {
  await page.getByLabel(trigger, { exact: true }).first().click();
  await page.getByPlaceholder(search).fill(text);
  await page.getByRole("option", { name: option }).first().click();
}

test("company profile, promotions on MRP, bank accounts and cheques, employee loans", async ({ page, request }) => {
  test.setTimeout(240_000);
  const auth = { Authorization: `Bearer ${await token(request)}` };
  const before = await (await request.get(`${API}/merchant/profile`, { headers: auth })).json();
  const stamp = Date.now().toString().slice(-6);

  try {
    await login(page);

    await test.step("Company Profile: validated, saved", async () => {
      await page.goto("/company-profile");
      await expect(page.getByRole("heading", { name: "Company Profile" })).toBeVisible();
      await page.getByLabel("PAN").fill("123");
      await page.getByRole("button", { name: "Save" }).click();
      await expect(page.getByText("pan is not a valid PAN", { exact: false })).toBeVisible();
      await page.getByLabel("PAN").fill("");
      await page.getByLabel("Trade name (on receipts)").fill(`Acme Sports ${stamp}`);
      await page.getByLabel("City").fill("Bengaluru");
      await page.getByLabel("State").selectOption("29");
      await page.getByRole("button", { name: "Save" }).click();
      await expect(page.getByText("Company profile saved")).toBeVisible();
      const p = await (await request.get(`${API}/merchant/profile`, { headers: auth })).json();
      expect(p.trade_name).toBe(`Acme Sports ${stamp}`);
      expect(p.address.state).toBe("Karnataka");
    });

    await test.step("Promotions: on MRP, Copy, state filter", async () => {
      const name = `E2E MRP ${stamp}`;
      const r = await request.post(`${API}/promotions`, {
        headers: auth,
        data: { name, promo_type: "percent", config: { value_percent: 10 }, apply_on: "mrp" },
      });
      expect(r.status()).toBe(201);
      await page.goto("/promotions");
      const row = page.getByRole("row", { name: new RegExp(name) }).first();
      await expect(row).toContainText("10% off MRP");
      await row.getByRole("button", { name: "Copy" }).click();
      await expect(page.getByText("Copied — edit it, then switch it on")).toBeVisible();
      await expect(page.getByLabel("Apply on")).toHaveValue("mrp");
      await page.getByRole("button", { name: "Save changes" }).click();
      await expect(page.getByText("Promotion updated")).toBeVisible();
      const copy = page.getByRole("row", { name: new RegExp(`Copy of ${name}`) });
      await expect(copy).toContainText("Inactive");
      await page.getByLabel("promotions status").selectOption("running");
      await expect(page.getByRole("row", { name: new RegExp(`Copy of ${name}`) })).toHaveCount(0);
      await page.getByLabel("promotions status").selectOption("inactive");
      await expect(page.getByRole("row", { name: new RegExp(`Copy of ${name}`) })).toBeVisible();
      await page.getByLabel("promotions status").selectOption("");
      // Leave no running promotion behind for other specs.
      const list = await (await request.get(`${API}/promotions`, { headers: auth })).json();
      for (const p of list.promotions.filter((x: { name: string }) => x.name.includes(`E2E MRP ${stamp}`))) {
        await request.patch(`${API}/promotions/${p.promotion_id}`, { headers: auth, data: { active: false } });
      }
    });

    const accountNo = `9988${stamp}`;
    await test.step("Bank Accounts: add one with a cheque book", async () => {
      await page.goto("/bank-accounts");
      await page.getByRole("button", { name: "New bank account" }).click();
      await page.getByLabel("Bank", { exact: true }).fill("E2E Bank");
      await page.getByLabel("Name on the account").fill("Acme Sports");
      await page.getByLabel("Account number").fill(accountNo);
      await page.getByLabel("IFSC").fill("HDFC0004321");
      await page.getByLabel("Its own ledger account (1002-NN, under Bank)").check();
      await page.getByRole("button", { name: "Save" }).click();
      await expect(page.getByText("Bank account added")).toBeVisible();
      const row = page.getByTestId("bank-account-row").filter({ hasText: `E2E Bank ••${accountNo.slice(-4)}` });
      await expect(row).toContainText(/1002-\d\d/);
      await row.getByRole("button", { name: /Cheque books/ }).click();
      await page.getByLabel("First leaf").fill("700001");
      await page.getByLabel("Last leaf").fill("700010");
      await page.getByRole("button", { name: "Add book" }).click();
      await expect(page.getByText("Cheque book added")).toBeVisible();
      await expect(page.getByRole("dialog").getByText("700001", { exact: true })).toBeVisible();
      await page.getByRole("button", { name: "Done" }).click();
      await expect(row).toContainText("10 leaves in 1 book");
    });

    await test.step("Cheques: write, print, clear", async () => {
      await page.goto("/cheques");
      await page.getByRole("button", { name: "Write a cheque" }).click();
      const accounts = (await (await request.get(`${API}/bank-accounts`, { headers: auth })).json()).bank_accounts;
      const mine = accounts.find((x: { account_number: string }) => x.account_number === accountNo);
      await page.getByLabel("From account").selectOption(mine.bank_account_id);
      await page.getByLabel("Pay", { exact: true }).fill("Prestige Estates");
      await page.getByLabel("Amount").fill("12500");
      await page.getByLabel("For (on the register)").fill(`Rent ${stamp}`);
      await page.getByRole("button", { name: "Write cheque" }).click();
      await expect(page.getByText(/Cheque 700001 written/)).toBeVisible();
      const row = page.getByTestId("issued-cheque-row").filter({ hasText: `Rent ${stamp}` });
      await expect(row).toContainText("700001");
      await expect(row.getByRole("button", { name: "Print" })).toBeVisible();
      await row.getByRole("button", { name: "Clear" }).click();
      await page.getByRole("button", { name: "Mark cleared" }).click();
      await expect(row).toContainText("Cleared");
    });

    await test.step("Cheques: a post-dated customer cheque can't be deposited early", async () => {
      const c = await (await request.post(`${API}/customers`, {
        headers: auth,
        data: { name: `E2E Cheque Co ${stamp}`, phone: `95${stamp}0011`.slice(0, 10), customer_type: "b2c" },
      })).json();
      const later = new Date(Date.now() + 20 * 86400_000).toISOString().slice(0, 10);
      const branch = (await (await request.get(`${API}/branches`, { headers: auth })).json()).branches[0].branch_id;
      const r = await request.post(`${API}/cheques/received`, {
        headers: auth,
        data: { branch_id: branch, customer_id: c.customer_id, cheque_number: `55${stamp}`, cheque_date: later, amount: 900 },
      });
      expect(r.status()).toBe(201);
      await page.goto("/cheques?tab=received");
      const row = page.getByTestId("received-cheque-row").filter({ hasText: `55${stamp}` });
      await expect(row).toContainText("post-dated");
      await expect(row.getByRole("button", { name: "Deposit" })).toBeDisabled();
    });

    await test.step("Employee Loans: pay out, schedule, repay", async () => {
      await page.goto("/hr/loans");
      await page.getByRole("button", { name: "New loan or advance" }).click();
      await pick(page, "Employee", "Name or employee code", "Ravi", /Ravi Kumar/);
      await page.getByLabel("Amount").fill("12000");
      await page.getByLabel("Interest (% a year, flat)").fill("10");
      await page.getByLabel("Months").fill("12");
      await page.getByLabel("By", { exact: true }).selectOption("cash");
      await expect(page.getByText(/EMI\s*₹1,100\.00/)).toBeVisible();
      await page.getByRole("button", { name: "Pay out" }).click();
      await expect(page.getByText(/paid out/)).toBeVisible();
      const dialog = page.getByRole("dialog");
      await expect(dialog.getByRole("row")).toHaveCount(13); // header + 12 instalments
      await dialog.getByRole("button", { name: "Record a repayment" }).click();
      await dialog.getByLabel("Amount").fill("1100");
      await dialog.getByRole("button", { name: "Record repayment" }).click();
      await expect(page.getByText("Repayment recorded")).toBeVisible();
      await expect(dialog.getByText(/Recovered · Cash/)).toBeVisible();
      await dialog.getByRole("button", { name: "Done" }).click();
    });

    await test.step("Vouchers: an employee as the party", async () => {
      await page.goto("/accounting/party-ledger");
      await page.getByLabel("Party type").selectOption("employee");
      await expect(page.getByLabel("Party type")).toHaveValue("employee");
    });
  } finally {
    await request.put(`${API}/merchant/profile`, {
      headers: auth,
      data: { trade_name: before.trade_name ?? "", address: before.address ?? {} },
    });
  }
});
