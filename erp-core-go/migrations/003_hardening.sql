-- =====================================================================
-- Phase 1 hardening: closes gaps found in the post-Phase-0/1 gap analysis
-- (see docs/phased_roadmap.md's updated Phase 1 status for the full list).
-- Adds:
--   1. RLS on sales_order_lines and payments (previously enforced only by
--      application-level joins through sales_orders — see the note at the
--      bottom of 001_schema.sql). Both lack a direct merchant_id column,
--      so the policy filters via a subquery to sales_orders, which is
--      itself RLS-protected — the same app_user role evaluating this
--      policy has sales_orders' own tenant_isolation policy applied to
--      that subquery too, so this is not a bypassable double standard.
--   2. merchant_id on refresh_tokens, so /auth/refresh can resolve which
--      tenant a presented token belongs to WITHOUT first needing a
--      tenant-scoped read of `users` (refresh_tokens itself carries no
--      RLS policy — same "enforce via the auth proof, not RLS" pattern
--      already used for the dev password endpoints' merchant lookup).
--   3. sales_order_discounts, an audit trail of every manual/coupon
--      discount applied (type, value, who authorized it, why) — the FRD's
--      tiered discount authorization needs a record of who approved what,
--      not just the resulting discount_amount on the line.
--
-- NOTE: users.employee_code lives in 001_schema.sql, not here, even though
-- it was designed alongside this migration. On a genuinely fresh volume,
-- docker-entrypoint-initdb.d runs every file in this directory strictly in
-- filename order — 001, then 002_seed.sql, then this file — so a seed
-- INSERT that references a column this migration would have added doesn't
-- see that column yet, and fails (silently aborting the rest of that seed
-- script). Found exactly this way. The rule going forward: if a seed row
-- needs a column, that column belongs in 001_schema.sql, full stop —
-- never in a later-numbered migration, no matter how related it is.
-- =====================================================================

-- pos_terminals loses its RLS policy for the same reason `merchants` never
-- had one: POST /auth/pin-login must resolve which tenant a request
-- belongs to from `pos_terminal_id` alone (the documented request body has
-- no merchant_code), and that resolution has to happen BEFORE any
-- tenant-scoped query can run — the same chicken-and-egg problem
-- phase0_1_design.md §2.1 already solved once for merchants.code. This is
-- safe on the same grounds: pos_terminals is only ever looked up by its
-- primary-key UUID (never listed/enumerated across tenants), and its
-- columns (name, device_fingerprint, status) carry no cross-tenant-
-- sensitive aggregate data the way sales/financial tables would.
DROP POLICY IF EXISTS tenant_isolation ON pos_terminals;
ALTER TABLE pos_terminals DISABLE ROW LEVEL SECURITY;

ALTER TABLE sales_order_lines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sales_order_lines
    USING (sales_order_id IN (SELECT id FROM sales_orders));

ALTER TABLE payments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payments
    USING (sales_order_id IN (SELECT id FROM sales_orders));

ALTER TABLE refresh_tokens ADD COLUMN merchant_id UUID REFERENCES merchants(id);
-- Backfill is a no-op in any environment that reaches this migration before
-- /auth/refresh ships any real tokens (there is no code path that inserts
-- into refresh_tokens before this change). If that's ever not true, backfill
-- via a join through users before adding the NOT NULL constraint.
ALTER TABLE refresh_tokens ALTER COLUMN merchant_id SET NOT NULL;
CREATE INDEX idx_refresh_tokens_hash ON refresh_tokens(token_hash);

CREATE TABLE sales_order_discounts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    sales_order_id      UUID NOT NULL REFERENCES sales_orders(id) ON DELETE CASCADE,
    type                TEXT NOT NULL CHECK (type IN ('manual','coupon')),
    value_percent       NUMERIC(5,2) NOT NULL,       -- 0-100
    discount_amount     NUMERIC(14,2) NOT NULL,       -- resolved amount at time of application
    authorized_by       UUID REFERENCES users(id),    -- NULL for the 0-5% no-approval tier
    applied_by          UUID NOT NULL REFERENCES users(id),
    reason              TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE sales_order_discounts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sales_order_discounts
    USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
CREATE INDEX idx_sales_order_discounts_order ON sales_order_discounts(sales_order_id);

-- 001_schema.sql's audit_logs is PARTITION BY RANGE (created_at) but never
-- actually created a partition — only a commented-out example of one. That
-- meant every INSERT into audit_logs was silently guaranteed to fail with
-- "no partition of relation found" from the moment the table was created;
-- it just went unnoticed because nothing in Phase 0/1 ever wrote to it
-- until this hardening pass's inventory-adjustment endpoint tried to.
-- A DEFAULT partition is a safety net so writes never fail even if the
-- monthly-partition job falls behind; the current month's partition is
-- created explicitly too so today's rows land in a real bucket instead of
-- the catch-all. Future months still need a scheduled job (or pg_partman)
-- per the original comment at the bottom of 001_schema.sql.
CREATE TABLE audit_logs_default PARTITION OF audit_logs DEFAULT;
CREATE TABLE audit_logs_current PARTITION OF audit_logs
    FOR VALUES FROM (date_trunc('month', now())) TO (date_trunc('month', now()) + interval '1 month');
