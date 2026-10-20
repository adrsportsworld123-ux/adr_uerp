-- =====================================================================
-- Employee loans and salary advances; employees and bank accounts as
-- journal parties.
--
-- employee_loans: a loan or salary advance to one employee (users.id),
-- with an optional flat annual interest rate, repaid in monthly
-- instalments from start_month/start_year. Disbursing posts
--   Dr 1150 Employee Loans & Advances (party: the employee)
--   Cr Cash / Bank (or the chosen bank account's ledger).
-- employee_loan_installments: the schedule. A payroll run deducts every
-- instalment due by its month (payslips.loan_deduction); finalizing the
-- run marks them recovered and posts
--   Dr 2150 Salaries Payable   Cr 1150 (principal)   Cr 4050 Interest Income.
-- A manual repayment (cash, bank, cheque) settles the oldest instalments
-- first; foreclosure settles all that is left. Payroll itself doesn't post
-- to the ledger yet, so a merchant who books salaries by voucher books
-- them to Salaries Payable (2150) for these to net off.
--
-- journal_lines / journal_voucher_lines.party_type gains 'employee'
-- (users.id) and 'bank_account' (bank_accounts.id), so a voucher line and
-- the party ledger can name them.
-- =====================================================================

CREATE TABLE employee_loans (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    loan_number         TEXT NOT NULL,
    user_id             UUID NOT NULL REFERENCES users(id),
    branch_id           UUID REFERENCES branches(id),
    loan_type           TEXT NOT NULL DEFAULT 'loan' CHECK (loan_type IN ('loan', 'advance')),
    purpose             TEXT,
    principal           NUMERIC(14,2) NOT NULL CHECK (principal > 0),
    interest_rate       NUMERIC(6,3) NOT NULL DEFAULT 0 CHECK (interest_rate >= 0 AND interest_rate <= 60),  -- flat, % a year
    tenure_months       SMALLINT NOT NULL CHECK (tenure_months BETWEEN 1 AND 120),
    total_interest      NUMERIC(14,2) NOT NULL DEFAULT 0,
    emi_amount          NUMERIC(14,2) NOT NULL,
    start_month         SMALLINT NOT NULL CHECK (start_month BETWEEN 1 AND 12),
    start_year          SMALLINT NOT NULL,
    disbursed_on        DATE NOT NULL,
    disbursement_method TEXT NOT NULL CHECK (disbursement_method IN ('cash', 'bank_transfer', 'cheque')),
    bank_account_id     UUID REFERENCES bank_accounts(id),
    issued_cheque_id    UUID REFERENCES issued_cheques(id),
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'closed', 'foreclosed')),
    closed_on           DATE,
    approved_by         UUID REFERENCES users(id),
    created_by          UUID REFERENCES users(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, loan_number)
);
CREATE INDEX employee_loans_user ON employee_loans (merchant_id, user_id, status);

CREATE TABLE employee_loan_installments (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    loan_id          UUID NOT NULL REFERENCES employee_loans(id) ON DELETE CASCADE,
    seq              SMALLINT NOT NULL,
    due_month        SMALLINT NOT NULL CHECK (due_month BETWEEN 1 AND 12),
    due_year         SMALLINT NOT NULL,
    principal_part   NUMERIC(14,2) NOT NULL,
    interest_part    NUMERIC(14,2) NOT NULL DEFAULT 0,
    amount           NUMERIC(14,2) NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'recovered')),
    recovered_on     DATE,
    recovered_via    TEXT CHECK (recovered_via IN ('payroll', 'cash', 'bank_transfer', 'cheque')),
    payslip_id       UUID REFERENCES payslips(id),
    repayment_id     UUID,
    UNIQUE (loan_id, seq)
);

CREATE TABLE employee_loan_repayments (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id      UUID NOT NULL REFERENCES merchants(id),
    loan_id          UUID NOT NULL REFERENCES employee_loans(id),
    amount           NUMERIC(14,2) NOT NULL CHECK (amount > 0),
    principal_part   NUMERIC(14,2) NOT NULL,
    interest_part    NUMERIC(14,2) NOT NULL DEFAULT 0,
    method           TEXT NOT NULL CHECK (method IN ('payroll', 'cash', 'bank_transfer', 'cheque')),
    bank_account_id  UUID REFERENCES bank_accounts(id),
    reference_no     TEXT,
    paid_on          DATE NOT NULL,
    payroll_run_id   UUID REFERENCES payroll_runs(id),
    foreclosure      BOOLEAN NOT NULL DEFAULT false,
    created_by       UUID REFERENCES users(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE employee_loans ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON employee_loans USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE employee_loan_installments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON employee_loan_installments USING (loan_id IN (SELECT id FROM employee_loans));
ALTER TABLE employee_loan_repayments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON employee_loan_repayments USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON employee_loans, employee_loan_installments, employee_loan_repayments TO erp_app;

-- The loan deduction on a payslip (0 for everyone without a loan: their
-- net pay is what it always was).
ALTER TABLE payslips ADD COLUMN loan_deduction NUMERIC(14,2) NOT NULL DEFAULT 0;

-- The three accounts the loan postings use, for every merchant (and in
-- internal/subscriptions' systemAccounts for new ones).
INSERT INTO chart_of_accounts (merchant_id, code, name, account_type, is_system)
SELECT DISTINCT c.merchant_id, v.code, v.name, v.account_type, true
FROM chart_of_accounts c
CROSS JOIN (VALUES ('1150', 'Employee Loans & Advances', 'asset'),
                   ('2150', 'Salaries Payable', 'liability'),
                   ('4050', 'Interest Income', 'income')) AS v(code, name, account_type)
WHERE c.code = '1001'
ON CONFLICT (merchant_id, code) DO NOTHING;

ALTER TABLE journal_entries DROP CONSTRAINT journal_entries_source_type_check;
ALTER TABLE journal_entries ADD CONSTRAINT journal_entries_source_type_check
    CHECK (source_type IN ('sale', 'sale_void', 'purchase_bill', 'bill_payment', 'purchase_return',
                           'inventory_adjustment', 'manual', 'receivable_payment', 'cash_reconciliation',
                           'payment_gateway_settlement', 'payment_gateway_settlement_reversal',
                           'inventory_reconciliation', 'sales_return', 'cash_movement', 'payment_correction',
                           'manual_reversal', 'cheque_bounce', 'loan_disbursement', 'loan_repayment'));

ALTER TABLE journal_lines DROP CONSTRAINT journal_lines_party_type_check;
ALTER TABLE journal_lines ADD CONSTRAINT journal_lines_party_type_check
    CHECK (party_type IN ('customer', 'supplier', 'employee', 'bank_account'));
ALTER TABLE journal_voucher_lines DROP CONSTRAINT journal_voucher_lines_party_type_check;
ALTER TABLE journal_voucher_lines ADD CONSTRAINT journal_voucher_lines_party_type_check
    CHECK (party_type IN ('customer', 'supplier', 'employee', 'bank_account'));

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000068', 'loans.manage',
     'Give employee loans and salary advances, record repayments and foreclose them')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'Merchant Admin' AND p.code = 'loans.manage'
ON CONFLICT DO NOTHING;
