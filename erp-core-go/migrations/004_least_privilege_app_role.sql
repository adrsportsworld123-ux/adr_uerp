-- =====================================================================
-- CRITICAL FIX — found while verifying the Phase 1 hardening migration
-- against a live instance of this repo's own shipped docker-compose.yml:
--
-- docker-compose.yml's `postgres` service sets POSTGRES_USER=app_user, and
-- the official postgres Docker image grants that env-var-created role
-- SUPERUSER. docker-compose.yml's `api` service then connects to Postgres
-- AS THAT SAME app_user. Per Postgres's own documentation, superusers (and
-- any role with the BYPASSRLS attribute) always bypass row-level security,
-- full stop — no CREATE POLICY, no FORCE ROW LEVEL SECURITY, nothing in
-- 001_schema.sql or 003_hardening.sql changes that. Confirmed directly:
--
--   SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = 'app_user';
--   -- rolsuper = t, rolbypassrls = t
--
--   -- with app.tenant_id set to a tenant that owns NO rows at all:
--   SELECT count(*) FROM sales_orders;  -- returns every tenant's rows, not 0
--
-- This means every RLS policy in this schema — the mechanism
-- phase0_1_design.md §2.1 documents as "verified against a live Postgres
-- instance" and CLAUDE.md calls a non-negotiable, fail-closed guarantee —
-- has been silently inert for every request the API has ever served
-- through this docker-compose stack, because the connecting role was
-- always exempt from it. Nothing about the Go code (db.WithTenant,
-- set_config, etc.) was wrong; the role the whole story assumed was
-- enforcing it never actually was.
--
-- Fix: a dedicated, non-superuser, non-bypassrls role for the API to
-- connect as. It is NOT the owner of these tables either (app_user/the
-- migration-running role remains the owner) — a non-owner, non-superuser,
-- non-bypassrls role is unconditionally subject to RLS the moment RLS is
-- enabled on a table, which is exactly the property this needs.
-- =====================================================================

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'erp_app') THEN
        CREATE ROLE erp_app LOGIN PASSWORD 'erp_app_password'
            NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS NOREPLICATION;
    END IF;
END $$;

GRANT USAGE ON SCHEMA public TO erp_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO erp_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO erp_app;

-- So tables/sequences added by future migrations (run as the owning role,
-- same as this one) are usable by erp_app without a manual GRANT each time.
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO erp_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO erp_app;

-- docker-compose.yml's `api` service DATABASE_DSN must be updated to
-- connect as erp_app, not app_user/POSTGRES_USER — done alongside this
-- migration. app_user remains the schema owner for running migrations
-- (docker-entrypoint-initdb.d, and any future `psql -f migrations/*.sql`)
-- but must never again be the role the running service authenticates as.
