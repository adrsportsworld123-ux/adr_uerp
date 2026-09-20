-- =====================================================================
-- Phase 2, final sub-area: Product Search (OpenSearch)
-- (phased_roadmap.md Phase 2 — "catalog size and multi-branch stock
-- lookups now justify it (<=100ms target)")
--
-- This migration only adds the permission gating the reindex operation.
-- Everything else search-related lives outside Postgres: the index and
-- its documents live in OpenSearch (internal/search), not a new table
-- here. Postgres stays the system of record; OpenSearch is a derived,
-- rebuildable read model, so there is nothing to migrate on its side.
-- =====================================================================

-- Reindexing rebuilds the entire OpenSearch product index from Postgres
-- for the caller's tenant. Not dangerous to data (OpenSearch is a
-- disposable read model, not a source of truth), but it's a bulk
-- operation worth gating the same way inventory.adjust/pricing.manage
-- are, rather than leaving it open to every authenticated user.
INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000005', 'search.reindex', 'Rebuild the product search index from the catalog')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.merchant_id = '11111111-1111-1111-1111-111111111111'
  AND r.name = 'Merchant Admin'
  AND p.code = 'search.reindex'
ON CONFLICT DO NOTHING;
