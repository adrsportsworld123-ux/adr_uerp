-- =====================================================================
-- Phase 4, sub-area 4: Reconciliation & Audit — Inventory reconciliation
-- (phased_roadmap.md Phase 4; pos_frd_complete.md §16's "3. Inventory:
-- Physical count vs system (cycle counting or full audit, variance
-- analysis)")
--
-- Scope: this is #3 of the FRD's four reconciliation types — #1 (Cash,
-- migrations/016) and #2 (Payment Gateway, migrations/017) already
-- closed, #4 (Inter-branch Transfer) closed in Phase 2. This is the last
-- item in this sub-area (audit trail hardening — immutable/checksummed,
-- 7-year retention — is tracked separately, not a reconciliation type).
--
-- §16's "Variance Handling (All 3)" names an "Investigation workflow
-- (flag → investigate → resolve → approve → adjust)" — a full multi-step
-- state machine is deliberately not built here, the same "skip the
-- formal step" simplification this codebase has already applied
-- elsewhere (Purchase's PO approval, Accounting's manual entries posting
-- immediately). Instead this follows Cash Reconciliation's own already-
-- shipped shape exactly: one submission carries the full count, a
-- non-zero variance needs a Branch Manager/Merchant Admin PIN inline
-- (checked the same way ApplyDiscount/CreateCashReconciliation already
-- do — not the router-level permission middleware, so a clean count
-- stays open to any authenticated stock clerk), and an approved variance
-- posts immediately as a real stock adjustment + ledger entry rather
-- than sitting in an intermediate "flagged" state.
--
-- Lives in a new internal/inventory file (inventory_reconciliation.go),
-- not internal/accounting — unlike Cash/Bank/Payment Gateway
-- reconciliation, this one is fundamentally about stock_levels, not the
-- ledger; the ledger posting it triggers is a side effect, the same
-- relationship AdjustStock already has with accounting.PostJournalEntry.
-- =====================================================================

CREATE TABLE inventory_reconciliations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    branch_id       UUID NOT NULL REFERENCES branches(id),
    recon_type      TEXT NOT NULL CHECK (recon_type IN ('cycle', 'full')),  -- cycle = a named subset of variants; full = every variant with a stock_levels row at this branch
    recon_date      DATE NOT NULL,
    reason          TEXT,                                -- required whenever any line has a non-zero variance
    variance_value  NUMERIC(14,2) NOT NULL DEFAULT 0,     -- sum of every line's variance × that variant's cost_price at count time — positive = net overage, negative = net shortage
    counted_by      UUID NOT NULL REFERENCES users(id),
    authorized_by   UUID REFERENCES users(id),            -- Branch Manager/Merchant Admin PIN-approval; NULL when every line's variance = 0
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_inventory_reconciliations_branch ON inventory_reconciliations(merchant_id, branch_id, recon_date);

CREATE TABLE inventory_reconciliation_lines (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reconciliation_id       UUID NOT NULL REFERENCES inventory_reconciliations(id) ON DELETE CASCADE,
    variant_id              UUID NOT NULL REFERENCES product_variants(id),
    system_qty              NUMERIC(14,3) NOT NULL,   -- stock_levels.on_hand snapshotted at count time, before any adjustment this reconciliation makes
    counted_qty             NUMERIC(14,3) NOT NULL,   -- the physical count
    variance                NUMERIC(14,3) NOT NULL,   -- counted_qty - system_qty; positive = overage, negative = shortage
    variance_value          NUMERIC(14,2) NOT NULL    -- variance × cost_price, stored (not just derived) for a stable audit record independent of a later cost change
);
CREATE INDEX idx_inventory_reconciliation_lines_recon ON inventory_reconciliation_lines(reconciliation_id);

ALTER TABLE inventory_reconciliations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON inventory_reconciliations USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

-- inventory_reconciliation_lines has no merchant_id of its own — same
-- "RLS via a subquery to the tenant-scoped parent" pattern
-- 003_hardening.sql established, already reused by
-- cash_reconciliation_denominations (migrations/016).
ALTER TABLE inventory_reconciliation_lines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON inventory_reconciliation_lines
    USING (reconciliation_id IN (SELECT id FROM inventory_reconciliations));

GRANT SELECT, INSERT, UPDATE, DELETE ON inventory_reconciliations, inventory_reconciliation_lines TO erp_app;

-- journal_entries.source_type needs widening again — the same recurring
-- gap this CHECK-constraint-as-enum shape produces every time a new kind
-- of journal-posting event is introduced (migrations/014, /016, /017 all
-- hit this before).
ALTER TABLE journal_entries DROP CONSTRAINT journal_entries_source_type_check;
ALTER TABLE journal_entries ADD CONSTRAINT journal_entries_source_type_check
    CHECK (source_type IN ('sale', 'sale_void', 'purchase_bill', 'bill_payment', 'purchase_return',
                            'inventory_adjustment', 'manual', 'receivable_payment', 'cash_reconciliation',
                            'payment_gateway_settlement', 'payment_gateway_settlement_reversal',
                            'inventory_reconciliation'));

-- No new ledger account and no new permission: a reconciliation's net
-- variance books against the same 'Inventory Shrinkage' account
-- (chart_of_accounts code '5001') AdjustStock already uses for a manual
-- correction — it's the identical kind of event (system stock corrected
-- to match reality), just sourced from a physical count instead of a
-- single typed-in delta. Submitting a count stays open to any
-- authenticated user for the zero-variance case (a stock clerk's count
-- matching the books needs no one's approval); a non-zero variance is
-- authorized the same inline role-name + bcrypt-PIN check
-- CreateCashReconciliation already established, not the router-level
-- RequirePermission middleware — a dedicated permission row would sit
-- unused for the same reason migrations/016's own header comment gives.
