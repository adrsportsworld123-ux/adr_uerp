-- =====================================================================
-- Soft delete. Every user-facing DELETE endpoint used to remove its row
-- outright: a mistaken delete of a price list, a promotion or a draft
-- voucher was unrecoverable, and a cashier removing an item from a cart
-- left no trace of it (the classic "scan, remove, pocket" loss pattern
-- needs exactly that trace). From here on those endpoints stamp
-- deleted_at/deleted_by instead (internal/softdelete), write an
-- audit_logs entry with the row as it was, and the row stays.
--
-- Hiding is done once, here, by RLS — not by adding "deleted_at IS NULL"
-- to the hundreds of existing queries that read these tables. Each table
-- gets a RESTRICTIVE policy: a deleted row is invisible to erp_app, and
-- cannot be updated, unless the transaction has explicitly set
-- app.include_deleted = 'on' (db.IncludeDeleted — the recycle bin and
-- the soft-delete/restore statements themselves). Same fail-closed shape
-- as app.branch_scope (migrations/044): forgetting the setting hides
-- deleted rows, it never shows them.
--
-- Covered: the masters (categories, brands, tax slabs, attribute values,
-- price lists and their items, price rules, scheduled price changes,
-- promotions, coupons, roles, report templates), the deletable drafts
-- (quotations, journal vouchers) and the lines a user removes from an
-- open document (cart lines, quotation lines).
--
-- Deliberately still physical deletes, because the row is an assignment
-- or derived bookkeeping, not a record anyone "deletes": user↔role,
-- role↔permission, user↔branch and category↔attribute links (each
-- change audited), a draft voucher's lines rewritten on edit, batch
-- allocations and discount layers recomputed from the cart, and the
-- signup rollback of a merchant whose provisioning failed.
--
-- Uniqueness: a deleted "Bats" category must not block creating a new
-- "Bats", so the name/code unique indexes become partial (live rows
-- only). Document numbers (voucher_number, quote_number) stay fully
-- unique: a number, once issued, is never reused.
-- =====================================================================

CREATE OR REPLACE FUNCTION app_include_deleted() RETURNS boolean
    LANGUAGE sql STABLE AS
$$ SELECT COALESCE(current_setting('app.include_deleted', true), '') = 'on' $$;

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'categories', 'brands', 'tax_slabs', 'attribute_values',
        'price_lists', 'price_list_items', 'price_rules', 'scheduled_price_changes',
        'promotions', 'coupons', 'roles', 'report_templates',
        'quotations', 'quotation_lines', 'journal_vouchers', 'sales_order_lines'
    ] LOOP
        EXECUTE format('ALTER TABLE %I ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ', t);
        EXECUTE format('ALTER TABLE %I ADD COLUMN IF NOT EXISTS deleted_by UUID REFERENCES users(id)', t);
        EXECUTE format('DROP POLICY IF EXISTS not_deleted ON %I', t);
        EXECUTE format(
            'CREATE POLICY not_deleted ON %I AS RESTRICTIVE USING (deleted_at IS NULL OR app_include_deleted()) '
            'WITH CHECK (deleted_at IS NULL OR app_include_deleted())', t);
    END LOOP;
END $$;

-- attribute_values never had RLS (its tenancy comes through attributes,
-- which it cascades from). Turning RLS on for the not_deleted policy
-- needs a permissive policy too, or every row would be refused; this one
-- keeps today's behaviour exactly.
ALTER TABLE attribute_values ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS via_attribute ON attribute_values;
CREATE POLICY via_attribute ON attribute_values USING (true) WITH CHECK (true);

-- Uniqueness over live rows only.
DROP INDEX IF EXISTS uq_brands_name;
CREATE UNIQUE INDEX uq_brands_name ON brands (merchant_id, lower(name)) WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS uq_categories_sibling_name;
CREATE UNIQUE INDEX uq_categories_sibling_name
    ON categories (merchant_id, COALESCE(parent_id, '00000000-0000-0000-0000-000000000000'::uuid), lower(name))
    WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS uq_tax_slabs_name;
CREATE UNIQUE INDEX uq_tax_slabs_name ON tax_slabs (merchant_id, lower(name)) WHERE deleted_at IS NULL;

ALTER TABLE coupons DROP CONSTRAINT IF EXISTS coupons_merchant_id_code_key;
CREATE UNIQUE INDEX uq_coupons_code ON coupons (merchant_id, code) WHERE deleted_at IS NULL;

ALTER TABLE price_lists DROP CONSTRAINT IF EXISTS price_lists_merchant_id_name_key;
CREATE UNIQUE INDEX uq_price_lists_name ON price_lists (merchant_id, name) WHERE deleted_at IS NULL;

-- PricingHandler.SetPriceListItem upserts on this; its ON CONFLICT names
-- the predicate so it infers the partial index.
ALTER TABLE price_list_items DROP CONSTRAINT IF EXISTS price_list_items_price_list_id_variant_id_key;
CREATE UNIQUE INDEX uq_price_list_items_variant ON price_list_items (price_list_id, variant_id) WHERE deleted_at IS NULL;

-- The recycle bin (GET /recycle-bin, POST /recycle-bin/{entity}/{id}/restore).
INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000050', 'recycle_bin.manage', 'See deleted masters and drafts, and restore them')
ON CONFLICT (code) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'Merchant Admin' AND p.code = 'recycle_bin.manage'
ON CONFLICT DO NOTHING;
