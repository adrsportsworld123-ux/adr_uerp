-- =====================================================================
-- UACL (User Access Control): role/permission administration.
--
-- No new tables — roles, permissions, role_permissions, and user_roles
-- have all existed since migrations/001_schema.sql and have been the
-- real enforcement mechanism behind every `authn.RequirePermission(...)`
-- call in this codebase since Phase 0. What never existed was any API to
-- MANAGE that mechanism: no POST /roles, no way to assign a permission to
-- a role, no way to grant a user a second role. Every role/permission
-- grant in this codebase to date was seeded directly by a migration file
-- (a merchant literally could not create a custom role, e.g. the FRD's
-- own named "Accountant"/"HR Manager"/"Sales Manager" roles, without a
-- developer editing a .sql file). This migration adds exactly one new
-- permission to gate that management surface — no schema change beyond
-- it.
--
-- Gated to Merchant Admin only, not Branch Manager — of everything in
-- this system, controlling who can do what is the one thing that should
-- never be self-service for anyone below the top tier; same reasoning
-- audit.view already uses for a different kind of sensitive surface.
-- =====================================================================

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000011', 'rbac.manage', 'Create/edit roles, assign permissions to roles, and assign/revoke roles on users')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.merchant_id = '11111111-1111-1111-1111-111111111111'
  AND r.name = 'Merchant Admin'
  AND p.code = 'rbac.manage'
ON CONFLICT DO NOTHING;
