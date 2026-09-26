-- =====================================================================
-- Phase 5: HR & Payroll (phased_roadmap.md; pos_frd_complete.md's HR
-- section: employee records, attendance/shift management, payroll run
-- with statutory deductions, commission engine).
--
-- Employee records are NOT a new parallel table. `users` (001_schema.sql)
-- already IS this system's staff record — it has employee_code, name,
-- phone, email, branch_id, and a PIN/password login, and there was
-- already no way to create one outside a migration (no POST /users has
-- ever existed, a real pre-existing gap this phase closes as a byproduct:
-- internal/hr's POST /hr/employees is the first user-creation endpoint
-- this codebase has ever had). Adding a separate `employees` table would
-- just duplicate name/phone/employee_code and create two sources of
-- truth for the same real person — so this migration extends `users`
-- with HR-specific columns instead. An employee who needs no system
-- login (e.g. a warehouse loader) is simply a `users` row with no
-- password_hash/pin_hash set, which the schema already allowed.
--
-- Honesty up front on statutory rates, matching internal/gst's own
-- header comment's posture: PF/ESI/PT/LWF are real statutory concepts,
-- but rates/ceilings change over time and PT/LWF vary by state.
-- statutory_config is one merchant-editable row, seeded below with
-- reasonable current-as-of-2026 defaults for Karnataka (the seed
-- merchant's own state) — verify against the actual current EPFO/ESIC/
-- state PT notification before relying on this for a real filing.
--
-- TDS (income tax withheld on salary) is deliberately NOT modeled as a
-- real slab/regime/investment-declaration computation — that is an
-- entire product category on its own (Form 16, old vs. new regime, 80C
-- declarations, Section 87A rebate). tds_rate_percent is a flat
-- percentage of gross pay, defaulting to 0 (disabled), so no merchant
-- silently gets a wrong TDS number deducted; a real deployment needs a
-- real TDS computation tool or a CA for this line — the same
-- "don't build what you can configure or buy" boundary already drawn
-- for e-invoicing's GSP integration (migrations/022_einvoice.sql).
-- =====================================================================

CREATE TABLE shifts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    branch_id           UUID REFERENCES branches(id),
    name                TEXT NOT NULL,
    start_time          TIME NOT NULL,
    end_time            TIME NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE users
    ADD COLUMN date_of_joining   DATE,
    ADD COLUMN date_of_exit      DATE,
    ADD COLUMN designation       TEXT,
    ADD COLUMN department        TEXT,
    ADD COLUMN employment_type   TEXT NOT NULL DEFAULT 'full_time' CHECK (employment_type IN ('full_time','part_time','contract')),
    ADD COLUMN pan_number        TEXT,
    ADD COLUMN uan_number        TEXT,          -- EPF Universal Account Number
    ADD COLUMN esi_number        TEXT,
    ADD COLUMN bank_account_no   TEXT,
    ADD COLUMN bank_ifsc         TEXT,
    ADD COLUMN shift_id          UUID REFERENCES shifts(id);

CREATE TABLE salary_structures (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    user_id             UUID NOT NULL REFERENCES users(id),
    effective_from      DATE NOT NULL,
    basic               NUMERIC(14,2) NOT NULL,
    hra                 NUMERIC(14,2) NOT NULL DEFAULT 0,
    special_allowance   NUMERIC(14,2) NOT NULL DEFAULT 0,
    other_allowances    NUMERIC(14,2) NOT NULL DEFAULT 0,
    pf_applicable       BOOLEAN NOT NULL DEFAULT true,   -- an explicit per-employee flag, not auto-derived: real PF mandatory-enrollment rules (new joiner wage threshold vs. already-enrolled continuity) are a genuine HR/legal judgment call, not something to compute wrongly by guessing
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, effective_from)
);

CREATE TABLE attendance (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    user_id             UUID NOT NULL REFERENCES users(id),
    branch_id           UUID REFERENCES branches(id),
    work_date           DATE NOT NULL,
    clock_in            TIMESTAMPTZ,
    clock_out           TIMESTAMPTZ,
    status              TEXT NOT NULL DEFAULT 'present' CHECK (status IN ('present','half_day','leave','absent')),
    hours_worked        NUMERIC(5,2),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, work_date)
);

CREATE TABLE statutory_config (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id             UUID NOT NULL UNIQUE REFERENCES merchants(id),
    pf_employee_rate        NUMERIC(5,2) NOT NULL DEFAULT 12.00,
    pf_employer_rate        NUMERIC(5,2) NOT NULL DEFAULT 12.00,
    pf_wage_ceiling         NUMERIC(14,2) NOT NULL DEFAULT 15000.00,
    esi_employee_rate       NUMERIC(5,2) NOT NULL DEFAULT 0.75,
    esi_employer_rate       NUMERIC(5,2) NOT NULL DEFAULT 3.25,
    esi_wage_ceiling        NUMERIC(14,2) NOT NULL DEFAULT 21000.00,
    pt_slabs                JSONB NOT NULL DEFAULT '[]',   -- [{"min":0,"max":15000,"amount":0}, {"min":15000,"max":null,"amount":200}] — ascending by min, max:null means unbounded
    lwf_employee_amount     NUMERIC(14,2) NOT NULL DEFAULT 0,
    lwf_employer_amount     NUMERIC(14,2) NOT NULL DEFAULT 0,
    tds_rate_percent        NUMERIC(5,2) NOT NULL DEFAULT 0,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE commission_rules (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    name                TEXT NOT NULL,
    category_id         UUID REFERENCES categories(id),   -- NULL = applies to a cashier's total net sales across all categories ("the default rule"); set = category-weighted, applies only to that category's share
    tiers               JSONB NOT NULL,                   -- [{"min_net_sales":0,"rate_percent":1}, {"min_net_sales":50000,"rate_percent":2}] ascending by min_net_sales — the highest threshold the period's net sales clears sets the rate for the WHOLE period's net sales (a tier, not a marginal bracket)
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE commission_earnings (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    user_id             UUID NOT NULL REFERENCES users(id),
    rule_id             UUID NOT NULL REFERENCES commission_rules(id),
    period_month        INT NOT NULL CHECK (period_month BETWEEN 1 AND 12),
    period_year         INT NOT NULL,
    net_sales           NUMERIC(14,2) NOT NULL,
    commission_amount   NUMERIC(14,2) NOT NULL,
    computed_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, rule_id, period_month, period_year)
);

CREATE TABLE payroll_runs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    period_month        INT NOT NULL CHECK (period_month BETWEEN 1 AND 12),
    period_year         INT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','finalized')),
    total_gross         NUMERIC(14,2) NOT NULL DEFAULT 0,
    total_deductions    NUMERIC(14,2) NOT NULL DEFAULT 0,
    total_net           NUMERIC(14,2) NOT NULL DEFAULT 0,
    created_by          UUID REFERENCES users(id),
    finalized_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, period_month, period_year)
);

CREATE TABLE payslips (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    payroll_run_id      UUID NOT NULL REFERENCES payroll_runs(id) ON DELETE CASCADE,
    user_id             UUID NOT NULL REFERENCES users(id),
    basic               NUMERIC(14,2) NOT NULL,
    hra                 NUMERIC(14,2) NOT NULL,
    special_allowance   NUMERIC(14,2) NOT NULL,
    other_allowances    NUMERIC(14,2) NOT NULL,
    commission_amount   NUMERIC(14,2) NOT NULL DEFAULT 0,
    gross_earnings      NUMERIC(14,2) NOT NULL,
    days_in_period      NUMERIC(5,2) NOT NULL,
    days_present        NUMERIC(5,2) NOT NULL,
    pf_employee         NUMERIC(14,2) NOT NULL DEFAULT 0,
    pf_employer         NUMERIC(14,2) NOT NULL DEFAULT 0,
    esi_employee        NUMERIC(14,2) NOT NULL DEFAULT 0,
    esi_employer        NUMERIC(14,2) NOT NULL DEFAULT 0,
    pt_amount           NUMERIC(14,2) NOT NULL DEFAULT 0,
    tds_amount          NUMERIC(14,2) NOT NULL DEFAULT 0,
    lwf_employee        NUMERIC(14,2) NOT NULL DEFAULT 0,
    lwf_employer        NUMERIC(14,2) NOT NULL DEFAULT 0,
    total_deductions    NUMERIC(14,2) NOT NULL,
    net_pay             NUMERIC(14,2) NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (payroll_run_id, user_id)
);

ALTER TABLE shifts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON shifts USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON shifts TO erp_app;

ALTER TABLE salary_structures ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON salary_structures USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON salary_structures TO erp_app;

ALTER TABLE attendance ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON attendance USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON attendance TO erp_app;

ALTER TABLE statutory_config ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON statutory_config USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON statutory_config TO erp_app;

ALTER TABLE commission_rules ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON commission_rules USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON commission_rules TO erp_app;

ALTER TABLE commission_earnings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON commission_earnings USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON commission_earnings TO erp_app;

ALTER TABLE payroll_runs ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payroll_runs USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON payroll_runs TO erp_app;

ALTER TABLE payslips ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payslips USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON payslips TO erp_app;

-- Two new permissions: hr.manage (employee records, shifts, attendance
-- corrections — operational, same tier as inventory.adjust/credit.manage)
-- and payroll.manage (salary structures, statutory config, commission
-- rules, running payroll, viewing any employee's payslip — compensation
-- data, gated Merchant-Admin-only same as audit.view's sensitivity call).
INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-00000000000f', 'hr.manage', 'Create/update employee records, shifts, and correct attendance'),
    ('f0000000-0000-0000-0000-000000000010', 'payroll.manage', 'Manage salary structures, statutory config, commission rules, and run payroll')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.merchant_id = '11111111-1111-1111-1111-111111111111'
  AND r.name IN ('Branch Manager', 'Merchant Admin')
  AND p.code = 'hr.manage'
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.merchant_id = '11111111-1111-1111-1111-111111111111'
  AND r.name = 'Merchant Admin'
  AND p.code = 'payroll.manage'
ON CONFLICT DO NOTHING;

-- Seed data: give the three existing seed users a join date (payroll
-- pro-rating needs one; NULL is still handled as "employed for the
-- whole period" for any real deployment's pre-existing staff who never
-- get a backfilled date) and a Karnataka-shaped statutory config so
-- payroll actually has something to compute against out of the box in
-- this dev environment.
UPDATE users SET date_of_joining = '2024-01-01', designation = 'Cashier', department = 'Sales'
    WHERE id = '55555555-5555-5555-5555-555555555555';
UPDATE users SET date_of_joining = '2023-06-01', designation = 'Branch Manager', department = 'Operations'
    WHERE id = 'cccccccc-cccc-cccc-cccc-cccccccccccc';
UPDATE users SET date_of_joining = '2022-01-01', designation = 'Merchant Admin', department = 'Management'
    WHERE id = 'eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee';

INSERT INTO statutory_config (id, merchant_id, pt_slabs)
VALUES ('f0000000-0000-0000-0000-0000000000f0', '11111111-1111-1111-1111-111111111111',
        '[{"min":0,"max":15000,"amount":0},{"min":15000,"max":null,"amount":200}]'::jsonb)
ON CONFLICT (merchant_id) DO NOTHING;
