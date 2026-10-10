-- =====================================================================
-- Phase 13: sync review queue (phased_roadmap.md Phase 13 "Sync queue
-- screen ... A NEGATIVE_STOCK oversell from §3.5 surfaces here and in the
-- admin as 'needs review'").
--
-- §3.5's rule stands: an offline sale already happened, so sync never
-- rejects it, and on_hand may go negative. Until now the only trace was an
-- audit_logs line nobody looks at. Each oversold line now opens a review
-- item someone resolves (usually after a count or an adjustment), with
-- the note of what was done.
-- =====================================================================

CREATE TABLE stock_review_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    branch_id       UUID NOT NULL REFERENCES branches(id),
    variant_id      UUID NOT NULL REFERENCES product_variants(id),
    sales_order_id  UUID NOT NULL REFERENCES sales_orders(id),
    pos_terminal_id UUID REFERENCES pos_terminals(id),
    reason          TEXT NOT NULL DEFAULT 'negative_stock' CHECK (reason IN ('negative_stock')),
    quantity        NUMERIC(14,3) NOT NULL,     -- what the offline line sold
    on_hand_after   NUMERIC(14,3) NOT NULL,     -- stock right after the sync applied it (< 0)
    status          TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    resolution      TEXT,
    resolved_by     UUID REFERENCES users(id),
    resolved_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((status = 'resolved') = (resolved_at IS NOT NULL))
);
CREATE INDEX idx_stock_review_open ON stock_review_items (branch_id, created_at) WHERE status = 'open';
CREATE INDEX idx_stock_review_order ON stock_review_items (sales_order_id);

ALTER TABLE stock_review_items ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_review_items USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON stock_review_items TO erp_app;
