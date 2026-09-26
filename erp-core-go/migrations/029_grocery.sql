-- =====================================================================
-- Phase 8: Vertical Expansion — Grocery/FMCG
-- (phased_roadmap.md Phase 8: "activate batch/lot + expiry tracking,
-- weighing-scale barcode integration, switch valuation to FIFO for
-- perishables")
--
-- Design constraint, stated up front because it shapes everything below:
-- CLAUDE.md flags internal/sales/reservation.go's optimistic-locking
-- stock_levels.version mechanism as sensitive and NOT to be replaced
-- without a real reason. Batch tracking here is an ADDITIVE layer that
-- runs alongside it, never a replacement — stock_levels.on_hand/reserved/
-- version stays the single, unchanged authority for "is there enough
-- stock"; stock_batches only decides WHICH batch(es) a sale draws from,
-- for expiry enforcement and FIFO ordering, and only for a variant that
-- explicitly opts in (product_variants.track_batch).
--
-- Real, disclosed scope limit (not an oversight): once track_batch=true,
-- ALL stock for that variant is expected to arrive via GRN with batch
-- info — manual inventory adjustments, reconciliation, and purchase
-- returns are NOT batch-aware yet (they still move stock_levels.on_hand
-- correctly, they just don't touch stock_batches), so mixing those paths
-- with a batch-tracked variant can desync the two. Fine for this pass's
-- goal (prove the mechanism genuinely works for GRN-received perishables
-- sold at POS); a real follow-up before this ships to a real grocery
-- merchant.
-- =====================================================================

ALTER TABLE product_variants ADD COLUMN track_batch BOOLEAN NOT NULL DEFAULT false;

-- Weighing-scale barcode integration's short internal code (distinct from
-- the full EAN-13 barcode/SKU) — see internal/catalog/handlers.go's
-- ServeHTTP for how a scanned weight-embedded code resolves back to a
-- variant via this. NULL for every variant that isn't sold by weight.
ALTER TABLE product_variants ADD COLUMN plu_code TEXT;
CREATE UNIQUE INDEX idx_product_variants_plu_code ON product_variants(merchant_id, plu_code) WHERE plu_code IS NOT NULL;

CREATE TABLE stock_batches (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id        UUID NOT NULL REFERENCES merchants(id),
    branch_id          UUID NOT NULL REFERENCES branches(id),
    variant_id         UUID NOT NULL REFERENCES product_variants(id),
    batch_no           TEXT NOT NULL,
    expiry_date        DATE,             -- NULL = no expiry tracked for this batch
    cost_price         NUMERIC(14,2) NOT NULL DEFAULT 0,
    quantity_received  NUMERIC(14,3) NOT NULL,
    quantity_remaining NUMERIC(14,3) NOT NULL,
    received_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (branch_id, variant_id, batch_no)
);

ALTER TABLE stock_batches ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_batches
    USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON stock_batches TO erp_app;

-- FIFO batch selection at sale time orders by expiry_date ascending
-- (soonest-to-expire sold first — the actual "FIFO valuation for
-- perishables" this phase asks for), so this index carries the real
-- query load, not just an FK lookup.
CREATE INDEX idx_stock_batches_variant_branch_expiry ON stock_batches(branch_id, variant_id, expiry_date);

-- Records which batch(es) a given sale actually drew from — audit trail
-- for recalls/traceability, and what DeleteLine reverses when a line is
-- removed before checkout.
CREATE TABLE sales_order_line_batches (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sales_order_line_id UUID NOT NULL REFERENCES sales_order_lines(id) ON DELETE CASCADE,
    batch_id            UUID NOT NULL REFERENCES stock_batches(id),
    quantity            NUMERIC(14,3) NOT NULL
);

-- Same "join through the parent" RLS shape as sales_order_lines itself.
ALTER TABLE sales_order_line_batches ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sales_order_line_batches
    USING (sales_order_line_id IN (SELECT id FROM sales_order_lines));

GRANT SELECT, INSERT, UPDATE, DELETE ON sales_order_line_batches TO erp_app;

-- GRN lines optionally carry the batch/expiry info a supplier delivery
-- note actually has — the real point of origin for this data. Required
-- (enforced in Go, not a DB constraint, since only batch-tracked variants
-- need it) when the received variant has track_batch = true.
ALTER TABLE goods_receipt_lines ADD COLUMN batch_no TEXT;
ALTER TABLE goods_receipt_lines ADD COLUMN expiry_date DATE;
