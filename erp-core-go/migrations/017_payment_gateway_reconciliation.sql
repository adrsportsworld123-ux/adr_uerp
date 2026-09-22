-- =====================================================================
-- Phase 4, sub-area 4: Reconciliation & Audit — Payment Gateway
-- reconciliation (phased_roadmap.md Phase 4; pos_frd_complete.md §16's
-- "2. Payment Gateway: Match UPI/Card with bank settlements (identify
-- missing/duplicate)")
--
-- Scope: this is #2 of the FRD's four reconciliation types — #1 (Cash)
-- closed in migrations/016, #4 (Inter-branch Transfer) closed in Phase 2.
-- #3 (Inventory) remains the last item in this sub-area.
--
-- Unlike bank reconciliation (migrations/015), which only LINKS an
-- already-posted journal_lines row to an imported statement line, a
-- payment gateway settlement is itself a real accounting event that
-- hasn't been posted yet: a card/UPI sale posts its full collected
-- amount to a clearing account (Card Clearing "1003" / UPI Clearing
-- "1004") at checkout, and that money only actually reaches the bank —
-- usually net of the gateway's fee — when the gateway's settlement
-- report says so. So matching a settlement line to a `payments` row
-- here posts Dr Bank (settlement amount) + Dr Payment Gateway Fees (the
-- shortfall, if any) / Cr the clearing account (the full original
-- payment amount) — zeroing that payment's slice of the clearing
-- account exactly once. Unmatching posts the exact mirror image (same
-- treatment sales.Void already gives a voided sale's original entry —
-- see internal/sales/void.go), not a delete, so the audit trail stays
-- immutable either way.
--
-- Matching key is `payments.reference_no` (already exists,
-- migrations/001_schema.sql — "gateway/transaction reference") against
-- the settlement line's own reference (the gateway's UTR/transaction
-- id) — not amount/date the way bank reconciliation matches, since a
-- gateway reference is unique per transaction and is exactly what a
-- real settlement report keys off of.
-- =====================================================================

CREATE TABLE payment_gateway_settlement_imports (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    gateway         TEXT NOT NULL,   -- free text: "Razorpay", "PayU", etc. — no vendor integration, just a label on the import
    filename        TEXT,
    line_count      INT NOT NULL DEFAULT 0,
    matched_count   INT NOT NULL DEFAULT 0,
    imported_by     UUID NOT NULL REFERENCES users(id),
    imported_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE payment_gateway_settlement_lines (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    import_id           UUID NOT NULL REFERENCES payment_gateway_settlement_imports(id) ON DELETE CASCADE,
    settlement_date     DATE NOT NULL,
    reference           TEXT NOT NULL,             -- gateway UTR/transaction id, matched against payments.reference_no
    amount              NUMERIC(14,2) NOT NULL,    -- net amount actually settled to bank (after the gateway's own fee)
    status              TEXT NOT NULL DEFAULT 'unmatched' CHECK (status IN ('unmatched', 'matched', 'duplicate')),
    matched_payment_id  UUID REFERENCES payments(id),
    matched_at          TIMESTAMPTZ,
    matched_by          UUID REFERENCES users(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_pg_settlement_lines_import ON payment_gateway_settlement_lines(import_id);
CREATE INDEX idx_pg_settlement_lines_status ON payment_gateway_settlement_lines(merchant_id, status);
CREATE INDEX idx_pg_settlement_lines_reference ON payment_gateway_settlement_lines(merchant_id, reference);

-- A single payment should never be claimed by two different settlement
-- lines — same data-integrity invariant bank reconciliation's own
-- unique index already enforces for matched_journal_line_id, here
-- against double-counting a single card/UPI collection as settled
-- twice.
CREATE UNIQUE INDEX idx_pg_settlement_lines_unique_match
    ON payment_gateway_settlement_lines(matched_payment_id) WHERE matched_payment_id IS NOT NULL;

ALTER TABLE payment_gateway_settlement_imports ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payment_gateway_settlement_imports USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE payment_gateway_settlement_lines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payment_gateway_settlement_lines USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON payment_gateway_settlement_imports, payment_gateway_settlement_lines TO erp_app;

-- journal_entries.source_type needs widening again — same recurring gap
-- this codebase's own CHECK-constraint-as-enum shape produces every time
-- a new kind of journal-posting event is introduced (migrations/014,
-- /016 both hit this before). Two new values: the settlement match
-- itself, and its unmatch/reversal mirror image.
ALTER TABLE journal_entries DROP CONSTRAINT journal_entries_source_type_check;
ALTER TABLE journal_entries ADD CONSTRAINT journal_entries_source_type_check
    CHECK (source_type IN ('sale', 'sale_void', 'purchase_bill', 'bill_payment', 'purchase_return',
                            'inventory_adjustment', 'manual', 'receivable_payment', 'cash_reconciliation',
                            'payment_gateway_settlement', 'payment_gateway_settlement_reversal'));

-- New ledger account: the gateway's own cut of a settled transaction
-- needs somewhere real to post to, distinct from Inventory Shrinkage/
-- Cash Over/Short — this is a genuine cost of accepting card/UPI
-- payments, not a variance or a loss event.
INSERT INTO chart_of_accounts (merchant_id, code, name, account_type, is_system) VALUES
    ('11111111-1111-1111-1111-111111111111', '5003', 'Payment Gateway Fees', 'expense', true);

-- New permission: importing a settlement file and matching/unmatching it
-- against collected payments is an accounts-team action with real
-- financial-integrity implications, same reasoning as
-- bank_reconciliation.manage/credit.manage/pricing.manage.
INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-00000000000b', 'payment_gateway_reconciliation.manage', 'Import payment gateway settlements and match/unmatch them against collected payments')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.merchant_id = '11111111-1111-1111-1111-111111111111'
  AND r.name IN ('Branch Manager', 'Merchant Admin')
  AND p.code = 'payment_gateway_reconciliation.manage'
ON CONFLICT DO NOTHING;
