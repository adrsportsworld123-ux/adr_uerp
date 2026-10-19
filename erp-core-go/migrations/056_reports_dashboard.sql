-- =====================================================================
-- Reporting round (phase0_1_design.md §3.60–§3.63):
--
-- 1. Day close (Z report, internal/sales/day_report.go). The X report is
--    one till's shift (pos_shifts, live while open, frozen at its close);
--    the Z report is one branch's whole business day, every till and
--    shift. Closing the day freezes its Z report here with the branch's
--    next Z number, so a reprint is always the same document, and a sale
--    synced late for a closed day shows as a difference instead of quietly
--    changing a printed report.
-- 2. The sales dashboard (internal/dashboard): which widgets each role
--    sees (no row = every widget), and monthly sales targets for the
--    "sales tracking" widget (branch_id NULL = the whole business).
-- 3. The standard report library (internal/reportbuilder library.go): a
--    saved report or an export made from a library report remembers which
--    one (library_key), so it runs on every plan, not only the ones with
--    the custom builder.
-- =====================================================================

CREATE TABLE z_reports (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id   UUID NOT NULL REFERENCES merchants(id),
    branch_id     UUID NOT NULL REFERENCES branches(id),
    business_date DATE NOT NULL,
    z_number      INT NOT NULL,
    report        JSONB NOT NULL,
    closed_by     UUID NOT NULL REFERENCES users(id),
    closed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (branch_id, business_date),
    UNIQUE (branch_id, z_number)
);
CREATE INDEX z_reports_branch_date ON z_reports (merchant_id, branch_id, business_date DESC);
ALTER TABLE z_reports ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON z_reports USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY branch_scope ON z_reports AS RESTRICTIVE USING (app_branch_visible(branch_id)) WITH CHECK (app_branch_visible(branch_id));
GRANT SELECT, INSERT ON z_reports TO erp_app;

CREATE TABLE dashboard_role_widgets (
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    role_id     UUID NOT NULL REFERENCES roles(id),
    widgets     TEXT[] NOT NULL,          -- in display order
    updated_by  UUID REFERENCES users(id),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (merchant_id, role_id)
);
ALTER TABLE dashboard_role_widgets ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON dashboard_role_widgets USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON dashboard_role_widgets TO erp_app;

CREATE TABLE sales_targets (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id    UUID NOT NULL REFERENCES merchants(id),
    branch_id      UUID REFERENCES branches(id),   -- NULL = the whole business
    monthly_target NUMERIC(14,2) NOT NULL CHECK (monthly_target > 0),
    updated_by     UUID REFERENCES users(id),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX sales_targets_one ON sales_targets (merchant_id, COALESCE(branch_id, '00000000-0000-0000-0000-000000000000'::uuid));
ALTER TABLE sales_targets ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sales_targets USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON sales_targets TO erp_app;

ALTER TABLE report_templates ADD COLUMN IF NOT EXISTS library_key TEXT;
ALTER TABLE report_runs ADD COLUMN IF NOT EXISTS library_key TEXT;

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000056', 'reports.day_close', 'Close a branch''s business day (freeze its Z report)'),
    ('f0000000-0000-0000-0000-000000000057', 'dashboard.view', 'See the sales dashboard (charts and sales tracking)'),
    ('f0000000-0000-0000-0000-000000000058', 'dashboard.configure', 'Choose which dashboard widgets each role sees, and set sales targets')
ON CONFLICT (code) DO NOTHING;

-- Admins get all three; a Branch Manager closes their branch's day. The
-- dashboard is the admin's to hand out (Roles & Permissions).
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE (r.name = 'Merchant Admin' AND p.code IN ('reports.day_close', 'dashboard.view', 'dashboard.configure'))
   OR (r.name = 'Branch Manager' AND p.code = 'reports.day_close')
ON CONFLICT DO NOTHING;
-- (A new merchant's Merchant Admin gets every permission at onboarding.)
INSERT INTO role_templates (role_name, permission_code) VALUES ('Branch Manager', 'reports.day_close') ON CONFLICT DO NOTHING;
