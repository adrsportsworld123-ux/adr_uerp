-- =====================================================================
-- Phase 4's "Reconciliation & Audit: ... immutable audit trail with
-- 7-year retention" — named in the roadmap's own Phase 4 "In" list since
-- it was scoped, tracked separately from the three reconciliation types
-- (closed in migrations/016-018) as still open, and closed here after a
-- 2026-09-22 "review previous phases for anything missing" audit found
-- it was worse than just "not built yet": `audit_logs` had no RLS policy
-- at all (every other tenant-scoped table in this schema has one — this
-- was the one silent exception), and `erp_app` — the app's own runtime
-- role — held full UPDATE/DELETE on it via migrations/004's blanket
-- `GRANT ... ON ALL TABLES`. An "immutable" audit trail that the
-- application itself could freely rewrite or delete wasn't immutable at
-- all; nothing had ever exploited that, but nothing had ever closed it
-- either.
--
-- Three real controls, not one:
--   1. RLS, the same fail-closed tenant-isolation guarantee every other
--      tenant-scoped table in this schema already has (see internal/db's
--      own doc comment) — audit_logs was the one table missing it.
--   2. REVOKE UPDATE, DELETE — the database itself now refuses to let
--      erp_app rewrite or remove a row, not just "the application code
--      doesn't currently do that." SELECT/INSERT only.
--   3. A tamper-evident hash chain (`checksum`/`prev_checksum`) — each
--      row's checksum covers its own fields plus the previous row's
--      checksum (per merchant), so altering any historical row (even by
--      a superuser bypassing RLS/grants entirely, the one actor #1/#2
--      can't stop) breaks the chain from that point forward in a way
--      `GET /audit-logs/verify` can detect and report. See
--      internal/audit/log.go's own doc comment for the concurrency
--      trade-off this simple design accepts.
--
-- Retention: no purge mechanism exists, deliberately — "7-year
-- retention" is a floor (don't lose it before then), not a mandate to
-- delete at exactly 7 years, and an automated deletion job against
-- compliance data is a bigger, riskier decision than this pass should
-- make unilaterally. Indefinite retention trivially satisfies the
-- 7-year floor. What *does* need addressing is storage growth over that
-- horizon — partitioning (already in place, see below) is what makes an
-- eventual archive-old-partitions-to-cold-storage policy possible later
-- without ever touching the immutability/checksum guarantees above.
-- =====================================================================

-- pgcrypto's digest() computes the checksum entirely server-side, over
-- Postgres's own canonical `::text` cast of the just-inserted JSONB
-- values — found live during this pass's own verification: JSONB does
-- NOT round-trip byte-identical to whatever raw JSON bytes Go's
-- json.Marshal produced before insert (confirmed directly:
-- `'{"b":1,"a":2}'::jsonb::text` comes back as `{"a": 2, "b": 1}` — keys
-- reordered, whitespace added). Hashing Go's pre-insert bytes and later
-- comparing against Postgres's post-insert canonical text would have
-- made every single row "fail" verification even with zero tampering.
-- Computing the digest from the same `::text` cast on both write and
-- read (internal/audit/log.go and verify.go both use the identical SQL
-- expression) sidesteps the mismatch entirely by never depending on
-- Go's own JSON serialization for anything that has to compare equal
-- later.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS checksum TEXT;
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS prev_checksum TEXT;

ALTER TABLE audit_logs ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON audit_logs;
CREATE POLICY tenant_isolation ON audit_logs USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

-- The immutability control: erp_app keeps SELECT/INSERT, loses
-- UPDATE/DELETE outright. migrations/004's blanket grant runs on every
-- future table too, so this REVOKE has to be reasserted here — a
-- blanket re-GRANT on this table (a future migration doing
-- `GRANT ALL ... TO erp_app` broadly, the way 004 itself did once) would
-- silently undo it, so treat this table as the one deliberate exception
-- to that pattern going forward.
REVOKE UPDATE, DELETE ON audit_logs FROM erp_app;

-- Two years (24 months) of partitions from the current month forward.
-- Deliberately *not* an "ensure at boot" routine the running API does
-- itself, unlike internal/search.EnsureIndex's own such pattern for the
-- OpenSearch index — found live, the hard way: the API connects as
-- erp_app, the least-privilege role migrations/004's own hardening pass
-- exists specifically to keep DDL-incapable (`permission denied for
-- schema public`), so partition creation is DDL that has to run through
-- a migration (applied with a privileged role, the same way this file
-- itself is applied), not through the app's own runtime connection.
-- 003_hardening.sql's original "future months still need a scheduled
-- job or pg_partman" gap therefore stays a real, honest operational
-- task — this migration just seeds two years of runway so it isn't an
-- urgent one, and the DEFAULT partition still catches any month this
-- runway doesn't reach.
-- Skips a month whose range is already covered by an existing partition
-- under a *different* name — found live: 003_hardening.sql's own
-- `audit_logs_current` already covers the current month, and Postgres
-- range partitions can't overlap regardless of partition name, so a
-- plain `IF NOT EXISTS (... WHERE relname = ...)` name check (which is
-- all internal/audit.EnsurePartitions can cheaply do at every API
-- startup, going forward) isn't enough on its own — this migration's
-- one-time backfill additionally needs to tolerate that specific
-- already-covered case rather than aborting the whole migration on it.
DO $$
DECLARE
    i INT;
    part_start DATE;
    part_end DATE;
    part_name TEXT;
BEGIN
    FOR i IN 0..23 LOOP
        part_start := date_trunc('month', now())::date + (i || ' months')::interval;
        part_end := part_start + interval '1 month';
        part_name := 'audit_logs_' || to_char(part_start, 'YYYY_MM');
        IF NOT EXISTS (SELECT 1 FROM pg_class WHERE relname = part_name) THEN
            BEGIN
                EXECUTE format(
                    'CREATE TABLE %I PARTITION OF audit_logs FOR VALUES FROM (%L) TO (%L)',
                    part_name, part_start, part_end
                );
            EXCEPTION WHEN invalid_object_definition OR duplicate_table THEN
                RAISE NOTICE 'audit_logs partition for % already covered by an existing partition, skipping', part_start;
            END;
        END IF;
    END LOOP;
END $$;

-- New permission: audit trail rows can contain other users' before/after
-- field values (prices, credit limits, discount overrides) — reading the
-- log is a real compliance/oversight action, not a routine operational
-- one, same reasoning notifications.view already established for a
-- similar "PII/sensitive audit surface" read.
INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-00000000000d', 'audit.view', 'View the audit trail and verify its tamper-evident chain (GET /audit-logs, GET /audit-logs/verify)')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.merchant_id = '11111111-1111-1111-1111-111111111111'
  AND r.name = 'Merchant Admin'
  AND p.code = 'audit.view'
ON CONFLICT DO NOTHING;
