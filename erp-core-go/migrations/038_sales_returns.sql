-- =====================================================================
-- Phase 13: sales returns and refunds (phased_roadmap.md Phase 13;
-- pos_frd_complete.md §3 "Return & Exchange")
--
-- Until now a finalized sale could only be voided whole. A return takes
-- back some or all of some lines, refunds their share of what was paid,
-- optionally restocks them (to the exact batches the sale consumed), and
-- posts a reversing journal entry for just that share.
--
-- FRD §3 asks for a hybrid model: immediate, or approval first. The
-- merchant chooses (sales_settings.returns_require_approval, default ON =
-- a Branch Manager / Merchant Admin approves with employee code + PIN, the
-- same pattern discounts and stock-count variances already use).
--
-- Order status:
--   partially_refunded  some lines/quantity returned, sale still stands
--   refunded            everything returned (the status existed since
--                       001_schema.sql but nothing ever set it)
-- =====================================================================

CREATE TABLE sales_settings (
    merchant_id                UUID PRIMARY KEY REFERENCES merchants(id),
    returns_require_approval   BOOLEAN NOT NULL DEFAULT true,
    -- NULL = no time limit. Counted in days from the sale's finalized_at.
    return_window_days         INTEGER CHECK (return_window_days IS NULL OR return_window_days > 0),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sales_returns (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    sales_order_id      UUID NOT NULL REFERENCES sales_orders(id),
    branch_id           UUID NOT NULL REFERENCES branches(id),
    pos_terminal_id     UUID REFERENCES pos_terminals(id),
    return_number       TEXT NOT NULL,
    reason              TEXT NOT NULL,
    refund_method       TEXT NOT NULL CHECK (refund_method IN ('cash', 'card', 'upi', 'credit')),
    refund_reference    TEXT,
    subtotal            NUMERIC(14,2) NOT NULL,   -- taxable value refunded
    tax_amount          NUMERIC(14,2) NOT NULL,
    refund_amount       NUMERIC(14,2) NOT NULL,   -- subtotal + tax, what the customer gets back
    restocked           BOOLEAN NOT NULL,
    processed_by        UUID NOT NULL REFERENCES users(id),
    approved_by         UUID REFERENCES users(id),
    idempotency_key     TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, idempotency_key),
    UNIQUE (branch_id, return_number)
);
CREATE INDEX idx_sales_returns_order ON sales_returns (sales_order_id);

CREATE TABLE sales_return_lines (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sales_return_id      UUID NOT NULL REFERENCES sales_returns(id) ON DELETE CASCADE,
    sales_order_line_id  UUID NOT NULL REFERENCES sales_order_lines(id),
    variant_id           UUID NOT NULL REFERENCES product_variants(id),
    quantity             NUMERIC(14,3) NOT NULL CHECK (quantity > 0),
    taxable_amount       NUMERIC(14,2) NOT NULL,
    tax_amount           NUMERIC(14,2) NOT NULL,
    refund_amount        NUMERIC(14,2) NOT NULL
);
CREATE INDEX idx_sales_return_lines_order_line ON sales_return_lines (sales_order_line_id);

-- Which batch each returned unit went back to, so a line returned in
-- several pieces never restores more to a batch than the sale took from it.
CREATE TABLE sales_return_line_batches (
    sales_return_line_id UUID NOT NULL REFERENCES sales_return_lines(id) ON DELETE CASCADE,
    batch_id             UUID NOT NULL REFERENCES stock_batches(id),
    quantity             NUMERIC(14,3) NOT NULL CHECK (quantity > 0),
    PRIMARY KEY (sales_return_line_id, batch_id)
);

-- RLS, the same tenant policy every tenant-scoped table uses. The child
-- tables carry no merchant_id; they're only ever reached through their
-- parent inside a tenant transaction (same as sales_order_lines' children).
ALTER TABLE sales_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sales_settings
    USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE sales_returns ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sales_returns
    USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE sales_orders DROP CONSTRAINT sales_orders_status_check;
ALTER TABLE sales_orders ADD CONSTRAINT sales_orders_status_check
    CHECK (status IN ('cart', 'held', 'finalized', 'voided', 'refunded', 'partially_refunded'));

ALTER TABLE stock_movements DROP CONSTRAINT stock_movements_movement_type_check;
ALTER TABLE stock_movements ADD CONSTRAINT stock_movements_movement_type_check
    CHECK (movement_type IN ('sale', 'adjustment', 'transfer_in', 'transfer_out', 'purchase', 'return'));

ALTER TABLE stock_batch_movements DROP CONSTRAINT stock_batch_movements_reference_type_check;
ALTER TABLE stock_batch_movements ADD CONSTRAINT stock_batch_movements_reference_type_check
    CHECK (reference_type IN ('adjustment', 'inventory_reconciliation', 'purchase_return', 'transfer_out',
                              'transfer_in', 'void', 'offline_sale', 'sales_return'));

ALTER TABLE journal_entries DROP CONSTRAINT journal_entries_source_type_check;
ALTER TABLE journal_entries ADD CONSTRAINT journal_entries_source_type_check
    CHECK (source_type IN ('sale', 'sale_void', 'purchase_bill', 'bill_payment', 'purchase_return',
                           'inventory_adjustment', 'manual', 'receivable_payment', 'cash_reconciliation',
                           'payment_gateway_settlement', 'payment_gateway_settlement_reversal',
                           'inventory_reconciliation', 'sales_return'));

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000038', 'sales.settings',
     'Change merchant-wide sales settings such as whether returns need manager approval')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'Merchant Admin' AND p.code = 'sales.settings'
ON CONFLICT DO NOTHING;
