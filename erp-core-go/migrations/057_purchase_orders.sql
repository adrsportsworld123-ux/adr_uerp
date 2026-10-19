-- =====================================================================
-- Purchase Orders: the formal PO → GRN → Purchase Invoice flow that
-- migrations/006 deferred ("Option A: direct GRN → Bill"). Additive only:
-- a GRN without a PO (direct receipt, the invoice importer) works exactly
-- as before, so nothing existing changes behaviour.
--
--   * A PO is the commitment to the supplier: lines with an ordered
--     quantity and agreed unit cost. It never touches stock or the books.
--   * A PO is received by one or more GRNs (partial deliveries). Each
--     GRN line points at the PO line it receives (po_line_id). The GRN's
--     quantity may be less than the PO's; it is never more than what is
--     still pending (an over-receipt is refused, PO_OVER_RECEIPT).
--   * received_qty moves only when a GRN is COMPLETED (stock actually
--     in), inside the same transaction, under a row lock on the PO, so two
--     GRNs completing at once can't both receive the last units.
--   * Once every line's received_qty reaches its quantity the PO is
--     'completed' by itself. A supplier who will never deliver the rest
--     is handled by closing the PO short ('closed', with a reason).
--   * The Purchase Invoice is the existing purchase bill, one per GRN
--     (internal/purchase/bills.go), so the ledger posting is unchanged.
--
-- Status: draft (editable) → open (issued to the supplier) →
-- partially_received → completed; draft/open → cancelled (nothing
-- received yet); partially_received → closed (short-closed).
-- =====================================================================

CREATE TABLE purchase_orders (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    branch_id       UUID NOT NULL REFERENCES branches(id),
    supplier_id     UUID NOT NULL REFERENCES suppliers(id),
    po_number       TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'draft'
                        CHECK (status IN ('draft','open','partially_received','completed','cancelled','closed')),
    order_date      DATE NOT NULL DEFAULT CURRENT_DATE,
    expected_date   DATE,
    subtotal        NUMERIC(14,2) NOT NULL DEFAULT 0,   -- sum of line quantity*unit_cost
    notes           TEXT,
    close_reason    TEXT,                               -- why it was cancelled or closed short
    created_by      UUID REFERENCES users(id),
    issued_by       UUID REFERENCES users(id),
    issued_at       TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, po_number)
);

CREATE TABLE purchase_order_lines (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    po_id           UUID NOT NULL REFERENCES purchase_orders(id) ON DELETE CASCADE,
    variant_id      UUID NOT NULL REFERENCES product_variants(id),
    quantity        NUMERIC(14,3) NOT NULL CHECK (quantity > 0),
    unit_cost       NUMERIC(14,2) NOT NULL CHECK (unit_cost >= 0),
    line_total      NUMERIC(14,2) NOT NULL,
    received_qty    NUMERIC(14,3) NOT NULL DEFAULT 0 CHECK (received_qty >= 0 AND received_qty <= quantity),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The links from the receiving side. NULL = a direct GRN (no PO), which
-- is every GRN that exists today.
ALTER TABLE goods_receipt_notes ADD COLUMN purchase_order_id UUID REFERENCES purchase_orders(id);
ALTER TABLE goods_receipt_lines ADD COLUMN po_line_id UUID REFERENCES purchase_order_lines(id);

CREATE INDEX idx_purchase_orders_supplier ON purchase_orders(supplier_id, status);
CREATE INDEX idx_purchase_orders_branch   ON purchase_orders(branch_id, created_at DESC);
CREATE INDEX idx_po_lines_po              ON purchase_order_lines(po_id);
CREATE INDEX idx_grn_purchase_order       ON goods_receipt_notes(purchase_order_id) WHERE purchase_order_id IS NOT NULL;
CREATE INDEX idx_grn_lines_po_line        ON goods_receipt_lines(po_line_id) WHERE po_line_id IS NOT NULL;

ALTER TABLE purchase_orders ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON purchase_orders
    USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
-- Branch-bearing transactional table: same restrictive branch scope as
-- goods_receipt_notes (migrations/044).
CREATE POLICY branch_scope ON purchase_orders AS RESTRICTIVE
    USING (app_branch_visible(branch_id)) WITH CHECK (app_branch_visible(branch_id));

-- No merchant_id of its own: scoped through the parent, like goods_receipt_lines.
ALTER TABLE purchase_order_lines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON purchase_order_lines
    USING (po_id IN (SELECT id FROM purchase_orders));

GRANT SELECT, INSERT, UPDATE, DELETE ON purchase_orders, purchase_order_lines TO erp_app;
