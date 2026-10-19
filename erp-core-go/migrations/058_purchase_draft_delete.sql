-- =====================================================================
-- Deleting a purchase order or GRN entered by mistake (internal/purchase
-- DeletePurchaseOrder / DeleteGRN). Soft delete, the migrations/050 way:
-- the row is stamped deleted_at/deleted_by, audited, hidden by RLS from
-- every query, and can be restored from the recycle bin.
--
-- Only documents that changed nothing may be deleted:
--   * a purchase order in draft or open (issued) with no GRN against it;
--   * a GRN in draft (stock, cost and the PO's received quantities move
--     only when a GRN completes). A completed GRN is corrected with a
--     purchase return, never deleted.
--
-- Additive: no existing row has deleted_at set, so nothing that reads
-- these tables sees any difference.
-- =====================================================================

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['purchase_orders', 'purchase_order_lines', 'goods_receipt_notes', 'goods_receipt_lines'] LOOP
        EXECUTE format('ALTER TABLE %I ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ', t);
        EXECUTE format('ALTER TABLE %I ADD COLUMN IF NOT EXISTS deleted_by UUID REFERENCES users(id)', t);
        EXECUTE format('DROP POLICY IF EXISTS not_deleted ON %I', t);
        EXECUTE format(
            'CREATE POLICY not_deleted ON %I AS RESTRICTIVE USING (deleted_at IS NULL OR app_include_deleted()) '
            'WITH CHECK (deleted_at IS NULL OR app_include_deleted())', t);
    END LOOP;
END $$;
