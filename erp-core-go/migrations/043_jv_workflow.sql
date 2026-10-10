-- =====================================================================
-- Phase 14: journal voucher (JV) workflow and period lock
-- (phased_roadmap.md Phase 14, decision 6: "draft → approve → post ...
-- a configurable merchant setting, with the default left as today's
-- behaviour").
--
-- Drafts live OUTSIDE the ledger. A journal_vouchers row is a manual entry
-- being prepared: draft → submitted → approved. Only posting writes a real
-- journal_entries row (source_type 'manual', source_id = the voucher), so
-- no report, balance or GST figure can ever see a draft: every one of them
-- reads journal_entries, unchanged.
--
--   jv_approval_required = false (default): a voucher posts as it's saved,
--     exactly what POST /accounting/journal-entries always did.
--   jv_approval_required = true: it's submitted, a different user with
--     accounting.jv_approve approves it (maker-checker), then it's posted.
--
-- Posted means final. A posted voucher is corrected by reversing it: a
-- mirror entry (source_type 'manual_reversal'), never an edit. And the
-- ledger itself is now append-only at the database level: journal_entries
-- and journal_lines refuse UPDATE of anything but an entry's status, and
-- refuse DELETE, whoever asks (the triggers below).
--
-- Period lock: accounting_settings.locked_through. Nothing posts with an
-- entry date on or before it (enforced in accounting.postJournalEntry,
-- the one function every posting goes through).
-- =====================================================================

CREATE TABLE accounting_settings (
    merchant_id          UUID PRIMARY KEY REFERENCES merchants(id),
    jv_approval_required BOOLEAN NOT NULL DEFAULT false,
    locked_through       DATE,
    updated_by           UUID REFERENCES users(id),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE accounting_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON accounting_settings USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON accounting_settings TO erp_app;

CREATE TABLE journal_vouchers (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id       UUID NOT NULL REFERENCES merchants(id),
    voucher_number    TEXT NOT NULL,
    branch_id         UUID REFERENCES branches(id),
    entry_date        DATE NOT NULL,
    description       TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'submitted', 'approved', 'posted', 'reversed')),
    created_by        UUID NOT NULL REFERENCES users(id),
    submitted_by      UUID REFERENCES users(id),
    submitted_at      TIMESTAMPTZ,
    approved_by       UUID REFERENCES users(id),
    approved_at       TIMESTAMPTZ,
    rejection_note    TEXT,                 -- the last "send back to draft" reason
    posted_by         UUID REFERENCES users(id),
    posted_at         TIMESTAMPTZ,
    journal_entry_id  UUID REFERENCES journal_entries(id),
    reversed_by       UUID REFERENCES users(id),
    reversed_at       TIMESTAMPTZ,
    reversal_reason   TEXT,
    reversal_entry_id UUID REFERENCES journal_entries(id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, voucher_number),
    CHECK ((status IN ('posted', 'reversed')) = (journal_entry_id IS NOT NULL)),
    CHECK ((status = 'reversed') = (reversal_entry_id IS NOT NULL))
);
CREATE INDEX idx_journal_vouchers_status ON journal_vouchers (merchant_id, status, created_at DESC);

CREATE TABLE journal_voucher_lines (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    voucher_id  UUID NOT NULL REFERENCES journal_vouchers(id) ON DELETE CASCADE,
    line_no     INTEGER NOT NULL,
    account_id  UUID NOT NULL REFERENCES chart_of_accounts(id),
    debit       NUMERIC(14,2) NOT NULL DEFAULT 0,
    credit      NUMERIC(14,2) NOT NULL DEFAULT 0,
    party_type  TEXT CHECK (party_type IN ('customer', 'supplier')),
    party_id    UUID,
    memo        TEXT,
    CHECK ((debit = 0) <> (credit = 0)),
    CHECK (debit >= 0 AND credit >= 0)
);
CREATE INDEX idx_journal_voucher_lines_voucher ON journal_voucher_lines (voucher_id, line_no);

ALTER TABLE journal_vouchers ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON journal_vouchers USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE journal_voucher_lines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON journal_voucher_lines USING (voucher_id IN (SELECT id FROM journal_vouchers));
GRANT SELECT, INSERT, UPDATE, DELETE ON journal_vouchers, journal_voucher_lines TO erp_app;

ALTER TABLE journal_entries DROP CONSTRAINT journal_entries_source_type_check;
ALTER TABLE journal_entries ADD CONSTRAINT journal_entries_source_type_check
    CHECK (source_type IN ('sale', 'sale_void', 'purchase_bill', 'bill_payment', 'purchase_return',
                           'inventory_adjustment', 'manual', 'receivable_payment', 'cash_reconciliation',
                           'payment_gateway_settlement', 'payment_gateway_settlement_reversal',
                           'inventory_reconciliation', 'sales_return', 'cash_movement', 'payment_correction',
                           'manual_reversal'));

-- The ledger is append-only. Nothing in the application updates or
-- deletes journal rows (corrections are new, reversing entries), and now
-- the database guarantees it. The one allowed change is an entry's status.
CREATE OR REPLACE FUNCTION journal_append_only() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'journal is append-only: % rows cannot be deleted; post a reversing entry', TG_TABLE_NAME
            USING ERRCODE = 'P0001', HINT = 'LEDGER_IMMUTABLE';
    END IF;
    -- Nested, not AND-ed: PL/pgSQL evaluates both sides, and a
    -- journal_lines row has no entry_number to compare.
    IF TG_TABLE_NAME = 'journal_entries' THEN
        IF (NEW.entry_number, NEW.entry_date, NEW.branch_id, NEW.source_type, NEW.source_id, NEW.description, NEW.merchant_id)
           IS NOT DISTINCT FROM
           (OLD.entry_number, OLD.entry_date, OLD.branch_id, OLD.source_type, OLD.source_id, OLD.description, OLD.merchant_id) THEN
            RETURN NEW; -- only status (or nothing) changed
        END IF;
    END IF;
    RAISE EXCEPTION 'journal is append-only: posted % cannot be edited; post a reversing entry', TG_TABLE_NAME
        USING ERRCODE = 'P0001', HINT = 'LEDGER_IMMUTABLE';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER journal_entries_append_only BEFORE UPDATE OR DELETE ON journal_entries
    FOR EACH ROW EXECUTE FUNCTION journal_append_only();
CREATE TRIGGER journal_lines_append_only BEFORE UPDATE OR DELETE ON journal_lines
    FOR EACH ROW EXECUTE FUNCTION journal_append_only();

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000043', 'accounting.jv_approve', 'Approve (or send back) journal vouchers others submitted'),
    ('f0000000-0000-0000-0000-000000000044', 'accounting.manage', 'Accounting settings: JV approval and the period lock')
ON CONFLICT (code) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'Merchant Admin' AND p.code IN ('accounting.jv_approve', 'accounting.manage')
ON CONFLICT DO NOTHING;
