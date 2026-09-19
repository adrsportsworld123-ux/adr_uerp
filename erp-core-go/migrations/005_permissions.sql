-- =====================================================================
-- General permission-code enforcement — closes the gap flagged in
-- internal/inventory/handlers.go's AdjustStock doc comment: the schema's
-- permissions/role_permissions model existed since 001_schema.sql but no
-- handler ever queried it; AdjustStock and (until now) the new void
-- endpoint gated by role NAME as an interim measure instead.
--
-- Permissions are global (the `permissions` table has no merchant_id —
-- codes like 'inventory.adjust' mean the same thing for every tenant).
-- role_permissions grants them to specific role ROWS, which today are
-- per-merchant (see roles.merchant_id) since there's no merchant-
-- onboarding flow yet that provisions roles from the `is_system_default`
-- template roles the schema already anticipates. Practical consequence:
-- this migration grants permissions to the ONE seeded merchant's roles,
-- exactly like 002_seed.sql seeds that merchant's roles/users in the first
-- place. A real "new merchant" flow (Phase 2+) needs to grant the same
-- permissions to whatever roles it provisions for that merchant — this
-- migration is the pattern to copy, not a one-time fix that generalizes
-- on its own.
-- =====================================================================

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000001', 'inventory.adjust', 'Create a manual stock correction (POST /inventory/adjustments)'),
    ('f0000000-0000-0000-0000-000000000002', 'sales.void', 'Void a finalized sales order (POST /sales/orders/{id}/void)')
ON CONFLICT (code) DO NOTHING;

-- Branch Manager and Merchant Admin get both; POS User gets neither —
-- matches the FRD's tiering (a cashier can't unilaterally correct stock or
-- reverse a finalized sale).
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.merchant_id = '11111111-1111-1111-1111-111111111111'
  AND r.name IN ('Branch Manager', 'Merchant Admin')
  AND p.code IN ('inventory.adjust', 'sales.void')
ON CONFLICT DO NOTHING;
