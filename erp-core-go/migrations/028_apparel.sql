-- =====================================================================
-- Phase 8: Vertical Expansion — Apparel
-- (phased_roadmap.md Phase 8: "you're already close from Phase 1's
-- variant matrix — mainly needs seasonal/collection catalog features")
--
-- Apparel's own size/color variant differentiation was already fully
-- covered by Phase 1's product_variants.attribute_combo, and — since the
-- prior Phase 8 pass — by the same category-attribute-set mechanism
-- jewelry/electronics/sports now use (a "Size"/"Color" attribute with a
-- controlled value list, assignable to an Apparel category). The one
-- genuinely missing piece is this: grouping products into a season or
-- named collection (e.g. "Monsoon Drop 2026"), for browsing/reporting by
-- collection the way a real apparel retailer actually organizes a
-- catalog — not a per-variant attribute, a product-level grouping.
-- =====================================================================

CREATE TABLE collections (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    name        TEXT NOT NULL,   -- e.g. "Monsoon Drop 2026"
    season      TEXT,            -- freeform, e.g. "Summer 2026" — display/filter only, no enforced enum
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, name)
);

ALTER TABLE collections ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON collections
    USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON collections TO erp_app;

-- NULL (every existing product, today) means "no collection" — zero
-- behavior change for anyone not using this feature.
ALTER TABLE products ADD COLUMN collection_id UUID REFERENCES collections(id);
