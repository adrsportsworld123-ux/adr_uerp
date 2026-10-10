-- =====================================================================
-- Phase 16: SaaS subscriptions and entitlements (phased_roadmap.md
-- Phase 16; FRD §19). Decisions on record (2026-10-11):
--   * the four FRD §19 tiers, as written, are the plans; names, prices,
--     limits and features are data here, editable from the SaaS console;
--   * a plan's retention limits only how far back on-screen reports reach,
--     never deletes anything: invoices, ledgers, GST and the audit log stay
--     readable and exportable on every plan;
--   * existing merchants are grandfathered on Enterprise with no expiry.
--
-- Expiry means read-only, never deletion (decision 2): an expired merchant
-- can sign in, read and export everything, and pay; it can't create new
-- business. internal/subscriptions computes the effective status from
-- these columns on every read, so enforcement never waits for the sweeper.
-- =====================================================================

-- The plan catalogue: platform-owned, the same for every merchant, so no
-- merchant_id and no RLS (like merchants itself). Only the SaaS console
-- writes it. limits: {branches, pos_terminals, products,
-- transactions_per_month}, a missing or null key = unlimited.
CREATE TABLE plans (
    code                TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    price_per_pos_month NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (price_per_pos_month >= 0),
    limits              JSONB NOT NULL DEFAULT '{}'::jsonb,
    features            TEXT[] NOT NULL DEFAULT '{}',
    retention_days      INT CHECK (retention_days IS NULL OR retention_days > 0), -- NULL = lifetime
    trial_days          INT NOT NULL DEFAULT 0 CHECK (trial_days >= 0),
    grace_days          INT NOT NULL DEFAULT 7 CHECK (grace_days >= 0),
    sort_order          INT NOT NULL DEFAULT 0,
    active              BOOLEAN NOT NULL DEFAULT true,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO plans (code, name, price_per_pos_month, limits, features, retention_days, trial_days, grace_days, sort_order) VALUES
    ('freemium', 'Freemium', 0,
        '{"branches": 1, "pos_terminals": 1, "products": 100, "transactions_per_month": 500}',
        '{basic_reports}', 30, 0, 0, 1),
    ('starter', 'Starter', 999,
        '{"branches": 3, "pos_terminals": 5, "products": 5000, "transactions_per_month": null}',
        '{basic_reports,gst_compliance}', 365, 14, 7, 2),
    ('professional', 'Professional', 1999,
        '{"branches": 10, "pos_terminals": 25, "products": null, "transactions_per_month": null}',
        '{basic_reports,gst_compliance,advanced_reports,custom_report_builder,loyalty_crm,api_access}', 1095, 14, 7, 3),
    ('enterprise', 'Enterprise', 3999,
        '{}',
        '{basic_reports,gst_compliance,advanced_reports,custom_report_builder,loyalty_crm,api_access,payroll,multi_currency,white_label,custom_integrations}', NULL, 14, 7, 4);

-- One subscription per merchant. A merchant with no row is treated as
-- Freemium, active (internal/subscriptions.Load), the safe default.
CREATE TABLE subscriptions (
    merchant_id        UUID PRIMARY KEY REFERENCES merchants(id),
    plan_code          TEXT NOT NULL REFERENCES plans(code),
    status             TEXT NOT NULL DEFAULT 'active'
                           CHECK (status IN ('trial', 'active', 'grace', 'expired', 'cancelled')),
    trial_ends_at      TIMESTAMPTZ,
    trial_used         BOOLEAN NOT NULL DEFAULT false,
    current_period_end TIMESTAMPTZ,          -- NULL = no expiry (free plans, grandfathered merchants)
    grace_ends_at      TIMESTAMPTZ,
    read_only_since    TIMESTAMPTZ,
    notes              TEXT,
    updated_by         UUID,                 -- a user or a platform_staff id
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE subscriptions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON subscriptions USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

-- Billing records. The payment itself goes through a provider interface
-- (internal/subscriptions.Provider); the stub records a reference and
-- makes no network call, like the GSP stub in Phase 4.
CREATE TABLE billing_invoices (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id    UUID NOT NULL REFERENCES merchants(id),
    invoice_number TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('renewal', 'plan_change', 'manual')),
    plan_code      TEXT NOT NULL REFERENCES plans(code),
    period_start   TIMESTAMPTZ NOT NULL,
    period_end     TIMESTAMPTZ NOT NULL,
    pos_count      INT NOT NULL CHECK (pos_count >= 1),
    unit_price     NUMERIC(14,2) NOT NULL,
    amount         NUMERIC(14,2) NOT NULL CHECK (amount >= 0),
    status         TEXT NOT NULL DEFAULT 'due' CHECK (status IN ('due', 'paid', 'void')),
    due_at         TIMESTAMPTZ NOT NULL,
    paid_at        TIMESTAMPTZ,
    provider       TEXT,
    provider_ref   TEXT,
    paid_by        UUID,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, invoice_number)
);
CREATE INDEX billing_invoices_merchant_status ON billing_invoices (merchant_id, status);
-- One renewal invoice per merchant and period, so a sweeper retry can't bill twice.
CREATE UNIQUE INDEX billing_invoices_one_renewal ON billing_invoices (merchant_id, period_start) WHERE kind = 'renewal' AND status <> 'void';
ALTER TABLE billing_invoices ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON billing_invoices USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

-- Platform staff: the SaaS operator's own people, outside every tenant. A
-- platform token carries no tenant_id, so it can't reach tenant data
-- (WithTenant fails closed on an empty tenant), and tenant tokens can't
-- reach /platform routes.
CREATE TABLE platform_staff (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email                 TEXT NOT NULL UNIQUE,
    name                  TEXT NOT NULL,
    password_hash         TEXT NOT NULL,
    status                TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    failed_login_attempts INT NOT NULL DEFAULT 0,
    locked_until          TIMESTAMPTZ,
    last_login_at         TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Set a real password with POST /dev/set-platform-password (dev only) or
-- by writing a bcrypt hash; the placeholder never verifies.
INSERT INTO platform_staff (id, email, name, password_hash) VALUES
    ('a0000000-0000-0000-0000-000000000001', 'ops@adrsw.com', 'Platform Operator', '$2a$10$placeholderplaceholderplaceholderplaceholderplace');

-- What platform staff did (plans, subscriptions, invoices), append-only by
-- convention like audit_logs. Changes to one merchant are also written to
-- that merchant's own audit log.
CREATE TABLE platform_audit_log (
    id           BIGSERIAL PRIMARY KEY,
    staff_id     UUID NOT NULL REFERENCES platform_staff(id),
    action       TEXT NOT NULL,
    target_type  TEXT NOT NULL,
    target_id    TEXT NOT NULL,
    before_value JSONB,
    after_value  JSONB,
    reason       TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

GRANT SELECT, UPDATE ON plans TO erp_app;
GRANT SELECT, INSERT, UPDATE ON subscriptions, billing_invoices, platform_staff TO erp_app;
GRANT SELECT, INSERT ON platform_audit_log TO erp_app;
GRANT USAGE, SELECT ON SEQUENCE platform_audit_log_id_seq TO erp_app;

-- Existing merchants: grandfathered on Enterprise, no expiry.
INSERT INTO subscriptions (merchant_id, plan_code, status, notes)
SELECT id, 'enterprise', 'active', 'Grandfathered at Phase 16 launch' FROM merchants
ON CONFLICT (merchant_id) DO NOTHING;
UPDATE merchants SET subscription_tier = 'enterprise', updated_at = now();

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000046', 'subscription.manage', 'See the subscription, usage and billing; change plan; pay invoices')
ON CONFLICT (code) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'Merchant Admin' AND p.code = 'subscription.manage'
ON CONFLICT DO NOTHING;

-- Merchant onboarding from the SaaS console (internal/subscriptions
-- ProvisionMerchant). Merchant Admin gets every permission; the other
-- system roles start from these templates, captured from the oldest
-- merchant's roles at this migration (each merchant can edit its own
-- roles afterwards). A later migration that grants a new permission to
-- Branch Manager or POS User should add it here too.
CREATE TABLE role_templates (
    role_name       TEXT NOT NULL,
    permission_code TEXT NOT NULL REFERENCES permissions(code),
    PRIMARY KEY (role_name, permission_code)
);
GRANT SELECT ON role_templates TO erp_app;
INSERT INTO role_templates (role_name, permission_code)
SELECT r.name, p.code
FROM roles r
JOIN role_permissions rp ON rp.role_id = r.id
JOIN permissions p ON p.id = rp.permission_id
WHERE r.name IN ('Branch Manager', 'POS User')
  AND r.merchant_id = (SELECT id FROM merchants ORDER BY created_at LIMIT 1)
ON CONFLICT DO NOTHING;
