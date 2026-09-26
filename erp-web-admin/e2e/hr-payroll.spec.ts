import { test, expect } from "@playwright/test";

// End-to-end coverage for Phase 5: HR & Payroll (erp-core-go's
// phased_roadmap.md; migrations/023_hr_payroll.sql). Drives the real
// /hr/employees, /hr/shifts, /hr/statutory-config, and /payroll screens
// against the live backend — creates a real employee (the first
// user-creation path this codebase has ever had), gives it a salary
// structure, runs payroll, and finalizes it.
const API_BASE = "http://localhost:8080/api/v1";

test("HR & Payroll: create an employee, run payroll, and confirm permission gating", async ({ page, request }) => {
  const unique = Date.now() % 1000000;
  const employeeCode = `E2E${unique}`;
  // A payroll run is real, persistent, and permanent once finalized —
  // this same test finalizes the one it creates, so a hardcoded period
  // would collide with itself on every re-run (confirmed live: the
  // second run got 409 PAYROLL_RUN_FINALIZED and the UI never navigated
  // to the run, failing the payslip assertion). Deriving a period far
  // outside any real business's date range from `unique` keeps every
  // run on its own never-before-used period.
  const testMonth = (unique % 12) + 1;
  const testYear = 2100 + (unique % 500);
  const testPeriodLabel = `${testMonth}/${testYear}`;

  await test.step("login as Merchant Admin", async () => {
    await page.goto("/login");
    await page.fill("#email", "arjun@acme-sports.test");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");
  });

  await test.step("create a shift", async () => {
    await page.goto("/hr/shifts");
    await page.fill("#name", `E2E Shift ${unique}`);
    await page.fill("#start", "09:00");
    await page.fill("#end", "18:00");
    await page.getByRole("button", { name: "Add shift" }).click();
    await expect(page.getByText(`E2E Shift ${unique}`)).toBeVisible();
  });

  await test.step("create an employee", async () => {
    await page.goto("/hr/employees");
    await page.fill("#name", `E2E Employee ${unique}`);
    await page.fill("#employeeCode", employeeCode);
    await page.fill("#doj", "2024-01-01");
    await page.getByRole("button", { name: "Add employee" }).click();
    await expect(page.getByText(`E2E Employee ${unique}`)).toBeVisible();
  });

  await test.step("open the employee and add a salary structure", async () => {
    await page.click(`text=E2E Employee ${unique}`);
    await expect(page.getByText("Salary structure history")).toBeVisible();
    await page.fill("#newEffectiveFrom", "2024-01-01");
    await page.fill("#newBasic", "20000");
    await page.fill("#newHRA", "8000");
    await page.fill("#newSpecial", "2000");
    await page.getByRole("button", { name: "Save salary structure" }).click();
    await expect(page.getByText("20000.00")).toBeVisible();
  });

  await test.step("run payroll for a period only this test uses and see the new employee's payslip", async () => {
    await page.goto("/payroll");
    await page.fill("#month", String(testMonth));
    await page.fill("#year", String(testYear));
    await page.getByRole("button", { name: "Run / recompute payroll" }).click();
    const payslipRow = page.locator("tr", { hasText: `E2E Employee ${unique}` });
    await expect(payslipRow).toBeVisible();
    await expect(payslipRow.getByText("28000.00")).toBeVisible(); // net pay: 30000 gross - 1800 PF - 200 PT
  });

  await test.step("finalize the run and view the challan summary", async () => {
    await page.locator("tr", { hasText: testPeriodLabel }).getByRole("button", { name: "Finalize" }).click();
    await expect(page.getByText("finalized").first()).toBeVisible();
    await page.getByRole("button", { name: "Challan summary" }).click();
    await expect(page.getByText(/PF payable/)).toBeVisible();
  });

  await test.step("a POS User (no hr.manage/payroll.manage) is denied both screens", async () => {
    await page.goto("/dashboard");
    await page.click("text=Log out");
    await page.waitForURL("/login");
    await page.fill("#email", "ravi@acme-sports.test");
    await page.fill("#password", "Passw0rd!");
    await page.click('button[type="submit"]');
    await page.waitForURL("/dashboard");

    await page.goto("/hr/employees");
    await expect(page.getByText(/hr\.manage/)).toBeVisible();

    await page.goto("/payroll");
    await expect(page.getByText(/payroll\.manage/)).toBeVisible();
  });

  await test.step("cleanup: mark the test employee exited so it drops out of future active-employee listings", async () => {
    const login = await request.post(`${API_BASE}/auth/login`, {
      data: { merchant_code: "acme-sports", email: "arjun@acme-sports.test", password: "Passw0rd!" },
    });
    const { access_token } = await login.json();
    const headers = { Authorization: `Bearer ${access_token}` };
    const list = await request.get(`${API_BASE}/hr/employees`, { headers });
    const { employees } = await list.json();
    const created = employees.find((e: { employee_code: string }) => e.employee_code === employeeCode);
    expect(created).toBeTruthy();
    await request.patch(`${API_BASE}/hr/employees/${created.id}`, {
      headers,
      data: { date_of_exit: new Date().toISOString().slice(0, 10) },
    });
  });
});
