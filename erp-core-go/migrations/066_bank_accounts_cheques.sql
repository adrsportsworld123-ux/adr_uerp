-- =====================================================================
-- Bank accounts (merchant and branch level), cheque books, issued and
-- received cheques.
--
-- bank_accounts: branch_id NULL = a merchant-level account; otherwise the
-- branch's own. Each posts to a ledger account: the first one created
-- uses the existing Bank account (1002), so every balance, bank
-- reconciliation and Cash Book line until now stays where it is; further
-- accounts get their own sub-account (1002-02, 1002-03 …, parent 1002).
-- A payment that names no bank account still posts to 1002, as before.
--
-- cheque_books: a leaf series (series_from..series_to) of one bank
-- account. issued_cheques: a cheque we write (a supplier bill payment,
-- or any other payee), one per leaf number and account; a cancelled
-- (spoilt) leaf is recorded the same way with status 'cancelled'.
--   issued → cleared | bounced (the bill payment it paid is reversed and
--   the bill is owed again) | cancelled (before it was presented).
-- received_cheques: a cheque a customer gives us, post-dated or not.
--   received → deposited (into one of our bank accounts; when it names a
--   credit invoice, the receipt is recorded against it then) → cleared |
--   bounced (the receipt is reversed and the invoice is owed again).
--   A received cheque can be returned to the customer before deposit.
--
-- bank_statement_imports.bank_account_id: which account a statement is
-- for, so it reconciles against that account's ledger (NULL = 1002).
-- =====================================================================

CREATE TABLE bank_accounts (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id       UUID NOT NULL REFERENCES merchants(id),
    branch_id         UUID REFERENCES branches(id),     -- NULL = merchant level
    account_name      TEXT NOT NULL,                     -- the name on the account
    bank_name         TEXT NOT NULL,
    bank_branch       TEXT,
    account_number    TEXT NOT NULL,
    ifsc              TEXT,
    account_type      TEXT NOT NULL DEFAULT 'current'
                          CHECK (account_type IN ('current', 'savings', 'overdraft', 'cash_credit')),
    ledger_account_id UUID NOT NULL REFERENCES chart_of_accounts(id),
    is_default        BOOLEAN NOT NULL DEFAULT false,   -- the one a payment picks first, per scope
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    -- Printing calibration for this bank's cheque leaf: {"offset_x_mm",
    -- "offset_y_mm"} shift the whole layout; empty = the CTS-2010 layout.
    cheque_layout     JSONB NOT NULL DEFAULT '{}',
    notes             TEXT,
    created_by        UUID REFERENCES users(id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX bank_accounts_number_uq ON bank_accounts (merchant_id, account_number, COALESCE(ifsc, ''));
CREATE UNIQUE INDEX bank_accounts_default_uq ON bank_accounts (merchant_id, COALESCE(branch_id, '00000000-0000-0000-0000-000000000000'::uuid))
    WHERE is_default AND status = 'active';

CREATE TABLE cheque_books (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    bank_account_id UUID NOT NULL REFERENCES bank_accounts(id),
    series_from     BIGINT NOT NULL CHECK (series_from > 0),
    series_to       BIGINT NOT NULL,
    leaf_digits     SMALLINT NOT NULL DEFAULT 6 CHECK (leaf_digits BETWEEN 4 AND 10),
    received_on     DATE,
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'closed')),
    notes           TEXT,
    created_by      UUID REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (series_to >= series_from AND series_to - series_from < 1000)
);

CREATE TABLE issued_cheques (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id      UUID NOT NULL REFERENCES merchants(id),
    branch_id        UUID REFERENCES branches(id),
    bank_account_id  UUID NOT NULL REFERENCES bank_accounts(id),
    cheque_book_id   UUID REFERENCES cheque_books(id),
    cheque_number    TEXT NOT NULL,
    cheque_date      DATE NOT NULL,
    payee_name       TEXT NOT NULL DEFAULT '',
    amount           NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (amount >= 0),
    account_payee    BOOLEAN NOT NULL DEFAULT true,     -- "A/c Payee" crossing on the print
    party_type       TEXT CHECK (party_type IN ('supplier', 'employee')),
    party_id         UUID,
    purchase_bill_id UUID REFERENCES purchase_bills(id),
    bill_payment_id  UUID REFERENCES purchase_bill_payments(id),
    memo             TEXT,
    status           TEXT NOT NULL DEFAULT 'issued' CHECK (status IN ('issued', 'cleared', 'bounced', 'cancelled')),
    cleared_on       DATE,
    bounced_on       DATE,
    status_reason    TEXT,
    print_count      INT NOT NULL DEFAULT 0,
    last_printed_at  TIMESTAMPTZ,
    created_by       UUID REFERENCES users(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (bank_account_id, cheque_number)
);
CREATE INDEX issued_cheques_status ON issued_cheques (merchant_id, status, cheque_date);

CREATE TABLE received_cheques (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id             UUID NOT NULL REFERENCES merchants(id),
    branch_id               UUID NOT NULL REFERENCES branches(id),
    customer_id             UUID REFERENCES customers(id),
    sales_order_id          UUID REFERENCES sales_orders(id),  -- the credit invoice it pays, if any
    drawer_name             TEXT NOT NULL,
    drawer_bank             TEXT,
    drawer_branch           TEXT,
    cheque_number           TEXT NOT NULL,
    cheque_date             DATE NOT NULL,
    amount                  NUMERIC(14,2) NOT NULL CHECK (amount > 0),
    status                  TEXT NOT NULL DEFAULT 'received'
                                CHECK (status IN ('received', 'deposited', 'cleared', 'bounced', 'returned')),
    deposit_bank_account_id UUID REFERENCES bank_accounts(id),
    deposited_on            DATE,
    receivable_payment_id   UUID,
    cleared_on              DATE,
    bounced_on              DATE,
    status_reason           TEXT,
    notes                   TEXT,
    created_by              UUID REFERENCES users(id),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX received_cheques_status ON received_cheques (merchant_id, status, cheque_date);

ALTER TABLE bank_accounts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON bank_accounts USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE cheque_books ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON cheque_books USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE issued_cheques ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON issued_cheques USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE received_cheques ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON received_cheques USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON bank_accounts, cheque_books, issued_cheques, received_cheques TO erp_app;

-- Which of our accounts a payment went through (NULL = the Bank account
-- 1002, as before), and a payment a bounced cheque reversed.
ALTER TABLE purchase_bill_payments ADD COLUMN bank_account_id UUID REFERENCES bank_accounts(id);
ALTER TABLE purchase_bill_payments ADD COLUMN reversed_at TIMESTAMPTZ;
ALTER TABLE customer_receivable_payments ADD COLUMN bank_account_id UUID REFERENCES bank_accounts(id);
ALTER TABLE customer_receivable_payments ADD COLUMN reversed_at TIMESTAMPTZ;

ALTER TABLE bank_statement_imports ADD COLUMN bank_account_id UUID REFERENCES bank_accounts(id);

ALTER TABLE journal_entries DROP CONSTRAINT journal_entries_source_type_check;
ALTER TABLE journal_entries ADD CONSTRAINT journal_entries_source_type_check
    CHECK (source_type IN ('sale', 'sale_void', 'purchase_bill', 'bill_payment', 'purchase_return',
                           'inventory_adjustment', 'manual', 'receivable_payment', 'cash_reconciliation',
                           'payment_gateway_settlement', 'payment_gateway_settlement_reversal',
                           'inventory_reconciliation', 'sales_return', 'cash_movement', 'payment_correction',
                           'manual_reversal', 'cheque_bounce'));

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000066', 'bank.manage',
     'Add and edit bank accounts (merchant and branch level) and their cheque books'),
    ('f0000000-0000-0000-0000-000000000067', 'cheques.manage',
     'Issue, print, cancel, clear and bounce cheques, and record, deposit and bounce received cheques')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'Merchant Admin' AND p.code IN ('bank.manage', 'cheques.manage')
ON CONFLICT DO NOTHING;
