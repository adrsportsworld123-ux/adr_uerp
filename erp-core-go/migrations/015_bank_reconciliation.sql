-- =====================================================================
-- Phase 4, sub-area 3: Bank Reconciliation
-- (phased_roadmap.md Phase 4; pos_frd_complete.md §8's "Bank
-- Reconciliation: Hybrid — Auto-import statements (CSV/Excel/PDF),
-- auto-match by amount/date/reference, manual matching for unmatched,
-- reconciliation reports")
--
-- Scope: CSV only (Go's stdlib encoding/csv, no new dependency — matching
-- this codebase's general dependency-light posture) — Excel/PDF parsing
-- both need a real third-party library this environment can't vet the
-- way it can a stdlib package; documented as a narrowing, not a silent
-- gap, same treatment as every other FRD line item this codebase has
-- ever trimmed.
--
-- One shared Bank ledger account per merchant, not one row per real bank
-- account — this mirrors the existing model exactly (Cash/Bank/clearing
-- accounts have been single lump accounts since Phase 2's
-- migrations/007_accounting.sql; a multi-bank-account merchant would
-- need that modeled first, a bigger change than this sub-area's scope).
-- Every bank_statement_line reconciles against journal_lines rows tagged
-- to the AccountBank ("1002") account — never a new parallel ledger.
-- =====================================================================

CREATE TABLE bank_statement_imports (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    filename        TEXT,
    line_count      INT NOT NULL DEFAULT 0,
    matched_count   INT NOT NULL DEFAULT 0,
    imported_by     UUID NOT NULL REFERENCES users(id),
    imported_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE bank_statement_lines (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id             UUID NOT NULL REFERENCES merchants(id),
    import_id               UUID NOT NULL REFERENCES bank_statement_imports(id) ON DELETE CASCADE,
    txn_date                DATE NOT NULL,
    description             TEXT,
    reference               TEXT,
    -- Signed: positive = deposit (money in), negative = withdrawal
    -- (money out) — the common single-column CSV export convention (as
    -- opposed to separate debit/credit columns), documented at the
    -- import endpoint.
    amount                  NUMERIC(14,2) NOT NULL,
    status                  TEXT NOT NULL DEFAULT 'unmatched' CHECK (status IN ('unmatched', 'matched', 'ignored')),
    matched_journal_line_id UUID REFERENCES journal_lines(id),
    matched_at              TIMESTAMPTZ,
    matched_by              UUID REFERENCES users(id),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_bank_statement_lines_import ON bank_statement_lines(import_id);
CREATE INDEX idx_bank_statement_lines_status ON bank_statement_lines(merchant_id, status);

-- A journal line should never be claimed by two different statement
-- lines — enforced here, not just in application code, since this is a
-- real data-integrity invariant (double-counting a single bank movement
-- across two statement rows would silently corrupt the reconciliation
-- report's totals).
CREATE UNIQUE INDEX idx_bank_statement_lines_unique_match
    ON bank_statement_lines(matched_journal_line_id) WHERE matched_journal_line_id IS NOT NULL;

ALTER TABLE bank_statement_imports ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON bank_statement_imports USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE bank_statement_lines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON bank_statement_lines USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON bank_statement_imports, bank_statement_lines TO erp_app;

-- New permission: importing a bank statement and matching it against the
-- ledger is an accounts-team action with real financial-integrity
-- implications — same reasoning as pricing.manage/credit.manage.
INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-00000000000a', 'bank_reconciliation.manage', 'Import bank statements and match/unmatch them against the ledger')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.merchant_id = '11111111-1111-1111-1111-111111111111'
  AND r.name IN ('Branch Manager', 'Merchant Admin')
  AND p.code = 'bank_reconciliation.manage'
ON CONFLICT DO NOTHING;
