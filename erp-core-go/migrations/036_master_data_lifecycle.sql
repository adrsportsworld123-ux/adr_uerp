-- =====================================================================
-- Phase 11: master-data lifecycle (phased_roadmap.md Phase 11)
--
-- Categories, brands and tax slabs have had list + create only since
-- migrations/019_catalog_management.sql ("deliberately minimal ... no
-- update/delete"). The brief behind Phase 11 asks every master to support
-- edit, activate/deactivate and dependency-safe delete, so this adds the
-- one piece of schema that needs: a status column. Deactivating hides a
-- record from pickers for NEW products; products already linked keep
-- their link, and nothing historical changes (sales lines already store
-- their own tax amounts, so a tax slab's status never rewrites a past bill).
--
-- Delete stays possible only for a record nothing references (enforced in
-- Go via GET /dependencies/{entity}/{id}, and by the existing foreign
-- keys as the backstop). Deactivate is the answer for everything else.
--
-- Names are unique per merchant, case-insensitively (categories: per
-- parent, so "Bats" can exist under both Cricket and Baseball). Checked
-- before writing this migration: neither the seed database nor the
-- host development database had any duplicates for these indexes to
-- reject.
-- =====================================================================

ALTER TABLE categories
    ADD COLUMN status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

ALTER TABLE brands
    ADD COLUMN status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

ALTER TABLE tax_slabs
    ADD COLUMN status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE UNIQUE INDEX uq_categories_sibling_name
    ON categories (merchant_id, COALESCE(parent_id, '00000000-0000-0000-0000-000000000000'::uuid), lower(name));
CREATE UNIQUE INDEX uq_brands_name ON brands (merchant_id, lower(name));
CREATE UNIQUE INDEX uq_tax_slabs_name ON tax_slabs (merchant_id, lower(name));

-- DELETE on these three is new (only list/create existed). erp_app's
-- grants come from migrations/004's blanket GRANT, which already covers
-- DELETE on every public table, so no grant change is needed here.
