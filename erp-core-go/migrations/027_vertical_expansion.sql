-- =====================================================================
-- Phase 8: Vertical Expansion
-- (phased_roadmap.md Phase 8 — "ongoing, per new industry"; the user
-- explicitly chose the "attribute-set configuration" vertical group
-- (Jewelry/Steel/Chemicals/Electronics) plus configuring Sports, this
-- merchant's own real vertical, rather than a shallow pass across all
-- five listed verticals in one go)
--
-- This closes a real, existing gap rather than inventing new schema:
-- migrations/001_schema.sql already created `attributes`/`attribute_values`
-- (a name + input_type + controlled value list, e.g. "Purity": 14K/18K/
-- 22K/24K) specifically for this roadmap item — its own header even says
-- the point is to "validate the configurable masters, not
-- industry-specific code promise". But no Go code has ever read or
-- written those two tables since Phase 1: `product_variants
-- .attribute_combo` (a free-form JSONB map) has accepted ANY key/value
-- with zero validation the entire time, so two variants could disagree
-- on representation for what's meant to be the same attribute (e.g. "M"
-- vs "Medium") and nothing would catch it.
--
-- The one piece actually missing is this table: which attributes apply
-- to a given category, and whether each is required — the real
-- "attribute-set configuration" a new vertical needs (jewelry's
-- Purity/Weight, electronics' RAM/Storage, sports' Material, etc.).
-- internal/catalog now validates attribute_combo against this at
-- product-creation time (see products_write.go).
-- =====================================================================

CREATE TABLE category_attributes (
    category_id  UUID NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    attribute_id UUID NOT NULL REFERENCES attributes(id) ON DELETE CASCADE,
    required     BOOLEAN NOT NULL DEFAULT false,
    unit         TEXT,             -- e.g. "grams", "GB", "%" — display-only, not enforced
    sort_order   INT NOT NULL DEFAULT 0,
    PRIMARY KEY (category_id, attribute_id)
);

-- Same "join through the parent, no own merchant_id" RLS shape as
-- product_components/role_permissions (001_schema.sql's own closing
-- comment names this exact pattern for tables like this one).
ALTER TABLE category_attributes ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON category_attributes
    USING (category_id IN (SELECT id FROM categories));

GRANT SELECT, INSERT, UPDATE, DELETE ON category_attributes TO erp_app;
