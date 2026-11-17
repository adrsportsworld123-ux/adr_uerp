-- =====================================================================
-- Phase 8 completion (§3.87): the three items Phase 8 named but left
-- unbuilt (phased_roadmap.md Phase 8, §3.27).
--
-- 1. Weighing-scale barcode formats, per merchant. Until now one fixed
--    convention: prefix 21, 5-digit PLU, 5-digit weight in grams. A
--    merchant may now list the formats their scales print: a 2-digit
--    prefix (02, or 21-29; 20 is this system's own generated codes), the
--    PLU's digits, and whether the rest is a weight or a price, with its
--    decimals (13 digits in all, the last an EAN-13 check digit). No row
--    = the old convention, so nothing changes until a format is saved.
--
-- 2. Apparel markdowns: a markdown plan steps a collection's prices down
--    on dates ("20% off from 1 Jan, 40% from 15 Jan, until 31 Jan"). Each
--    step is an ordinary percent price rule scoped to the collection
--    (price_rules.collection_id, new) and tagged with its plan, so the
--    till, the online store and every report price it the way they price
--    any rule. A variant rule still beats it; it beats a category rule.
--
-- 3. Pharmacy: the Schedule H1 register (the record a chemist keeps of
--    each H1 / narcotic sale: drug, quantity, batch, patient and
--    prescriber with their addresses) as a report and PDF. A government
--    e-filing link stays out: no drug-control authority publishes an API
--    for it. prescriptions.doctor_address is the one field it lacked.
-- =====================================================================

CREATE TABLE IF NOT EXISTS scale_barcode_formats (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id    UUID NOT NULL REFERENCES merchants(id),
    prefix         TEXT NOT NULL CHECK (prefix ~ '^(02|2[1-9])$'),
    plu_digits     SMALLINT NOT NULL CHECK (plu_digits BETWEEN 4 AND 6),
    value_kind     TEXT NOT NULL CHECK (value_kind IN ('weight', 'price')),
    value_digits   SMALLINT NOT NULL CHECK (value_digits BETWEEN 4 AND 6),
    -- weight: 3 = grams (00500 = 0.500 kg); price: 2 = paise (01250 = 12.50)
    value_decimals SMALLINT NOT NULL CHECK (value_decimals BETWEEN 0 AND 3),
    active         BOOLEAN NOT NULL DEFAULT true,
    updated_by     UUID REFERENCES users(id),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, prefix),
    CHECK (plu_digits + value_digits = 10)
);
ALTER TABLE scale_barcode_formats ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON scale_barcode_formats;
CREATE POLICY tenant_isolation ON scale_barcode_formats USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON scale_barcode_formats TO erp_app;

CREATE TABLE IF NOT EXISTS markdown_plans (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id   UUID NOT NULL REFERENCES merchants(id),
    collection_id UUID NOT NULL REFERENCES collections(id),
    branch_id     UUID REFERENCES branches(id),   -- NULL = every branch
    name          TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cancelled')),
    created_by    UUID REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    cancelled_by  UUID REFERENCES users(id),
    cancelled_at  TIMESTAMPTZ,
    CHECK ((status = 'cancelled') = (cancelled_at IS NOT NULL))
);
ALTER TABLE markdown_plans ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON markdown_plans;
CREATE POLICY tenant_isolation ON markdown_plans USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON markdown_plans TO erp_app;
CREATE INDEX IF NOT EXISTS idx_markdown_plans_collection ON markdown_plans (collection_id);

ALTER TABLE price_rules
    ADD COLUMN IF NOT EXISTS collection_id    UUID REFERENCES collections(id),
    ADD COLUMN IF NOT EXISTS markdown_plan_id UUID REFERENCES markdown_plans(id);
-- The old "variant or category" check, whatever it is called here.
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN SELECT conname FROM pg_constraint WHERE conrelid = 'price_rules'::regclass AND contype = 'c'
             AND pg_get_constraintdef(oid) LIKE '%(variant_id IS NULL) <> (category_id IS NULL)%'
    LOOP
        EXECUTE format('ALTER TABLE price_rules DROP CONSTRAINT %I', r.conname);
    END LOOP;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'price_rules_one_scope') THEN
        ALTER TABLE price_rules ADD CONSTRAINT price_rules_one_scope CHECK (num_nonnulls(variant_id, category_id, collection_id) = 1);
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS idx_price_rules_collection ON price_rules (collection_id) WHERE active AND collection_id IS NOT NULL;

ALTER TABLE prescriptions ADD COLUMN IF NOT EXISTS doctor_address TEXT;
