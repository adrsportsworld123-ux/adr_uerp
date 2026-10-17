-- =====================================================================
-- Activity log: every action taken from the POS or the web admin, one
-- row per API request (internal/activity), alongside the existing
-- entity audit trail (audit_logs, migrations/021).
--
-- The two answer different questions and are kept apart on purpose:
--   audit_logs     what a record looked like before and after a change.
--                  Hash-chained, written inside the change's own
--                  transaction, ALWAYS on. India's Companies (Accounts)
--                  Rules (rule 3(1), in force since 1 April 2023) require
--                  accounting software to keep an audit trail that cannot
--                  be disabled, so no merchant setting reaches it.
--   activity_logs  who did what, from where: user, POS or web, terminal,
--                  IP, route, outcome (including refused attempts: a 403
--                  on a price override is exactly what an owner wants to
--                  see), duration and the request body with secrets
--                  redacted. This is the part a merchant can configure.
--
-- audit_settings is that configuration, one row per merchant (a missing
-- row means the defaults): on/off, whether reads are logged too (off by
-- default: the web admin's live screens poll), whether request bodies
-- are kept, and modules to leave out. Changing it is itself written to
-- audit_logs, so switching the activity log off is never invisible.
--
-- Like audit_logs, erp_app may only add rows: no UPDATE or DELETE grant.
-- No purge job, same reasoning as migrations/021: deleting compliance
-- data on a schedule is a decision for later, made deliberately.
-- =====================================================================

CREATE TABLE audit_settings (
    merchant_id          UUID PRIMARY KEY REFERENCES merchants(id),
    activity_log_enabled BOOLEAN NOT NULL DEFAULT true,
    log_read_requests    BOOLEAN NOT NULL DEFAULT false,
    capture_request_body BOOLEAN NOT NULL DEFAULT true,
    disabled_modules     TEXT[] NOT NULL DEFAULT '{}',
    updated_by           UUID REFERENCES users(id),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE audit_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON audit_settings USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON audit_settings TO erp_app;

CREATE TABLE activity_logs (
    id           BIGSERIAL PRIMARY KEY,
    merchant_id  UUID NOT NULL REFERENCES merchants(id),
    occurred_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id      UUID,
    branch_id    UUID,
    terminal_id  UUID,
    client       TEXT NOT NULL,          -- pos | web | api
    method       TEXT NOT NULL,
    route        TEXT NOT NULL,          -- the route pattern: /sales/orders/{id}/checkout
    path         TEXT NOT NULL,          -- the concrete path
    module       TEXT NOT NULL,          -- first route segment: sales, purchase, hr, ...
    entity_id    TEXT,                   -- the {id} in the route, when there is one
    status_code  INT NOT NULL,
    duration_ms  INT NOT NULL,
    ip           TEXT,
    user_agent   TEXT,
    request_id   TEXT,
    request_body JSONB                   -- redacted; NULL when not captured
);
CREATE INDEX idx_activity_logs_time   ON activity_logs (merchant_id, occurred_at DESC);
CREATE INDEX idx_activity_logs_user   ON activity_logs (merchant_id, user_id, occurred_at DESC);
CREATE INDEX idx_activity_logs_entity ON activity_logs (merchant_id, entity_id) WHERE entity_id IS NOT NULL;
ALTER TABLE activity_logs ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON activity_logs USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT ON activity_logs TO erp_app;
REVOKE UPDATE, DELETE ON activity_logs FROM erp_app;
GRANT USAGE ON SEQUENCE activity_logs_id_seq TO erp_app;

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000051', 'audit.configure', 'Turn the activity log on or off and choose what it records')
ON CONFLICT (code) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'Merchant Admin' AND p.code = 'audit.configure'
ON CONFLICT DO NOTHING;
