-- =====================================================================
-- Phase 1's "Product & Catalog Management" — the actual product-creation
-- gap, found during an explicit "review previous phases for anything
-- missing" audit. `POST /products`/`PATCH /products/{id}` were named in
-- phase0_1_design.md §3.2's contract table since Phase 0/1, but neither
-- was ever built (Phase 1 only ever needed the barcode-scan path, Phase
-- 2's Pricing sub-area only ever needed the browse/list path
-- `internal/catalog/products.go`'s `ListProducts` covers). The result:
-- after four phases of "fully complete" work across Purchase, Accounting,
-- Multi-Branch, Pricing, Search, CRM, Promotions, Loyalty, Notifications,
-- Credit, GST, and three reconciliation types, there was still no way for
-- a real merchant to add a second product to the one hard-seeded in
-- migrations/002_seed.sql — every one of those phases' own live
-- verification passes exercised that same single seed product.
--
-- Closing this needs three supporting lookups that also never got an
-- API surface at all (categories, brands, tax_slabs) — all three tables
-- have existed since migrations/001_schema.sql, seeded with exactly one
-- row apiece (or zero, for categories/brands), but with zero endpoints.
-- Kept deliberately minimal: list + create only, no update/delete, no
-- hierarchy management for categories beyond a single optional
-- parent_id — this is unblocking product creation, not building out
-- full catalog-taxonomy management as its own feature.
-- =====================================================================

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-00000000000c', 'catalog.manage', 'Create/update products, categories, brands, and tax slabs')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.merchant_id = '11111111-1111-1111-1111-111111111111'
  AND r.name IN ('Branch Manager', 'Merchant Admin')
  AND p.code = 'catalog.manage'
ON CONFLICT DO NOTHING;
