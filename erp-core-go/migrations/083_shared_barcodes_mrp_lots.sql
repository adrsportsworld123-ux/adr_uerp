-- =====================================================================
-- One original barcode, several things on the shelf (§3.86).
--
-- 1. An original barcode (printed on the goods) may be shared by variants
--    of the SAME product: one code on every colour of a shirt, or every
--    size of a shoe. A generated code still names one variant. The
--    till asks which one when it is scanned. Across products a code stays
--    unique, as before: the old UNIQUE (merchant_id, code) becomes
--    UNIQUE (merchant_id, code, variant_id) plus a trigger that refuses a
--    code already on another product, raising the same unique violation
--    on the same constraint name, so every existing BARCODE_EXISTS
--    mapping keeps working. Nothing existing changes: today every code is
--    on one variant.
--
-- 2. MRP lots: the same item arrives with a new MRP while the old stock is
--    still on the shelf. Each MRP is its own variant (its stock, MRP,
--    selling price and cost) with mrp_parent_id pointing at the variant it
--    is a lot of. A lot has no barcode of its own: it answers to its
--    parent's codes. Scanning a code with lots in stock picks the one lot
--    in stock, or asks "Which MRP is on the pack?" when two or more are.
--    NULL on every existing variant.
--
-- 3. goods_receipt_lines.mrp / selling_price: the MRP printed on what
--    arrived (and the price to sell that lot at). NULL = as the variant,
--    as before. Completing the GRN receives a line whose MRP differs from
--    its variant's into that MRP's lot, creating the lot when needed.
-- =====================================================================

-- The one-code-per-merchant rule may be the original constraint or, on a
-- database set up differently, a unique index under another name: drop
-- whichever covers exactly (merchant_id, code).
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN
        SELECT c.conname FROM pg_constraint c
        WHERE c.conrelid = 'barcodes'::regclass AND c.contype = 'u'
          AND (SELECT array_agg(a.attname::text ORDER BY a.attname) FROM pg_attribute a
               WHERE a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)) = ARRAY['code', 'merchant_id']
    LOOP
        EXECUTE format('ALTER TABLE barcodes DROP CONSTRAINT %I', r.conname);
    END LOOP;
    FOR r IN
        SELECT i.indexrelid::regclass::text AS idx FROM pg_index i
        WHERE i.indrelid = 'barcodes'::regclass AND i.indisunique AND NOT i.indisprimary
          AND NOT EXISTS (SELECT 1 FROM pg_constraint c WHERE c.conindid = i.indexrelid)
          AND (SELECT array_agg(a.attname::text ORDER BY a.attname) FROM pg_attribute a
               WHERE a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)) = ARRAY['code', 'merchant_id']
    LOOP
        EXECUTE format('DROP INDEX %s', r.idx);
    END LOOP;
END $$;
-- Every statement below is guarded, so a database that already has part
-- of this (an index of the same name, the column) takes the rest as is.
DO $$
BEGIN
    IF to_regclass('barcodes_merchant_code_variant_key') IS NULL THEN
        ALTER TABLE barcodes ADD CONSTRAINT barcodes_merchant_code_variant_key UNIQUE (merchant_id, code, variant_id);
    END IF;
END $$;

CREATE OR REPLACE FUNCTION app_barcode_one_product() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    mine  uuid;
    other uuid;
BEGIN
    -- Two writers adding the same code at once queue here, so the check
    -- below always sees the other's row.
    PERFORM pg_advisory_xact_lock(hashtext(NEW.merchant_id::text || ':' || NEW.code));
    SELECT product_id INTO mine FROM product_variants WHERE id = NEW.variant_id;
    -- Only a code printed on the goods ('original') is shared, and only
    -- within one product; a code this system generated names one variant.
    SELECT v.product_id INTO other
    FROM barcodes b JOIN product_variants v ON v.id = b.variant_id
    WHERE b.merchant_id = NEW.merchant_id AND b.code = NEW.code AND b.id <> NEW.id
      AND (v.product_id <> mine OR NEW.source = 'generated' OR b.source = 'generated')
    LIMIT 1;
    IF other IS NOT NULL THEN
        RAISE EXCEPTION 'barcode % is already assigned to another product', NEW.code
            USING ERRCODE = 'unique_violation', CONSTRAINT = 'barcodes_merchant_id_code_key';
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS barcodes_one_product ON barcodes;
CREATE TRIGGER barcodes_one_product BEFORE INSERT OR UPDATE OF code, variant_id ON barcodes
    FOR EACH ROW EXECUTE FUNCTION app_barcode_one_product();

ALTER TABLE product_variants ADD COLUMN IF NOT EXISTS mrp_parent_id UUID REFERENCES product_variants(id);
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'product_variants_mrp_parent_check') THEN
        ALTER TABLE product_variants ADD CONSTRAINT product_variants_mrp_parent_check CHECK (mrp_parent_id IS NULL OR mrp_parent_id <> id);
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS idx_product_variants_mrp_parent ON product_variants (mrp_parent_id) WHERE mrp_parent_id IS NOT NULL;

ALTER TABLE goods_receipt_lines
    ADD COLUMN IF NOT EXISTS mrp           NUMERIC(14,2) CHECK (mrp IS NULL OR mrp > 0),
    ADD COLUMN IF NOT EXISTS selling_price NUMERIC(14,2) CHECK (selling_price IS NULL OR selling_price > 0);
