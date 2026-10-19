-- =====================================================================
-- 1. Discounts on open-price items, configurable per branch. An
--    open-price item's price is typed at the till (migrations/039), so a
--    merchant may not want promotions, coupons, loyalty redemption or a
--    manual discount to cut it further.
--      sales_settings.open_price_discounts     the merchant's default
--      branch_sales_settings.open_price_discounts   a branch's override
--                                               (NULL = the merchant's)
--    false = open-price lines are left out of every discount layer and
--    of the subtotal a percent discount, a coupon's minimum spend or a
--    promotion is sized on. true (the default, and every existing row) =
--    exactly as before. app_open_price_discountable(branch) resolves it.
--
-- 2. A description typed for an open-price line
--    (sales_order_lines.description): what the item was ("Custom
--    engraving — 3 lines"). Printed on the receipt and invoice in place of
--    the catalog name; NULL (every existing line) prints the name as
--    before. Statutory summaries (GSTR HSN) keep the catalog name.
--
-- 3. branch_print_settings.show_sku: whether receipts and invoices print
--    the SKU under the product name. true (default) = as before.
--
-- 4. users.manage: the User Management screen (passwords, PINs, status,
--    home branch, branch access, roles). Merchant Admin.
-- =====================================================================

-- 1. ---------------------------------------------------------------------

ALTER TABLE sales_settings ADD COLUMN open_price_discounts BOOLEAN NOT NULL DEFAULT true;

CREATE TABLE branch_sales_settings (
    branch_id            UUID PRIMARY KEY REFERENCES branches(id) ON DELETE CASCADE,
    merchant_id          UUID NOT NULL REFERENCES merchants(id),
    open_price_discounts BOOLEAN,               -- NULL = the merchant's default
    updated_by           UUID REFERENCES users(id),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE branch_sales_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON branch_sales_settings USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON branch_sales_settings TO erp_app;

-- Whether open-price lines of branch b take discounts and promotions.
CREATE OR REPLACE FUNCTION app_open_price_discountable(b uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT COALESCE(
        (SELECT open_price_discounts FROM branch_sales_settings WHERE branch_id = b),
        (SELECT open_price_discounts FROM sales_settings LIMIT 1),
        true)
$$;

-- 2. ---------------------------------------------------------------------

ALTER TABLE sales_order_lines ADD COLUMN description TEXT
    CHECK (description IS NULL OR char_length(btrim(description)) BETWEEN 1 AND 120);

-- 3. ---------------------------------------------------------------------

ALTER TABLE branch_print_settings ADD COLUMN show_sku BOOLEAN NOT NULL DEFAULT true;

-- 4. ---------------------------------------------------------------------

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000062', 'users.manage',
     'Manage users: reset passwords and PINs, unlock, activate or deactivate, home branch and branch access')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'Merchant Admin' AND p.code = 'users.manage'
ON CONFLICT DO NOTHING;
