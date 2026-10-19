-- =====================================================================
-- The POS product grid: open-price items first, and tile colours.
--
-- 1. sales_settings.open_price_first (the merchant's default) and
--    branch_sales_settings.open_price_first (a branch's override, NULL =
--    the default): the POS grid — the full list and every category
--    filter — shows open-price items before the rest. false (the default,
--    and every existing row) = the order it has today.
--    app_open_price_first(branch) resolves it.
--
-- 2. products.pos_color: the tile colour the POS paints a product with
--    (#RRGGBB), so a cashier spots it at a glance. NULL = no colour of its
--    own: an open-price item then gets the POS's standard open-price
--    colour, any other item the plain tile it has today.
-- =====================================================================

ALTER TABLE sales_settings ADD COLUMN open_price_first BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE branch_sales_settings ADD COLUMN open_price_first BOOLEAN;

CREATE OR REPLACE FUNCTION app_open_price_first(b uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT COALESCE(
        (SELECT open_price_first FROM branch_sales_settings WHERE branch_id = b),
        (SELECT open_price_first FROM sales_settings LIMIT 1),
        false)
$$;

ALTER TABLE products ADD COLUMN pos_color TEXT CHECK (pos_color IS NULL OR pos_color ~ '^#[0-9A-Fa-f]{6}$');
