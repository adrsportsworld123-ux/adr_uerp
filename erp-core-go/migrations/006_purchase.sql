-- =====================================================================
-- Phase 2, sub-area 1: Purchase Management
-- (phased_roadmap.md Phase 2; pos_frd_complete.md §9)
--
-- Scope, per the roadmap's explicit call and the FRD's "Option A": direct
-- GRN → Bill entry, NOT the formal PO → Approval → Send → GRN → Invoice →
-- Payment workflow (deferred). So there is no purchase_orders table here —
-- a goods_receipt_note is the first document in the flow, created
-- directly against a supplier, with a purchase_bill following it for the
-- financial side. "Direct GRN (without PO) option" in the FRD is not an
-- option alongside a PO path here — for Phase 2 it's the only path.
--
-- Costing: FRD §2 "Valuation Methods" names Weighted Average Cost (WAC) as
-- primary — (existing_value + new_value) / (existing_qty + new_qty) — so
-- completing a GRN updates product_variants.cost_price via that formula,
-- not just stock_levels.on_hand.
-- =====================================================================

CREATE TABLE suppliers (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    name                TEXT NOT NULL,
    legal_name          TEXT,
    gstin               TEXT,
    contact_name        TEXT,
    email               TEXT,
    phone               TEXT,
    payment_terms       TEXT,                  -- e.g. "Net 30", "COD" — free text for Phase 2, not a structured enum
    credit_limit        NUMERIC(14,2) NOT NULL DEFAULT 0,
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE goods_receipt_notes (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    branch_id           UUID NOT NULL REFERENCES branches(id),
    supplier_id         UUID NOT NULL REFERENCES suppliers(id),
    grn_number          TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','completed','cancelled')),
    freight_amount      NUMERIC(14,2) NOT NULL DEFAULT 0,
    other_charges       NUMERIC(14,2) NOT NULL DEFAULT 0,   -- landed cost = line cost + freight + other_charges, allocated per line on completion
    subtotal            NUMERIC(14,2) NOT NULL DEFAULT 0,   -- sum of line quantity*unit_cost, before landed-cost allocation
    grand_total         NUMERIC(14,2) NOT NULL DEFAULT 0,   -- subtotal + freight_amount + other_charges
    notes               TEXT,
    received_by         UUID REFERENCES users(id),
    completed_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (branch_id, grn_number)
);

CREATE TABLE goods_receipt_lines (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    grn_id                  UUID NOT NULL REFERENCES goods_receipt_notes(id) ON DELETE CASCADE,
    variant_id              UUID NOT NULL REFERENCES product_variants(id),
    quantity                NUMERIC(14,3) NOT NULL,
    unit_cost               NUMERIC(14,2) NOT NULL,          -- purchase price per unit, before landed-cost allocation
    landed_unit_cost        NUMERIC(14,2),                   -- unit_cost + this line's share of freight/other_charges; set on completion
    line_total              NUMERIC(14,2) NOT NULL,          -- quantity * unit_cost
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE purchase_bills (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id             UUID NOT NULL REFERENCES merchants(id),
    branch_id               UUID NOT NULL REFERENCES branches(id),
    supplier_id             UUID NOT NULL REFERENCES suppliers(id),
    grn_id                  UUID NOT NULL REFERENCES goods_receipt_notes(id),
    bill_number             TEXT NOT NULL,
    supplier_invoice_number TEXT,
    bill_date               DATE NOT NULL DEFAULT CURRENT_DATE,
    due_date                DATE,
    subtotal                NUMERIC(14,2) NOT NULL DEFAULT 0,
    tax_total               NUMERIC(14,2) NOT NULL DEFAULT 0,
    freight_amount          NUMERIC(14,2) NOT NULL DEFAULT 0,
    other_charges           NUMERIC(14,2) NOT NULL DEFAULT 0,
    grand_total             NUMERIC(14,2) NOT NULL DEFAULT 0,
    amount_paid             NUMERIC(14,2) NOT NULL DEFAULT 0,
    status                  TEXT NOT NULL DEFAULT 'unpaid' CHECK (status IN ('unpaid','partially_paid','paid')),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, bill_number)
);

CREATE TABLE purchase_bill_payments (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    purchase_bill_id    UUID NOT NULL REFERENCES purchase_bills(id) ON DELETE CASCADE,
    amount              NUMERIC(14,2) NOT NULL,
    method              TEXT NOT NULL CHECK (method IN ('cash','card','upi','bank_transfer','cheque')),
    reference_no        TEXT,
    paid_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    performed_by        UUID REFERENCES users(id)
);

CREATE TABLE purchase_returns (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    branch_id           UUID NOT NULL REFERENCES branches(id),
    supplier_id         UUID NOT NULL REFERENCES suppliers(id),
    grn_id              UUID REFERENCES goods_receipt_notes(id),  -- nullable: a return can reference what was received, but isn't required to
    return_number       TEXT NOT NULL,
    reason              TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'completed' CHECK (status IN ('completed','cancelled')),
    grand_total         NUMERIC(14,2) NOT NULL DEFAULT 0,
    created_by          UUID REFERENCES users(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, return_number)
);

CREATE TABLE purchase_return_lines (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    purchase_return_id      UUID NOT NULL REFERENCES purchase_returns(id) ON DELETE CASCADE,
    variant_id              UUID NOT NULL REFERENCES product_variants(id),
    quantity                NUMERIC(14,3) NOT NULL,
    unit_cost               NUMERIC(14,2) NOT NULL,
    line_total              NUMERIC(14,2) NOT NULL
);

CREATE INDEX idx_suppliers_merchant             ON suppliers(merchant_id);
CREATE INDEX idx_grn_branch_supplier            ON goods_receipt_notes(branch_id, supplier_id);
CREATE INDEX idx_grn_lines_grn                  ON goods_receipt_lines(grn_id);
CREATE INDEX idx_purchase_bills_supplier        ON purchase_bills(supplier_id, status);
CREATE INDEX idx_purchase_bill_payments_bill    ON purchase_bill_payments(purchase_bill_id);
CREATE INDEX idx_purchase_returns_supplier      ON purchase_returns(supplier_id);

DO $$
DECLARE
    t TEXT;
BEGIN
    FOR t IN SELECT unnest(ARRAY['suppliers','goods_receipt_notes','purchase_bills','purchase_returns'])
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY;', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I USING (merchant_id = current_setting(''app.tenant_id'', true)::uuid);',
            t
        );
    END LOOP;
END $$;

-- goods_receipt_lines, purchase_bill_payments, purchase_return_lines have
-- no direct merchant_id, same pattern as sales_order_lines/payments
-- (migrations/003_hardening.sql) — RLS via a subquery to their parent,
-- which is itself RLS-protected.
ALTER TABLE goods_receipt_lines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON goods_receipt_lines
    USING (grn_id IN (SELECT id FROM goods_receipt_notes));

ALTER TABLE purchase_bill_payments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON purchase_bill_payments
    USING (purchase_bill_id IN (SELECT id FROM purchase_bills));

ALTER TABLE purchase_return_lines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON purchase_return_lines
    USING (purchase_return_id IN (SELECT id FROM purchase_returns));

-- Redundant with migration 004's ALTER DEFAULT PRIVILEGES (which already
-- covers new tables created by the same schema-owning role) but stated
-- explicitly so this migration is self-contained and correct even if that
-- assumption ever changes.
GRANT SELECT, INSERT, UPDATE, DELETE ON
    suppliers, goods_receipt_notes, goods_receipt_lines,
    purchase_bills, purchase_bill_payments,
    purchase_returns, purchase_return_lines
    TO erp_app;

-- Demo supplier for the seeded acme-sports merchant — belongs here, not
-- 002_seed.sql, since 002 runs before this migration on a fresh volume and
-- the suppliers table doesn't exist yet at that point (the exact ordering
-- mistake migrations/003's header comment already warns about).
INSERT INTO suppliers (id, merchant_id, name, legal_name, gstin, contact_name, email, phone, payment_terms, credit_limit)
VALUES ('11111111-2222-3333-4444-555555555555', '11111111-1111-1111-1111-111111111111',
        'SG Sports Distributors', 'SG Sports Distributors Pvt Ltd', '29ABCDE1234F1Z5',
        'Suresh Gupta', 'suresh@sgsports-supplier.test', '9876543210', 'Net 30', 500000.00);
