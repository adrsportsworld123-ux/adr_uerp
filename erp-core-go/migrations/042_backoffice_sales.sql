-- =====================================================================
-- Phase 14: back-office sales (phased_roadmap.md Phase 14 "Admin new-sale
-- screen ... built on the existing CreateCart/AddLineToCart/Checkout path
-- ... on the channel model Phase 7 introduced").
--
-- 1. A third channel, 'backoffice': a sale rung up in the admin app. It
--    gets its channel the same way 'online' does: from a virtual terminal
--    per branch (migrations/026), never from the client. Terminal lists,
--    pairing and PIN login already only see channel 'pos', so the virtual
--    terminal is invisible to them. Shifts don't apply (requireOpenShift
--    checks channel 'pos' only).
-- 2. Permission 'sales.backoffice' to sell from the admin app (Merchant
--    Admin and Branch Manager, by role name, like terminal.manage).
-- 3. Payment corrections (FRD §3 "Bill Modification": approval + reason +
--    audit). A finalized sale's prices and quantities are corrected by a
--    return plus a new sale, which keeps the GST trail intact; what a
--    correction fixes is a payment recorded against the wrong method
--    (rung up as cash, actually paid by card). The ledger is reclassed
--    between the two accounts; the original row's method changes, with
--    the before/after kept here and in audit_logs.
-- =====================================================================

ALTER TABLE pos_terminals DROP CONSTRAINT pos_terminals_channel_check;
ALTER TABLE pos_terminals ADD CONSTRAINT pos_terminals_channel_check CHECK (channel IN ('pos', 'online', 'backoffice'));
ALTER TABLE sales_orders DROP CONSTRAINT sales_orders_channel_check;
ALTER TABLE sales_orders ADD CONSTRAINT sales_orders_channel_check CHECK (channel IN ('pos', 'online', 'backoffice'));

INSERT INTO pos_terminals (id, merchant_id, branch_id, name, channel, status)
SELECT gen_random_uuid(), b.merchant_id, b.id, 'Back Office', 'backoffice', 'active'
FROM branches b
WHERE NOT EXISTS (SELECT 1 FROM pos_terminals pt WHERE pt.branch_id = b.id AND pt.channel = 'backoffice');

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000042', 'sales.backoffice',
     'Ring up sales from the back-office (admin) app')
ON CONFLICT (code) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name IN ('Merchant Admin', 'Branch Manager') AND p.code = 'sales.backoffice'
ON CONFLICT DO NOTHING;

CREATE TABLE payment_corrections (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    sales_order_id  UUID NOT NULL REFERENCES sales_orders(id),
    payment_id      UUID NOT NULL REFERENCES payments(id),
    from_method     TEXT NOT NULL,
    to_method       TEXT NOT NULL,
    from_reference  TEXT,
    to_reference    TEXT,
    amount          NUMERIC(14,2) NOT NULL,
    reason          TEXT NOT NULL,
    performed_by    UUID NOT NULL REFERENCES users(id),
    approved_by     UUID NOT NULL REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_payment_corrections_order ON payment_corrections (sales_order_id);
ALTER TABLE payment_corrections ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payment_corrections USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT ON payment_corrections TO erp_app;

ALTER TABLE journal_entries DROP CONSTRAINT journal_entries_source_type_check;
ALTER TABLE journal_entries ADD CONSTRAINT journal_entries_source_type_check
    CHECK (source_type IN ('sale', 'sale_void', 'purchase_bill', 'bill_payment', 'purchase_return',
                           'inventory_adjustment', 'manual', 'receivable_payment', 'cash_reconciliation',
                           'payment_gateway_settlement', 'payment_gateway_settlement_reversal',
                           'inventory_reconciliation', 'sales_return', 'cash_movement', 'payment_correction'));
