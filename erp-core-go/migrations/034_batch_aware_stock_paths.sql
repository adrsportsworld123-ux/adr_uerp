-- =====================================================================
-- Phase 8 follow-up: batch-aware stock paths (Grocery/FMCG, Pharmacy)
--
-- 029_grocery.sql disclosed that manual adjustments, reconciliation and
-- purchase returns moved stock_levels.on_hand without touching
-- stock_batches. A completeness audit (2026-10-04) found the same gap in
-- three more paths it didn't list: branch transfers (dispatch and
-- receive), voiding a finalized sale, and offline-synced sales — plus
-- PATCH /sales/orders/{id}/lines/{line_id}, which reserved extra stock on
-- a quantity increase WITHOUT running the batch allocation at all, so the
-- expiry and min_shelf_life_days checks could be bypassed simply by
-- adding 1 unit and then raising the quantity.
--
-- All of those are now batch-aware in Go (internal/inventory/batches.go's
-- DrainBatches/ReceiveBatchReturningID/ReleaseLineBatchQuantity). This
-- table is the one schema piece they need: a ledger of every batch
-- quantity change that ISN'T a sale line's own allocation (that already
-- has sales_order_line_batches) or a GRN receipt (goods_receipt_lines
-- already carries batch_no/expiry_date). Two jobs:
--   1. Recall traceability — "where did batch X go?" is answerable for
--      write-offs, supplier returns and inter-branch moves, not just sales.
--   2. Branch transfers read their own 'transfer_out' rows back at
--      receive time, so the destination branch gets the SAME batch_no,
--      expiry_date and cost the goods actually left with — without this,
--      received stock would arrive with no batch and be unsellable there.
-- =====================================================================

CREATE TABLE stock_batch_movements (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id    UUID NOT NULL REFERENCES merchants(id),
    batch_id       UUID NOT NULL REFERENCES stock_batches(id),
    quantity_delta NUMERIC(14,3) NOT NULL,  -- signed
    reference_type TEXT NOT NULL CHECK (reference_type IN (
        'adjustment', 'inventory_reconciliation', 'purchase_return',
        'transfer_out', 'transfer_in', 'void', 'offline_sale')),
    -- The row that caused it: stock_movements.id for an adjustment,
    -- inventory_reconciliations.id, purchase_returns.id,
    -- branch_transfer_lines.id (both directions), sales_orders.id (void),
    -- sales_order_lines.id (offline sale).
    reference_id   UUID,
    performed_by   UUID REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE stock_batch_movements ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_batch_movements
    USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

GRANT SELECT, INSERT ON stock_batch_movements TO erp_app;

CREATE INDEX idx_stock_batch_movements_batch ON stock_batch_movements(batch_id, created_at DESC);
CREATE INDEX idx_stock_batch_movements_ref   ON stock_batch_movements(reference_type, reference_id);

-- Supplier debit notes for perishables/drugs need the batch actually
-- returned. Optional: NULL means "drawn oldest-expiry-first" (the actual
-- split is in stock_batch_movements either way).
ALTER TABLE purchase_return_lines ADD COLUMN batch_no TEXT;
