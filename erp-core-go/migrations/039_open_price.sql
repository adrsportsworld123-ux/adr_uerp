-- =====================================================================
-- Phase 13: open-price items (phased_roadmap.md Phase 13, decision 3:
-- "normal stock"; brief §5 "open price").
--
-- An open-price variant has no fixed shelf price: the cashier types the
-- price at billing (alterations, loose goods, a negotiated one-off). It is
-- still an ordinary stocked variant (reserved, batch-allocated and
-- decremented like any other); only the price source changes.
--
--   open_price_min / open_price_max   the price the cashier may enter,
--                                     inclusive, in the product's own tax
--                                     basis (products.price_includes_tax)
--   open_price_approval_above         above this, a Branch Manager or
--                                     Merchant Admin approves with employee
--                                     code + PIN; NULL = never needs approval
--
-- selling_price stays required and is shown as the suggested price.
--
-- On the sale line: open_price marks a cashier-entered price;
-- price_approved_by is the approver when one was needed. The entry is also
-- written to audit_logs (internal/audit) with the catalog price beside it.
-- =====================================================================

ALTER TABLE product_variants
    ADD COLUMN open_price                BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN open_price_min            NUMERIC(14,2),
    ADD COLUMN open_price_max            NUMERIC(14,2),
    ADD COLUMN open_price_approval_above NUMERIC(14,2);

ALTER TABLE product_variants ADD CONSTRAINT product_variants_open_price_check CHECK (
    NOT open_price OR (
        open_price_min IS NOT NULL AND open_price_max IS NOT NULL
        AND open_price_min >= 0 AND open_price_max > 0
        AND open_price_min <= open_price_max
        AND (open_price_approval_above IS NULL
             OR open_price_approval_above BETWEEN open_price_min AND open_price_max)
    )
);

ALTER TABLE sales_order_lines
    ADD COLUMN open_price        BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN price_approved_by UUID REFERENCES users(id);
