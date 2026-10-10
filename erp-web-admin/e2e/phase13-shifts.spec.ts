import { test, expect, Page, APIRequestContext } from "@playwright/test";

// Phase 13 shifts, admin side. The till side (open, cash in/out, count and
// close) is the POS's job and is covered by erp-core-go's live checks and
// the Flutter tests; here a shift is run through the API exactly as the
// POS sends it, and the admin is checked to (1) switch "require a shift"
// on the Sales Policy page (phase13-return-policy.spec.ts, the one spec
// that edits sales settings, so parallel workers never race on them) and
// (2) see the shift close in the cash reconciliation history, here.

const API = "http://localhost:8080/api/v1";
const MERCHANT = process.env.E2E_MERCHANT_CODE ?? "acme-sports";
const BRANCH = "22222222-2222-2222-2222-222222222222";

async function login(page: Page, email: string) {
  await page.goto("/login");
  await page.fill("#email", email);
  await page.fill("#password", "Passw0rd!");
  await page.click('button[type="submit"]');
  await page.waitForURL("/dashboard");
}

async function token(request: APIRequestContext, email: string) {
  const r = await request.post(`${API}/auth/login`, { data: { merchant_code: MERCHANT, email, password: "Passw0rd!" } });
  return (await r.json()).access_token as string;
}

test("Phase 13: a shift close shows in cash reconciliation history, labelled with its till", async ({ page, request }) => {
  const admin = { Authorization: `Bearer ${await token(request, "arjun@acme-sports.test")}` };
  const cashier = { Authorization: `Bearer ${await token(request, "ravi@acme-sports.test")}` };

  const name = `E2E Shift Till ${Date.now()}`;
  const term = await (await request.post(`${API}/pos-terminals`, { headers: admin, data: { branch_id: BRANCH, name } })).json();
  try {
    await test.step("a shift run through the API appears in cash reconciliation history", async () => {
      const opened = await (
        await request.post(`${API}/pos-shifts`, {
          headers: cashier,
          data: { pos_terminal_id: term.terminal_id, opening_float: 1500, idempotency_key: `e2e-shift-${Date.now()}` },
        })
      ).json();
      expect(opened.status).toBe("open");
      const closed = await (
        await request.post(`${API}/pos-shifts/${opened.shift_id}/close`, {
          headers: cashier,
          data: { denominations: [{ denomination: 500, count: 3 }] },
        })
      ).json();
      expect(closed.report.close.variance).toBe("0.00");

      await login(page, "arjun@acme-sports.test");
      await page.goto("/cash-reconciliation");
      const row = page.getByRole("row", { name: new RegExp(`${name} · shift 1`) });
      await expect(row).toBeVisible();
      await expect(row).toContainText("₹1500.00");
    });
  } finally {
    await request.patch(`${API}/pos-terminals/${term.terminal_id}`, { headers: admin, data: { status: "inactive" } });
  }
});
