-- =====================================================================
-- Phase 13: POS shifts (phased_roadmap.md Phase 13 "Shifts"; decision 4:
-- the POS journal is cash in/out only).
--
-- A shift is one till's drawer from open to close: an opening float, cash
-- moved in and out during the day, and a counted close (the Z report).
--
-- Close feeds the existing Phase 4 cash reconciliation instead of
-- duplicating it: a shift close IS a cash_reconciliations row (linked by
-- pos_shift_id), with the same denomination lines, the same variance rule
-- (reason + Branch Manager/Merchant Admin approval), the same Cash
-- Over/Short posting, and the same history. A branch that runs shifts
-- reconciles per shift, so the daily whole-branch count is refused on a day
-- with shifts (and a shift can't open on a day already counted daily),
-- which keeps any variance from being posted twice.
--
-- Cash movements (pos_cash_movements):
--   float_in   change brought from the safe into the drawer   no posting
--   drop       cash taken from the drawer to the safe          no posting
--   paid_in    non-sale cash received                          Dr Cash / Cr Other Income
--   paid_out   an expense paid from the drawer                 Dr Petty Cash Expenses / Cr Cash
-- Account 1001 Cash is all of the business's physical cash, drawer and
-- safe alike, so moving it between the two isn't a ledger event; only
-- money entering or leaving the business is.
-- =====================================================================

CREATE TABLE pos_shifts (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id      UUID NOT NULL REFERENCES merchants(id),
    branch_id        UUID NOT NULL REFERENCES branches(id),
    pos_terminal_id  UUID NOT NULL REFERENCES pos_terminals(id),
    shift_number     INTEGER NOT NULL,               -- per terminal: 1, 2, 3 ...
    status           TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    opening_float    NUMERIC(14,2) NOT NULL CHECK (opening_float >= 0),
    opened_by        UUID NOT NULL REFERENCES users(id),
    opened_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_by        UUID REFERENCES users(id),
    closed_at        TIMESTAMPTZ,
    -- The Z report exactly as produced at close. Frozen: a sale that
    -- syncs later doesn't rewrite a closed shift's figures.
    z_report         JSONB,
    idempotency_key  TEXT NOT NULL,
    CHECK ((status = 'open') = (closed_at IS NULL)),
    UNIQUE (merchant_id, idempotency_key),
    UNIQUE (pos_terminal_id, shift_number)
);
-- One open shift per till; this also serialises two opens racing.
CREATE UNIQUE INDEX uq_pos_shifts_one_open ON pos_shifts (pos_terminal_id) WHERE status = 'open';
CREATE INDEX idx_pos_shifts_branch_opened ON pos_shifts (branch_id, opened_at);

CREATE TABLE pos_cash_movements (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id      UUID NOT NULL REFERENCES merchants(id),
    shift_id         UUID NOT NULL REFERENCES pos_shifts(id),
    branch_id        UUID NOT NULL REFERENCES branches(id),
    kind             TEXT NOT NULL CHECK (kind IN ('float_in', 'drop', 'paid_in', 'paid_out')),
    amount           NUMERIC(14,2) NOT NULL CHECK (amount > 0),
    reason           TEXT NOT NULL,
    performed_by     UUID NOT NULL REFERENCES users(id),
    approved_by      UUID REFERENCES users(id),
    idempotency_key  TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, idempotency_key)
);
CREATE INDEX idx_pos_cash_movements_shift ON pos_cash_movements (shift_id);

ALTER TABLE pos_shifts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pos_shifts USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE pos_cash_movements ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pos_cash_movements USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON pos_shifts, pos_cash_movements TO erp_app;

-- A shift close is a cash reconciliation for that one drawer. The old
-- one-per-branch-per-day rule now applies to whole-branch (daily) counts
-- only; each shift has at most one.
ALTER TABLE cash_reconciliations ADD COLUMN pos_shift_id UUID UNIQUE REFERENCES pos_shifts(id);
ALTER TABLE cash_reconciliations DROP CONSTRAINT cash_reconciliations_branch_id_recon_date_key;
CREATE UNIQUE INDEX uq_cash_reconciliations_daily ON cash_reconciliations (branch_id, recon_date) WHERE pos_shift_id IS NULL;

-- Per-merchant: refuse POS checkout on a till with no open shift. Off by
-- default, so nothing changes for merchants that don't use shifts.
ALTER TABLE sales_settings ADD COLUMN shifts_required BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE journal_entries DROP CONSTRAINT journal_entries_source_type_check;
ALTER TABLE journal_entries ADD CONSTRAINT journal_entries_source_type_check
    CHECK (source_type IN ('sale', 'sale_void', 'purchase_bill', 'bill_payment', 'purchase_return',
                           'inventory_adjustment', 'manual', 'receivable_payment', 'cash_reconciliation',
                           'payment_gateway_settlement', 'payment_gateway_settlement_reversal',
                           'inventory_reconciliation', 'sales_return', 'cash_movement'));

-- The two accounts cash in/out posts to, for every merchant that has a
-- chart of accounts (its Cash account), not just the demo merchant.
INSERT INTO chart_of_accounts (merchant_id, code, name, account_type, is_system)
SELECT c.merchant_id, v.code, v.name, v.account_type, true
FROM chart_of_accounts c
CROSS JOIN (VALUES ('4002', 'Other Income', 'income'), ('5004', 'Petty Cash Expenses', 'expense')) AS v(code, name, account_type)
WHERE c.code = '1001'
ON CONFLICT (merchant_id, code) DO NOTHING;
