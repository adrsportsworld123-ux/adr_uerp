-- =====================================================================
-- Localization, merchant level, with two POS-terminal overrides.
--
-- localization_settings (one row per merchant; no row = the defaults):
--   language        en | ta | hi | ar | ml | te | kn | fr   (default en)
--   timezone        IANA name, the merchant's business clock: the
--                   business day of the Z report and the dashboards, report
--                   dates and the default for new branches. Default
--                   Asia/Kolkata — the clock every report used until now.
--   text_direction  auto (by language: Arabic is right-to-left) | ltr | rtl
--   cart_side       right (default, today's POS layout) | left — the POS
--                   cart panel beside the product grid
--   denominations   the notes and coins counted at a shift close or a cash
--                   count; NULL = the currency's standard set
--   (Currency itself stays in tax_settings, migrations/061.)
--
-- pos_terminals.text_direction / cart_side: one till's override (NULL =
-- the merchant's).
--
-- app_merchant_tz() is the merchant's timezone inside a tenant
-- transaction; SQL that used the literal 'Asia/Kolkata' reads it instead,
-- so with no row nothing changes.
-- =====================================================================

CREATE TABLE localization_settings (
    merchant_id    UUID PRIMARY KEY REFERENCES merchants(id),
    language       TEXT NOT NULL DEFAULT 'en' CHECK (language IN ('en', 'ta', 'hi', 'ar', 'ml', 'te', 'kn', 'fr', 'es')),
    timezone       TEXT NOT NULL DEFAULT 'Asia/Kolkata',
    text_direction TEXT NOT NULL DEFAULT 'auto' CHECK (text_direction IN ('auto', 'ltr', 'rtl')),
    cart_side      TEXT NOT NULL DEFAULT 'right' CHECK (cart_side IN ('left', 'right')),
    denominations  NUMERIC(14,2)[] CHECK (denominations IS NULL OR (cardinality(denominations) BETWEEN 1 AND 20 AND 0 < ALL (denominations))),
    updated_by     UUID REFERENCES users(id),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE localization_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON localization_settings USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON localization_settings TO erp_app;

ALTER TABLE pos_terminals ADD COLUMN text_direction TEXT CHECK (text_direction IN ('auto', 'ltr', 'rtl'));
ALTER TABLE pos_terminals ADD COLUMN cart_side TEXT CHECK (cart_side IN ('left', 'right'));

CREATE OR REPLACE FUNCTION app_merchant_tz() RETURNS text
LANGUAGE sql STABLE AS $$
    SELECT COALESCE((SELECT timezone FROM localization_settings LIMIT 1), 'Asia/Kolkata')
$$;

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000064', 'localization.manage',
     'Change the language, time zone, text direction, POS cart side and cash denominations')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'Merchant Admin' AND p.code = 'localization.manage'
ON CONFLICT DO NOTHING;
