-- =====================================================================
-- Phase 4, sub-area 4: Reconciliation & Audit — Cash reconciliation
-- (phased_roadmap.md Phase 4; pos_frd_complete.md §16's "Reconciliation
-- Types: 1. Cash — EOD counting vs system cash sales, detect
-- overages/shortages, denomination-wise")
--
-- Scope note: §16 names four reconciliation types. Inter-branch Transfer
-- (#4) was already closed in Phase 2 (branch_transfers' own
-- dispatch/complete-with-discrepancy flow, migrations/008_multi_branch.sql
-- — a received≠sent discrepancy already posts to Inventory Shrinkage).
-- This migration closes #1 (Cash); #2 (Payment Gateway) and #3
-- (Inventory) are the next items in this same sub-area, not yet built.
--
-- Lives alongside bank_reconciliation's own tables/package
-- (internal/accounting) — same "expected vs actual, then a gated
-- adjustment" shape, same accounts-team ownership.
-- =====================================================================

CREATE TABLE cash_reconciliations (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    branch_id           UUID NOT NULL REFERENCES branches(id),
    recon_date          DATE NOT NULL,
    opening_float       NUMERIC(14,2) NOT NULL DEFAULT 0,   -- cash placed in the drawer before the shift, entered at count time
    system_expected     NUMERIC(14,2) NOT NULL,             -- opening_float + that day's finalized cash sales, snapshotted at count time
    counted_total       NUMERIC(14,2) NOT NULL,             -- SUM(denomination * count) from cash_reconciliation_denominations
    variance            NUMERIC(14,2) NOT NULL,             -- counted_total - system_expected; positive = overage, negative = shortage
    reason              TEXT,                                -- required whenever variance != 0
    counted_by          UUID NOT NULL REFERENCES users(id),
    authorized_by       UUID REFERENCES users(id),           -- Branch Manager/Merchant Admin PIN-approval; NULL when variance = 0 (no approval needed)
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One reconciliation per branch per day, matching §16's own
    -- "Frequency: Configurable — Daily (cash)" default cadence.
    UNIQUE (branch_id, recon_date)
);

CREATE TABLE cash_reconciliation_denominations (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cash_reconciliation_id  UUID NOT NULL REFERENCES cash_reconciliations(id) ON DELETE CASCADE,
    denomination            NUMERIC(10,2) NOT NULL,   -- e.g. 2000, 500, 200, 100, 50, 20, 10, 5, 2, 1 — notes and coins both, no separate type needed
    count                   INT NOT NULL CHECK (count >= 0),
    subtotal                NUMERIC(14,2) NOT NULL    -- denomination * count, stored (not just derived) for a stable audit record independent of future rounding-rule changes
);

ALTER TABLE cash_reconciliations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON cash_reconciliations USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

-- cash_reconciliation_denominations has no merchant_id of its own — same
-- "RLS via a subquery to the tenant-scoped parent" pattern
-- 003_hardening.sql already established for sales_order_lines/payments.
ALTER TABLE cash_reconciliation_denominations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON cash_reconciliation_denominations
    USING (cash_reconciliation_id IN (SELECT id FROM cash_reconciliations));

GRANT SELECT, INSERT, UPDATE, DELETE ON cash_reconciliations, cash_reconciliation_denominations TO erp_app;

-- journal_entries.source_type needs widening again (same class of gap
-- migrations/014's own header comment already flagged and fixed for
-- 'receivable_payment' — this codebase's CHECK constraint literally lists
-- every valid source_type, so it needs one more value each time a new
-- kind of journal-posting event is introduced).
ALTER TABLE journal_entries DROP CONSTRAINT journal_entries_source_type_check;
ALTER TABLE journal_entries ADD CONSTRAINT journal_entries_source_type_check
    CHECK (source_type IN ('sale', 'sale_void', 'purchase_bill', 'bill_payment', 'purchase_return',
                            'inventory_adjustment', 'manual', 'receivable_payment', 'cash_reconciliation'));

-- New ledger account: a real retail cash-drawer variance needs somewhere
-- to post to. Nets both directions (overage credits it, shortage debits
-- it) — standard "Cash Over/Short" treatment, not a fabricated category.
INSERT INTO chart_of_accounts (merchant_id, code, name, account_type, is_system) VALUES
    ('11111111-1111-1111-1111-111111111111', '5002', 'Cash Over/Short', 'expense', true);

-- No new permissions table row: closing a reconciliation with a variance
-- is authorized the same way ApplyDiscount's tiered discounts already are
-- (internal/sales/discounts.go's authorizerPermits) — an inline PIN check
-- against the approver's role NAME (Branch Manager/Merchant Admin), not
-- the router-level RequirePermission middleware, since the same endpoint
-- is open to any authenticated user for the (far more common) zero-
-- variance case and only needs an approver when there's something to
-- approve. A dedicated permissions row would sit unused — this codebase
-- already has the right mechanism for exactly this shape of check.
