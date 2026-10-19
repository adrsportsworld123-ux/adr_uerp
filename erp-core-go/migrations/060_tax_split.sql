-- =====================================================================
-- GST split on bills and invoices: CGST + SGST for an intra-state supply,
-- IGST for an inter-state one, printed separately on every sale invoice,
-- receipt, quotation and purchase bill, and carried into the reports and
-- GST returns. For merchants outside India, a "single tax" regime prints
-- one tax line under the merchant's own label (VAT, Sales Tax, GST).
--
-- 1. tax_settings: the merchant's default (branch_id NULL) and optional
--    per-branch overrides. A branch row's NULL regime / label inherits the
--    merchant's. state_code is the GST state the branch (or merchant)
--    supplies from; when unset it comes from the branch GSTIN, then the
--    merchant GSTIN (app_branch_state). No row at all = 'gst_split',
--    label 'GST' — the Indian standard every existing merchant uses.
--
-- 2. Line snapshots, so a later change to a tax slab never rewrites an
--    old invoice: sales_order_lines / quotation_lines get tax_rate (the
--    GST rate, cess excluded) and cess_amount (the cess inside
--    tax_amount), set by a trigger whenever a line's tax is written — the
--    online cart, quantity edits, discount layers and offline sync push
--    all get it without each having to remember.
--
-- 3. Place of supply, fixed on the sale: sales_orders.place_of_supply
--    (2-digit GST state code) and inter_state. A customer with a GSTIN
--    from another state makes the sale inter-state (IGST); a walk-in or
--    same-state customer is intra-state (CGST + SGST). Kept live while
--    the order is a cart or held, frozen once it is finalized.
--
-- 4. The split itself is never stored per sale line: it is derived from
--    the line's tax, its cess and the order's inter_state by gst_cgst /
--    gst_sgst / gst_igst (and internal/taxes in Go, the same rule), so
--    the invoice, the receipt and GSTR-1 can never disagree.
--      intra:  CGST = round((tax - cess) / 2, 2);  SGST = the rest
--      inter:  IGST = tax - cess
--
-- 5. purchase_bills: the supplier invoice's own CGST / SGST / IGST / cess
--    (they always add up to tax_total). Existing bills are split by the
--    supplier's GSTIN state against the branch's — intra-state 50/50,
--    which is what GSTR-3B already assumed for them.
--
-- Additive: no amount, total, journal entry or stock figure changes.
-- tax_total stays the one tax figure every existing report reads.
-- =====================================================================

-- 1. Settings ----------------------------------------------------------

CREATE TABLE tax_settings (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    branch_id   UUID REFERENCES branches(id) ON DELETE CASCADE,   -- NULL = the merchant's default
    regime      TEXT CHECK (regime IN ('gst_split', 'single')),   -- NULL on a branch row = inherit
    tax_label   TEXT CHECK (tax_label IS NULL OR char_length(btrim(tax_label)) BETWEEN 1 AND 20),
    state_code  TEXT CHECK (state_code IS NULL OR state_code ~ '^[0-9]{2}$'),
    updated_by  UUID REFERENCES users(id),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (branch_id IS NOT NULL OR regime IS NOT NULL)
);
CREATE UNIQUE INDEX uq_tax_settings_merchant ON tax_settings (merchant_id) WHERE branch_id IS NULL;
CREATE UNIQUE INDEX uq_tax_settings_branch   ON tax_settings (branch_id)   WHERE branch_id IS NOT NULL;
ALTER TABLE tax_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tax_settings USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON tax_settings TO erp_app;

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000060', 'tax.settings',
     'Change how tax is split and labelled on bills and invoices, for the merchant and each branch')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'Merchant Admin' AND p.code = 'tax.settings'
ON CONFLICT DO NOTHING;

-- The GST state code a GSTIN belongs to (its first two digits), or NULL.
CREATE OR REPLACE FUNCTION gst_state_of(gstin text) RETURNS text
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN btrim(gstin) ~ '^[0-9]{2}' THEN left(btrim(gstin), 2) END
$$;

-- The state a branch supplies from: its own setting, its GSTIN, the
-- merchant's setting, the merchant's GSTIN. NULL when none is known
-- (every supply is then treated as intra-state).
CREATE OR REPLACE FUNCTION app_branch_state(b uuid) RETURNS text
LANGUAGE sql STABLE AS $$
    SELECT COALESCE(bs.state_code, gst_state_of(br.gstin), ms.state_code, gst_state_of(m.gstin))
    FROM branches br
    JOIN merchants m ON m.id = br.merchant_id
    LEFT JOIN tax_settings bs ON bs.branch_id = br.id
    LEFT JOIN tax_settings ms ON ms.merchant_id = br.merchant_id AND ms.branch_id IS NULL
    WHERE br.id = b
$$;

-- The per-line split (see the header, point 4).
CREATE OR REPLACE FUNCTION gst_cgst(tax numeric, cess numeric, inter boolean) RETURNS numeric
LANGUAGE sql IMMUTABLE AS $$ SELECT (CASE WHEN inter THEN 0 ELSE round((tax - cess) / 2, 2) END)::numeric(14,2) $$;
CREATE OR REPLACE FUNCTION gst_sgst(tax numeric, cess numeric, inter boolean) RETURNS numeric
LANGUAGE sql IMMUTABLE AS $$ SELECT (CASE WHEN inter THEN 0 ELSE (tax - cess) - round((tax - cess) / 2, 2) END)::numeric(14,2) $$;
CREATE OR REPLACE FUNCTION gst_igst(tax numeric, cess numeric, inter boolean) RETURNS numeric
LANGUAGE sql IMMUTABLE AS $$ SELECT (CASE WHEN inter THEN tax - cess ELSE 0 END)::numeric(14,2) $$;

-- 2. Line snapshots ----------------------------------------------------

ALTER TABLE sales_order_lines ADD COLUMN tax_rate    NUMERIC(5,2);
ALTER TABLE sales_order_lines ADD COLUMN cess_amount NUMERIC(14,2) NOT NULL DEFAULT 0;
ALTER TABLE quotation_lines   ADD COLUMN tax_rate    NUMERIC(5,2);
ALTER TABLE quotation_lines   ADD COLUMN cess_amount NUMERIC(14,2) NOT NULL DEFAULT 0;

-- The rate from the product's tax slab as the line is written; a line
-- whose slab can't be read falls back to the rate its own figures imply
-- (tax / taxable value, to the nearest 0.25%).
CREATE OR REPLACE FUNCTION line_tax_snapshot() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    gst  numeric;
    cess numeric;
    taxable numeric;
BEGIN
    SELECT COALESCE(ts.cgst_rate, 0) + COALESCE(ts.sgst_rate, 0) + COALESCE(ts.igst_rate, 0), COALESCE(ts.cess_rate, 0)
    INTO gst, cess
    FROM product_variants pv
    JOIN products p ON p.id = pv.product_id
    JOIN tax_slabs ts ON ts.id = p.tax_slab_id
    WHERE pv.id = NEW.variant_id;

    IF gst IS NOT NULL AND gst + cess > 0 THEN
        NEW.tax_rate := gst;
        NEW.cess_amount := CASE WHEN cess > 0 THEN round(NEW.tax_amount * cess / (gst + cess), 2) ELSE 0 END;
    ELSE
        taxable := NEW.line_total - NEW.tax_amount;
        NEW.tax_rate := CASE WHEN taxable > 0 THEN round(NEW.tax_amount / taxable * 400) / 4 ELSE 0 END;
        NEW.cess_amount := 0;
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER sales_order_lines_tax_snapshot
    BEFORE INSERT OR UPDATE OF tax_amount, variant_id ON sales_order_lines
    FOR EACH ROW EXECUTE FUNCTION line_tax_snapshot();
CREATE TRIGGER quotation_lines_tax_snapshot
    BEFORE INSERT OR UPDATE OF tax_amount, variant_id ON quotation_lines
    FOR EACH ROW EXECUTE FUNCTION line_tax_snapshot();

-- Existing lines: the rate their own figures imply (the slab may have
-- changed since), cess as the current slab has it.
UPDATE sales_order_lines l SET cess_amount = (
    SELECT round(l.tax_amount * ts.cess_rate / (ts.cgst_rate + ts.sgst_rate + ts.igst_rate + ts.cess_rate), 2)
    FROM product_variants pv JOIN products p ON p.id = pv.product_id JOIN tax_slabs ts ON ts.id = p.tax_slab_id
    WHERE pv.id = l.variant_id)
WHERE EXISTS (
    SELECT 1 FROM product_variants pv JOIN products p ON p.id = pv.product_id JOIN tax_slabs ts ON ts.id = p.tax_slab_id
    WHERE pv.id = l.variant_id AND ts.cess_rate > 0);
UPDATE sales_order_lines SET
    tax_rate = CASE WHEN taxable_value > 0 THEN round((tax_amount - cess_amount) / taxable_value * 400) / 4 ELSE 0 END;

UPDATE quotation_lines l SET cess_amount = (
    SELECT round(l.tax_amount * ts.cess_rate / (ts.cgst_rate + ts.sgst_rate + ts.igst_rate + ts.cess_rate), 2)
    FROM product_variants pv JOIN products p ON p.id = pv.product_id JOIN tax_slabs ts ON ts.id = p.tax_slab_id
    WHERE pv.id = l.variant_id)
WHERE EXISTS (
    SELECT 1 FROM product_variants pv JOIN products p ON p.id = pv.product_id JOIN tax_slabs ts ON ts.id = p.tax_slab_id
    WHERE pv.id = l.variant_id AND ts.cess_rate > 0);
UPDATE quotation_lines SET
    tax_rate = CASE WHEN line_total - tax_amount > 0 THEN round((tax_amount - cess_amount) / (line_total - tax_amount) * 400) / 4 ELSE 0 END;

-- 3. Place of supply ---------------------------------------------------

ALTER TABLE sales_orders ADD COLUMN place_of_supply TEXT;
ALTER TABLE sales_orders ADD COLUMN inter_state BOOLEAN NOT NULL DEFAULT false;

CREATE OR REPLACE FUNCTION sale_supply_snapshot() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    branch_state text;
    customer_state text;
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.status NOT IN ('cart', 'held') THEN
        RETURN NEW;  -- finalized (or voided / refunded): the invoice's place of supply is fixed
    END IF;
    branch_state := app_branch_state(NEW.branch_id);
    IF NEW.customer_id IS NOT NULL THEN
        SELECT gst_state_of(c.gstin) INTO customer_state FROM customers c WHERE c.id = NEW.customer_id;
    END IF;
    NEW.place_of_supply := COALESCE(customer_state, branch_state);
    NEW.inter_state := customer_state IS NOT NULL AND branch_state IS NOT NULL AND customer_state <> branch_state;
    RETURN NEW;
END $$;

CREATE TRIGGER sales_orders_supply_snapshot
    BEFORE INSERT OR UPDATE OF status, customer_id, branch_id ON sales_orders
    FOR EACH ROW EXECUTE FUNCTION sale_supply_snapshot();

UPDATE sales_orders so SET
    place_of_supply = COALESCE(x.customer_state, x.branch_state),
    inter_state = x.customer_state IS NOT NULL AND x.branch_state IS NOT NULL AND x.customer_state <> x.branch_state
FROM (
    SELECT o.id, gst_state_of(c.gstin) AS customer_state, app_branch_state(o.branch_id) AS branch_state
    FROM sales_orders o LEFT JOIN customers c ON c.id = o.customer_id
) x
WHERE x.id = so.id;

-- 5. Purchase bills ----------------------------------------------------

ALTER TABLE purchase_bills ADD COLUMN cgst_amount NUMERIC(14,2) NOT NULL DEFAULT 0;
ALTER TABLE purchase_bills ADD COLUMN sgst_amount NUMERIC(14,2) NOT NULL DEFAULT 0;
ALTER TABLE purchase_bills ADD COLUMN igst_amount NUMERIC(14,2) NOT NULL DEFAULT 0;
ALTER TABLE purchase_bills ADD COLUMN cess_amount NUMERIC(14,2) NOT NULL DEFAULT 0;
ALTER TABLE purchase_bills ADD COLUMN inter_state BOOLEAN NOT NULL DEFAULT false;

UPDATE purchase_bills pb SET inter_state = true
FROM suppliers s
WHERE s.id = pb.supplier_id
  AND gst_state_of(s.gstin) IS NOT NULL AND app_branch_state(pb.branch_id) IS NOT NULL
  AND gst_state_of(s.gstin) <> app_branch_state(pb.branch_id);
UPDATE purchase_bills SET
    cgst_amount = gst_cgst(tax_total, 0, inter_state),
    sgst_amount = gst_sgst(tax_total, 0, inter_state),
    igst_amount = gst_igst(tax_total, 0, inter_state);

ALTER TABLE purchase_bills ADD CONSTRAINT purchase_bills_tax_split_check
    CHECK (cgst_amount + sgst_amount + igst_amount + cess_amount = tax_total
           AND cgst_amount >= 0 AND sgst_amount >= 0 AND igst_amount >= 0 AND cess_amount >= 0);
