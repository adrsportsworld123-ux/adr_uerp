-- =====================================================================
-- Phase 2, sub-area 2: Ledger & Accounting
-- (phased_roadmap.md Phase 2; pos_frd_complete.md §8)
--
-- Scope, matching the roadmap's explicit "In" list for this phase: chart
-- of accounts, double-entry auto-journaling from every sale/purchase,
-- party ledgers (customer/supplier), Day Book/Cash Book. NOT built here
-- (FRD §8 covers them, but the roadmap doesn't list them for Phase 2):
-- payment-terms/aging/credit-hold, bank reconciliation, multi-currency,
-- and manual-journal-entry approval (manual entries post immediately here
-- — the same "skip the formal workflow for now" simplification already
-- applied to Purchase Management's PO step).
--
-- Account codes follow the FRD's standard numbering (Assets 1xxx,
-- Liabilities 2xxx, Equity 3xxx, Income 4xxx, Expenses 5xxx) and are
-- looked up BY CODE from Go, not by a hardcoded UUID, so the seed data
-- below is the only place these specific codes need to exist.
-- =====================================================================

CREATE TABLE chart_of_accounts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    parent_id       UUID REFERENCES chart_of_accounts(id),
    code            TEXT NOT NULL,
    name            TEXT NOT NULL,
    account_type    TEXT NOT NULL CHECK (account_type IN ('asset','liability','equity','income','expense')),
    is_system       BOOLEAN NOT NULL DEFAULT false,  -- system accounts auto-journaling posts against; not user-deletable
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, code)
);

CREATE TABLE journal_entries (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    branch_id       UUID REFERENCES branches(id),
    entry_number    TEXT NOT NULL,
    entry_date      DATE NOT NULL DEFAULT CURRENT_DATE,
    source_type     TEXT NOT NULL CHECK (source_type IN ('sale','sale_void','purchase_bill','bill_payment','purchase_return','inventory_adjustment','manual')),
    source_id       UUID,                     -- e.g. sales_orders.id, purchase_bills.id — not a typed FK since it points at different tables depending on source_type
    description     TEXT,
    status          TEXT NOT NULL DEFAULT 'posted' CHECK (status IN ('posted','void')),
    created_by      UUID REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, entry_number)
);

CREATE TABLE journal_lines (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    journal_entry_id    UUID NOT NULL REFERENCES journal_entries(id) ON DELETE CASCADE,
    account_id          UUID NOT NULL REFERENCES chart_of_accounts(id),
    debit               NUMERIC(14,2) NOT NULL DEFAULT 0,
    credit              NUMERIC(14,2) NOT NULL DEFAULT 0,
    party_type          TEXT CHECK (party_type IN ('customer','supplier')),
    party_id            UUID,   -- customers.id or suppliers.id depending on party_type — no single FK spans both tables, validated at the application layer
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((debit = 0) OR (credit = 0)),   -- a line is a debit or a credit, never both
    CHECK (debit >= 0 AND credit >= 0)
);

CREATE INDEX idx_journal_entries_branch_date  ON journal_entries(branch_id, entry_date);
CREATE INDEX idx_journal_entries_source       ON journal_entries(source_type, source_id);
CREATE INDEX idx_journal_lines_entry          ON journal_lines(journal_entry_id);
CREATE INDEX idx_journal_lines_account        ON journal_lines(account_id);
CREATE INDEX idx_journal_lines_party          ON journal_lines(party_type, party_id);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOR t IN SELECT unnest(ARRAY['chart_of_accounts','journal_entries'])
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY;', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I USING (merchant_id = current_setting(''app.tenant_id'', true)::uuid);',
            t
        );
    END LOOP;
END $$;

-- journal_lines has no direct merchant_id — same subquery-through-parent
-- pattern as sales_order_lines/goods_receipt_lines.
ALTER TABLE journal_lines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON journal_lines
    USING (journal_entry_id IN (SELECT id FROM journal_entries));

GRANT SELECT, INSERT, UPDATE, DELETE ON chart_of_accounts, journal_entries, journal_lines TO erp_app;

-- =====================================================================
-- Default chart of accounts for the seeded acme-sports merchant. A real
-- merchant-onboarding flow (still hypothetical — see migrations/005's
-- header comment for the same caveat about role_permissions) needs to
-- seed the same set for every new merchant; this is the pattern to copy,
-- not a one-time fix.
-- =====================================================================

INSERT INTO chart_of_accounts (merchant_id, code, name, account_type, is_system) VALUES
    ('11111111-1111-1111-1111-111111111111', '1001', 'Cash',                    'asset',     true),
    ('11111111-1111-1111-1111-111111111111', '1002', 'Bank',                    'asset',     true),
    ('11111111-1111-1111-1111-111111111111', '1003', 'Card Clearing',           'asset',     true),
    ('11111111-1111-1111-1111-111111111111', '1004', 'UPI Clearing',            'asset',     true),
    ('11111111-1111-1111-1111-111111111111', '1005', 'GST Input Credit',        'asset',     true),
    ('11111111-1111-1111-1111-111111111111', '1100', 'Accounts Receivable',     'asset',     true),
    ('11111111-1111-1111-1111-111111111111', '1200', 'Inventory Asset',         'asset',     true),
    ('11111111-1111-1111-1111-111111111111', '2001', 'GST Output Payable',      'liability', true),
    ('11111111-1111-1111-1111-111111111111', '2002', 'Accounts Payable',        'liability', true),
    ('11111111-1111-1111-1111-111111111111', '4001', 'Sales Revenue',           'income',    true),
    ('11111111-1111-1111-1111-111111111111', '5001', 'Inventory Shrinkage',     'expense',   true);
