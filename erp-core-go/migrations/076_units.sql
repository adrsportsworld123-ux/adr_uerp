-- =====================================================================
-- Units of measure (§3.79): a product keeps its stock in one base unit
-- (Pcs, Kg, g, L, ml, m, Dozen…), and each barcode can be a pack of it —
-- a 30-egg tray sells 30 Pcs, a 25 Kg bag 25 Kg, a 500 g packet 0.5 Kg.
-- Stock, cost and price stay per base unit everywhere (stock_levels,
-- sales lines, GRN lines): a pack only decides the quantity a scan adds.
--
-- A unit's `decimals` is how finely it is counted: a sale, GRN, PO,
-- quotation, transfer or adjustment of a product in a 0-decimal unit
-- (Pcs) must be a whole number.
--
-- Every existing product becomes Pcs — except one with a weighing-scale
-- code (PLU) or a sale or receipt in a fraction, which becomes Kg so
-- nothing it already does is refused. Every existing barcode is a pack
-- of 1, exactly what a scan added before.
-- =====================================================================

CREATE TABLE units (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id  UUID NOT NULL REFERENCES merchants(id),
    code         TEXT NOT NULL,
    name         TEXT NOT NULL,
    symbol       TEXT NOT NULL,
    -- count | weight | volume | length | area | pack. A pack unit (Box,
    -- Bag, Tray) has no fixed size: each barcode says how much is in it.
    kind         TEXT NOT NULL CHECK (kind IN ('count', 'weight', 'volume', 'length', 'area', 'pack')),
    -- How many of the kind's reference unit (pcs, kg, L, m, sq m) one of
    -- this is; NULL for a pack unit.
    factor       NUMERIC(18,6) CHECK (factor IS NULL OR factor > 0),
    decimals     SMALLINT NOT NULL DEFAULT 0 CHECK (decimals BETWEEN 0 AND 3),
    is_system    BOOLEAN NOT NULL DEFAULT false,
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    sort_order   INT NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((kind = 'pack') = (factor IS NULL))
);
CREATE UNIQUE INDEX uq_units_code ON units (merchant_id, lower(code));
ALTER TABLE units ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON units USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON units TO erp_app;

-- The standard set, for every merchant (subscriptions.systemUnits gives a
-- new one the same).
INSERT INTO units (merchant_id, code, name, symbol, kind, factor, decimals, is_system, sort_order)
SELECT m.id, v.code, v.name, v.symbol, v.kind, v.factor, v.decimals, true, v.ord
FROM merchants m
CROSS JOIN (VALUES
    ('PCS', 'Pieces', 'pcs', 'count', 1::numeric, 0, 1),
    ('DOZ', 'Dozen', 'dz', 'count', 12, 0, 2),
    ('PAIR', 'Pair', 'pr', 'count', 2, 0, 3),
    ('KG', 'Kilogram', 'kg', 'weight', 1, 3, 10),
    ('G', 'Gram', 'g', 'weight', 0.001, 0, 11),
    ('QTL', 'Quintal', 'qtl', 'weight', 100, 3, 12),
    ('L', 'Litre', 'L', 'volume', 1, 3, 20),
    ('ML', 'Millilitre', 'ml', 'volume', 0.001, 0, 21),
    ('M', 'Metre', 'm', 'length', 1, 2, 30),
    ('CM', 'Centimetre', 'cm', 'length', 0.01, 0, 31),
    ('SQFT', 'Square foot', 'sq ft', 'area', 0.092903, 2, 40),
    ('BOX', 'Box', 'box', 'pack', NULL, 0, 50),
    ('BAG', 'Bag', 'bag', 'pack', NULL, 0, 51),
    ('TRAY', 'Tray', 'tray', 'pack', NULL, 0, 52),
    ('PKT', 'Packet', 'pkt', 'pack', NULL, 0, 53),
    ('BTL', 'Bottle', 'btl', 'pack', NULL, 0, 54),
    ('CRATE', 'Crate', 'crate', 'pack', NULL, 0, 55),
    ('CTN', 'Carton', 'ctn', 'pack', NULL, 0, 56)
) AS v(code, name, symbol, kind, factor, decimals, ord);

ALTER TABLE products ADD COLUMN unit_id UUID REFERENCES units(id);

UPDATE products p SET unit_id = u.id
FROM units u
WHERE u.merchant_id = p.merchant_id
  AND u.code = CASE
      WHEN EXISTS (SELECT 1 FROM product_variants v WHERE v.product_id = p.id AND v.plu_code IS NOT NULL)
        OR EXISTS (SELECT 1 FROM sales_order_lines l JOIN product_variants v ON v.id = l.variant_id
                   WHERE v.product_id = p.id AND l.quantity <> trunc(l.quantity))
        OR EXISTS (SELECT 1 FROM goods_receipt_lines l JOIN product_variants v ON v.id = l.variant_id
                   WHERE v.product_id = p.id AND l.quantity <> trunc(l.quantity))
      THEN 'KG' ELSE 'PCS' END;

-- A product made by any path that doesn't name a unit (the product form,
-- an invoice import, an offline sync) is in Pieces.
CREATE FUNCTION app_default_product_unit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.unit_id IS NULL THEN
        SELECT id INTO NEW.unit_id FROM units WHERE merchant_id = NEW.merchant_id AND code = 'PCS';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER products_default_unit BEFORE INSERT ON products FOR EACH ROW EXECUTE FUNCTION app_default_product_unit();

-- A barcode is a pack of pack_qty base units (1 = one, as before), called
-- pack_unit_id (a Tray, a 500 g packet; NULL = the base unit itself).
ALTER TABLE barcodes
    ADD COLUMN pack_qty      NUMERIC(14,3) NOT NULL DEFAULT 1 CHECK (pack_qty > 0),
    ADD COLUMN pack_unit_id  UUID REFERENCES units(id),
    ADD COLUMN pack_size     NUMERIC(14,3);   -- as entered: 30 (base units in a Tray) or 500 (g)

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000076', 'units.manage', 'Add and edit units of measure')
ON CONFLICT (code) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'Merchant Admin' AND p.code = 'units.manage'
ON CONFLICT DO NOTHING;
