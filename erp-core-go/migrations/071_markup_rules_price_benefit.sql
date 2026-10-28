-- =====================================================================
-- Markup rules, and which price a branch sells at (phase0_1_design.md
-- §3.77).
--
-- markup_rules: a price worked out from the cost price, kept separately
-- from price_rules (which adjust the selling price). Each rule covers one
-- variant, a category (with its sub-categories) or, with neither, every
-- product; it may be limited to a branch, and has the same optional
-- dates, days and hours as a price rule. markup_type 'percent' = cost +
-- value %; 'fixed' = cost + value. The most specific live rule wins
-- (variant, then category, then all products), then priority, then the
-- newest. The markup price is never above MRP, and a product with no cost
-- price has none. For a product whose prices include GST the markup price
-- includes it too, so it compares like for like with the selling price.
--
-- price_benefit: which price a POS line gets, on sales_settings (the
-- merchant's default) and branch_sales_settings (a branch's override, NULL
-- = the default). app_price_benefit(branch) resolves it.
--   'standard' (the default, every existing merchant): as before. A price
--     rule, else the markup price acting as the selling price, else the
--     selling price.
--   'customer': the lowest of MRP, selling price, markup price and price
--     rule price.
--   'shop': the highest of the same.
-- A customer's price list (a contract price) still wins over all of them,
-- and quotations are priced as before.
--
-- sales_order_lines.markup_rule_id / price_basis record which rule and
-- which of those prices a line was sold at (NULL on every existing line).
-- =====================================================================

CREATE TABLE markup_rules (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id  UUID NOT NULL REFERENCES merchants(id),
    name         TEXT NOT NULL,
    variant_id   UUID REFERENCES product_variants(id),
    category_id  UUID REFERENCES categories(id),
    branch_id    UUID REFERENCES branches(id),     -- NULL = every branch
    markup_type  TEXT NOT NULL CHECK (markup_type IN ('percent', 'fixed')),
    value        NUMERIC(14,2) NOT NULL CHECK (value >= 0),
    priority     INT NOT NULL DEFAULT 0,
    starts_at    TIMESTAMPTZ,
    ends_at      TIMESTAMPTZ,
    days_of_week SMALLINT[],
    time_start   TIME,
    time_end     TIME,
    active       BOOLEAN NOT NULL DEFAULT true,
    created_by   UUID REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ,
    deleted_by   UUID REFERENCES users(id),
    CHECK (variant_id IS NULL OR category_id IS NULL),
    CHECK ((time_start IS NULL) = (time_end IS NULL)),
    CHECK (starts_at IS NULL OR ends_at IS NULL OR starts_at < ends_at)
);
CREATE INDEX idx_markup_rules_live ON markup_rules (merchant_id) WHERE active AND deleted_at IS NULL;

ALTER TABLE markup_rules ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON markup_rules
    USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY not_deleted ON markup_rules AS RESTRICTIVE
    USING (deleted_at IS NULL OR app_include_deleted())
    WITH CHECK (deleted_at IS NULL OR app_include_deleted());
GRANT SELECT, INSERT, UPDATE, DELETE ON markup_rules TO erp_app;

ALTER TABLE sales_settings ADD COLUMN price_benefit TEXT NOT NULL DEFAULT 'standard'
    CHECK (price_benefit IN ('standard', 'customer', 'shop'));
ALTER TABLE branch_sales_settings ADD COLUMN price_benefit TEXT
    CHECK (price_benefit IN ('standard', 'customer', 'shop'));

CREATE OR REPLACE FUNCTION app_price_benefit(b uuid) RETURNS text
LANGUAGE sql STABLE AS $$
    SELECT COALESCE(
        (SELECT price_benefit FROM branch_sales_settings WHERE branch_id = b),
        (SELECT price_benefit FROM sales_settings LIMIT 1),
        'standard')
$$;

ALTER TABLE sales_order_lines ADD COLUMN markup_rule_id UUID REFERENCES markup_rules(id);
ALTER TABLE sales_order_lines ADD COLUMN price_basis TEXT
    CHECK (price_basis IN ('price_list', 'price_rule', 'markup', 'selling_price', 'mrp'));
