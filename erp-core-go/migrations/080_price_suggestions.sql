-- =====================================================================
-- Selling price suggestions (§3.83): a suggested selling price from the
-- cost price and MRP, by rules the Merchant Admin sets. A suggestion only
-- pre-fills a price someone then saves; nothing is priced by it on its
-- own, and the POS never sees it.
--
-- Plan feature `price_suggestion` (Professional and Enterprise; the SaaS
-- console moves it between plans). Every existing merchant is on
-- Enterprise, so it has the feature, but it stays switched off until the
-- Merchant Admin turns it on (pricing_settings.suggestion_enabled).
--
-- price_suggestion_rules: for every product, a category (and the ones
-- under it), a brand, or a category and a brand. The most specific live
-- rule wins (category + brand, the nearest category, brand, all), then
-- priority, then the newest.
--   method  'cost'   cost + markup % or the margin % on the price
--           'mrp'    MRP less a discount %
--           'lower'  the lower of the two;  'higher' the higher
--   cost_type 'margin' (price = cost / (1 − margin)) or 'markup'
--           (price = cost × (1 + markup)).
--   rounding  up to ₹1 / ₹5 / ₹10, to a price ending in 9, to ₹X.99, or
--           'charm': the next ₹5 up to ₹100, then the next price ending
--           in 9 (ADR Sports' pricing playbook); never down.
--   min_margin_pct: never below cost plus this margin.
-- The suggestion is never above MRP: a price that rounds over it is the
-- MRP ("at MRP"); when cost (plus the minimum margin) is at or above MRP
-- there is none ("hold"); with no MRP the price is suggested but marked
-- "check".
--
-- suggestion_cost_includes_tax: the item's cost price already includes
-- GST (no input credit). Off: GST is added to cost for a product whose
-- prices include it, so margins compare like for like.
-- =====================================================================

UPDATE plans SET features = array_append(features, 'price_suggestion'), updated_at = now()
WHERE code IN ('professional', 'enterprise') AND NOT ('price_suggestion' = ANY(features));

ALTER TABLE pricing_settings
    ADD COLUMN suggestion_enabled           BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN suggestion_cost_includes_tax BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE price_suggestion_rules (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id       UUID NOT NULL REFERENCES merchants(id),
    name              TEXT NOT NULL,
    category_id       UUID REFERENCES categories(id),
    brand_id          UUID REFERENCES brands(id),
    method            TEXT NOT NULL CHECK (method IN ('cost', 'mrp', 'lower', 'higher')),
    cost_type         TEXT NOT NULL DEFAULT 'margin' CHECK (cost_type IN ('margin', 'markup')),
    cost_pct          NUMERIC(6,2) CHECK (cost_pct IS NULL OR (cost_pct >= 0 AND cost_pct < 1000)),
    mrp_discount_pct  NUMERIC(5,2) CHECK (mrp_discount_pct IS NULL OR (mrp_discount_pct >= 0 AND mrp_discount_pct < 100)),
    min_margin_pct    NUMERIC(5,2) NOT NULL DEFAULT 0 CHECK (min_margin_pct >= 0 AND min_margin_pct < 100),
    rounding          TEXT NOT NULL DEFAULT 'none' CHECK (rounding IN ('none', 'up_1', 'up_5', 'up_10', 'end_9', 'end_99', 'charm')),
    priority          INT NOT NULL DEFAULT 0,
    active            BOOLEAN NOT NULL DEFAULT true,
    created_by        UUID REFERENCES users(id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ,
    deleted_by        UUID REFERENCES users(id),
    CHECK (cost_type <> 'margin' OR cost_pct IS NULL OR cost_pct < 100),
    CHECK (method = 'mrp' OR cost_pct IS NOT NULL),
    CHECK (method = 'cost' OR mrp_discount_pct IS NOT NULL)
);
CREATE INDEX idx_price_suggestion_rules_live ON price_suggestion_rules (merchant_id) WHERE active AND deleted_at IS NULL;
ALTER TABLE price_suggestion_rules ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON price_suggestion_rules
    USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY not_deleted ON price_suggestion_rules AS RESTRICTIVE
    USING (deleted_at IS NULL OR app_include_deleted())
    WITH CHECK (deleted_at IS NULL OR app_include_deleted());
GRANT SELECT, INSERT, UPDATE ON price_suggestion_rules TO erp_app;

-- Only the Merchant Admin switches suggestions on and sets the rules.
INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000080', 'pricing.suggestion.manage', 'Switch selling price suggestions on or off and set their rules')
ON CONFLICT (code) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'Merchant Admin' AND p.code = 'pricing.suggestion.manage'
ON CONFLICT DO NOTHING;
