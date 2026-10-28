-- =====================================================================
-- Expenses (with an import for the ones never recorded) and partners'
-- capital (phase0_1_design.md §3.77).
--
-- Accounts, for every merchant (and in internal/subscriptions'
-- systemAccounts for new ones). An existing account already on one of
-- these codes is left alone.
--   1300 Deposits & Advances (asset)   — a shop advance / rent deposit
--   1400 Furniture & Fixtures (asset)  — interior work
--   2200 Expenses Payable (liability)  — an expense not yet paid
--   3000 Partners' Capital (equity)    — each partner gets 3000-NN under it
--   5100 Rent, 5110 Advertising & Promotion, 5120 Freight & Transport,
--   5130 Electricity, 5140 Repairs & Maintenance, 5150 Telephone & Internet,
--   5190 General Expenses (expense)
--
-- expense_heads: what an expense is for, each mapped to the account it
-- posts to (editable). Seeded per merchant: Shop Advance → 1300, Interior →
-- 1400, Shop Rent → 5100, Flex & Banner → 5110, Transport → 5120,
-- Electricity → 5130, Repairs → 5140, Phone & Internet → 5150, Other → 5190.
--
-- expenses: one expense, dated, for a head, paid by cash, bank (an
-- account), a partner (it adds to that partner's capital) or unpaid
-- (Expenses Payable until paid). Posting: Dr the head's account, Cr what
-- paid it. Cancelling posts the reverse; nothing is deleted.
--
-- partners: name, contact, profit-sharing ratio (%), joined/left, and
-- the capital sub-account. partner_transactions is the partner's side of
-- every entry on that account: pay_in (Dr cash/bank, Cr capital), pay_out
-- (Dr capital, Cr cash/bank), paid_expense and paid_bill (the partner paid
-- from their own money: Cr capital). The ledger stays the truth; this is
-- the statement's register.
--
-- A purchase order may be funded by one partner
-- (purchase_orders.funded_by_partner_id); a bill payment may be made by a
-- partner (purchase_bill_payments.method 'partner', partner_id): Dr
-- Accounts Payable, Cr that partner's capital.
--
-- journal source types expense / expense_payment / expense_cancel /
-- partner_pay_in / partner_pay_out; party_type 'partner'.
-- =====================================================================

INSERT INTO chart_of_accounts (merchant_id, code, name, account_type, is_system)
SELECT DISTINCT c.merchant_id, v.code, v.name, v.account_type, true
FROM chart_of_accounts c
CROSS JOIN (VALUES ('1300', 'Deposits & Advances', 'asset'),
                   ('1400', 'Furniture & Fixtures', 'asset'),
                   ('2200', 'Expenses Payable', 'liability'),
                   ('3000', 'Partners'' Capital', 'equity'),
                   ('5100', 'Rent', 'expense'),
                   ('5110', 'Advertising & Promotion', 'expense'),
                   ('5120', 'Freight & Transport', 'expense'),
                   ('5130', 'Electricity', 'expense'),
                   ('5140', 'Repairs & Maintenance', 'expense'),
                   ('5150', 'Telephone & Internet', 'expense'),
                   ('5190', 'General Expenses', 'expense')) AS v(code, name, account_type)
WHERE c.code = '1001'
ON CONFLICT (merchant_id, code) DO NOTHING;

CREATE TABLE expense_heads (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    name        TEXT NOT NULL,
    account_id  UUID NOT NULL REFERENCES chart_of_accounts(id),
    status      TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_expense_heads_name ON expense_heads (merchant_id, lower(name));
ALTER TABLE expense_heads ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON expense_heads USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON expense_heads TO erp_app;

INSERT INTO expense_heads (merchant_id, name, account_id)
SELECT a.merchant_id, v.name, a.id
FROM chart_of_accounts a
JOIN (VALUES ('Shop Advance', '1300'), ('Interior', '1400'), ('Shop Rent', '5100'), ('Flex & Banner', '5110'),
             ('Transport', '5120'), ('Electricity', '5130'), ('Repairs', '5140'), ('Phone & Internet', '5150'),
             ('Other', '5190')) AS v(name, code) ON a.code = v.code
ON CONFLICT DO NOTHING;

CREATE TABLE partners (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id  UUID NOT NULL REFERENCES merchants(id),
    name         TEXT NOT NULL,
    phone        TEXT,
    email        TEXT,
    pan          TEXT,
    share_ratio  NUMERIC(7,4) NOT NULL DEFAULT 0 CHECK (share_ratio >= 0 AND share_ratio <= 100),  -- % of profit
    account_id   UUID NOT NULL REFERENCES chart_of_accounts(id),                                 -- 3000-NN
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'retired')),
    joined_on    DATE,
    left_on      DATE,
    notes        TEXT,
    created_by   UUID REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, account_id)
);
CREATE UNIQUE INDEX uq_partners_name ON partners (merchant_id, lower(name));
ALTER TABLE partners ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON partners USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON partners TO erp_app;

CREATE TABLE expenses (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id      UUID NOT NULL REFERENCES merchants(id),
    expense_number   TEXT NOT NULL,
    branch_id        UUID NOT NULL REFERENCES branches(id),
    expense_date     DATE NOT NULL,
    head_id          UUID NOT NULL REFERENCES expense_heads(id),
    account_id       UUID NOT NULL REFERENCES chart_of_accounts(id),   -- the head's account when it was entered
    description      TEXT,
    vendor           TEXT,
    reference        TEXT,
    amount           NUMERIC(14,2) NOT NULL CHECK (amount > 0),
    paid_by          TEXT NOT NULL CHECK (paid_by IN ('cash', 'bank', 'partner', 'unpaid')),
    bank_account_id  UUID REFERENCES bank_accounts(id),
    partner_id       UUID REFERENCES partners(id),
    status           TEXT NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'cancelled')),
    paid_at          DATE,          -- an unpaid one, once paid
    paid_with        TEXT CHECK (paid_with IN ('cash', 'bank', 'partner')),
    paid_bank_account_id UUID REFERENCES bank_accounts(id),
    paid_partner_id  UUID REFERENCES partners(id),
    cancel_reason    TEXT,
    import_batch     TEXT,          -- the import that created it
    created_by       UUID REFERENCES users(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, expense_number),
    CHECK ((paid_by = 'partner') = (partner_id IS NOT NULL)),
    CHECK (paid_by = 'bank' OR bank_account_id IS NULL)
);
CREATE INDEX idx_expenses_date ON expenses (merchant_id, expense_date DESC);
ALTER TABLE expenses ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON expenses USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY branch_scope ON expenses AS RESTRICTIVE USING (app_branch_visible(branch_id)) WITH CHECK (app_branch_visible(branch_id));
GRANT SELECT, INSERT, UPDATE ON expenses TO erp_app;

CREATE TABLE partner_transactions (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id      UUID NOT NULL REFERENCES merchants(id),
    partner_id       UUID NOT NULL REFERENCES partners(id),
    txn_type         TEXT NOT NULL CHECK (txn_type IN ('pay_in', 'pay_out', 'paid_expense', 'paid_bill', 'reversal')),
    txn_date         DATE NOT NULL,
    amount           NUMERIC(14,2) NOT NULL,   -- signed for the capital: + adds to it, − takes from it
    method           TEXT CHECK (method IN ('cash', 'bank_transfer', 'cheque', 'own_funds')),
    bank_account_id  UUID REFERENCES bank_accounts(id),
    branch_id        UUID REFERENCES branches(id),
    reference        TEXT,
    notes            TEXT,
    expense_id       UUID REFERENCES expenses(id),
    bill_payment_id  UUID REFERENCES purchase_bill_payments(id),
    journal_entry_id UUID REFERENCES journal_entries(id),
    created_by       UUID REFERENCES users(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_partner_transactions_partner ON partner_transactions (partner_id, txn_date);
ALTER TABLE partner_transactions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON partner_transactions USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT ON partner_transactions TO erp_app;

ALTER TABLE purchase_orders ADD COLUMN funded_by_partner_id UUID REFERENCES partners(id);

ALTER TABLE purchase_bill_payments DROP CONSTRAINT purchase_bill_payments_method_check;
ALTER TABLE purchase_bill_payments ADD CONSTRAINT purchase_bill_payments_method_check
    CHECK (method IN ('cash', 'card', 'upi', 'bank_transfer', 'cheque', 'partner'));
ALTER TABLE purchase_bill_payments ADD COLUMN partner_id UUID REFERENCES partners(id);
ALTER TABLE purchase_bill_payments ADD CONSTRAINT purchase_bill_payments_partner_check
    CHECK ((method = 'partner') = (partner_id IS NOT NULL));

ALTER TABLE journal_entries DROP CONSTRAINT journal_entries_source_type_check;
ALTER TABLE journal_entries ADD CONSTRAINT journal_entries_source_type_check
    CHECK (source_type IN ('sale', 'sale_void', 'purchase_bill', 'bill_payment', 'purchase_return',
                           'inventory_adjustment', 'manual', 'receivable_payment', 'cash_reconciliation',
                           'payment_gateway_settlement', 'payment_gateway_settlement_reversal',
                           'inventory_reconciliation', 'sales_return', 'cash_movement', 'payment_correction',
                           'manual_reversal', 'cheque_bounce', 'loan_disbursement', 'loan_repayment',
                           'expense', 'expense_payment', 'expense_cancel', 'partner_pay_in', 'partner_pay_out'));

ALTER TABLE journal_lines DROP CONSTRAINT journal_lines_party_type_check;
ALTER TABLE journal_lines ADD CONSTRAINT journal_lines_party_type_check
    CHECK (party_type IN ('customer', 'supplier', 'employee', 'bank_account', 'partner'));
ALTER TABLE journal_voucher_lines DROP CONSTRAINT journal_voucher_lines_party_type_check;
ALTER TABLE journal_voucher_lines ADD CONSTRAINT journal_voucher_lines_party_type_check
    CHECK (party_type IN ('customer', 'supplier', 'employee', 'bank_account', 'partner'));

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000074', 'expenses.manage', 'Record, import, pay and cancel expenses, and edit expense heads'),
    ('f0000000-0000-0000-0000-000000000075', 'partners.manage', 'Add partners, set profit shares, and record partners'' pay-ins and pay-outs')
ON CONFLICT (code) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'Merchant Admin' AND p.code IN ('expenses.manage', 'partners.manage')
ON CONFLICT DO NOTHING;
