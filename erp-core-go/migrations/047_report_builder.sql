-- =====================================================================
-- Phase 16: the custom report builder (FRD §15 "Custom Builder: select
-- fields, filters, groupings", export; phased_roadmap.md Phase 16 "report
-- template engine"). Users never write SQL: a template is a JSON spec over
-- an allow-listed semantic layer (internal/reportbuilder), compiled to a
-- parameterised query that runs under the caller's tenant and branch scope
-- (RLS), with a date-span, row and statement-time limit. Exports run as
-- background jobs (report_runs) and re-resolve the requester's branch
-- scope when they run.
-- =====================================================================

CREATE TABLE report_templates (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    name        TEXT NOT NULL,
    description TEXT,
    spec        JSONB NOT NULL,
    shared      BOOLEAN NOT NULL DEFAULT false,  -- visible to everyone in the merchant who can run reports
    created_by  UUID NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX report_templates_merchant ON report_templates (merchant_id, name);
ALTER TABLE report_templates ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON report_templates USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

CREATE TABLE report_runs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id  UUID NOT NULL REFERENCES merchants(id),
    template_id  UUID REFERENCES report_templates(id) ON DELETE SET NULL,
    name         TEXT NOT NULL,
    spec         JSONB NOT NULL,
    format       TEXT NOT NULL CHECK (format IN ('csv', 'xlsx')),
    status       TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'done', 'failed')),
    row_count    INT,
    truncated    BOOLEAN NOT NULL DEFAULT false,
    error        TEXT,
    result       BYTEA,
    requested_by UUID NOT NULL REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ
);
CREATE INDEX report_runs_merchant ON report_runs (merchant_id, requested_by, created_at DESC);
CREATE INDEX report_runs_queued ON report_runs (status, created_at) WHERE status IN ('queued', 'running');
ALTER TABLE report_runs ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON report_runs USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON report_templates, report_runs TO erp_app;

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000047', 'reports.build', 'Build, save and share custom report templates')
ON CONFLICT (code) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name IN ('Merchant Admin', 'Branch Manager') AND p.code = 'reports.build'
ON CONFLICT DO NOTHING;
INSERT INTO role_templates (role_name, permission_code) VALUES ('Branch Manager', 'reports.build') ON CONFLICT DO NOTHING;
