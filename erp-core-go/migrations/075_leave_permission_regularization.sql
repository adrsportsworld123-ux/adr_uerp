-- =====================================================================
-- Leave, permission (a few hours off) and attendance regularization, all
-- asked for by the employee (POS or web) and approved by someone with
-- hr.manage who is not that employee (§3.78).
--
-- Leave: leave types with a yearly quota (Casual 12, Sick 6, Loss of Pay
-- unpaid). An approved request marks each of its days on the attendance
-- row (status 'leave', leave_request_id, leave_days, leave_unpaid_days).
-- Days within the type's balance are paid; days beyond it, and every day
-- of an unpaid type, are unpaid and payroll docks them. A day marked
-- 'leave' by hand (no request) stays paid, exactly as before.
--
-- Permission: an approved request is free while the month's count and
-- minutes are within attendance_settings' limits; minutes beyond them are
-- unpaid, and payroll docks them as a part of a day (the employee's shift
-- length, else standard_day_hours).
--
-- Regularization: a correction of a check-in/out (a missed check-out, a
-- wrong time, a forgotten day). Approval applies it the way HR's own
-- session correction does, with the request named in the reason.
--
-- Nothing changes for anyone who never asks: no request, no effect.
-- =====================================================================

ALTER TABLE attendance_settings
    ADD COLUMN leave_year_start_month  INT NOT NULL DEFAULT 1 CHECK (leave_year_start_month BETWEEN 1 AND 12),
    ADD COLUMN permission_free_count   INT NOT NULL DEFAULT 2 CHECK (permission_free_count BETWEEN 0 AND 31),
    ADD COLUMN permission_free_minutes INT NOT NULL DEFAULT 240 CHECK (permission_free_minutes BETWEEN 0 AND 6000),
    ADD COLUMN permission_max_minutes  INT NOT NULL DEFAULT 240 CHECK (permission_max_minutes BETWEEN 15 AND 720),
    ADD COLUMN standard_day_hours      NUMERIC(4,2) NOT NULL DEFAULT 8 CHECK (standard_day_hours > 0 AND standard_day_hours <= 24),
    ADD COLUMN regularize_within_days  INT NOT NULL DEFAULT 30 CHECK (regularize_within_days BETWEEN 1 AND 366);

CREATE TABLE leave_types (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    code            TEXT NOT NULL,
    name            TEXT NOT NULL,
    paid            BOOLEAN NOT NULL DEFAULT true,
    annual_quota    NUMERIC(5,1) CHECK (annual_quota IS NULL OR annual_quota >= 0),   -- days a leave year; NULL = no limit
    allow_half_day  BOOLEAN NOT NULL DEFAULT true,
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    sort_order      INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_leave_types_code ON leave_types (merchant_id, lower(code));
ALTER TABLE leave_types ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON leave_types USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON leave_types TO erp_app;

INSERT INTO leave_types (merchant_id, code, name, paid, annual_quota, sort_order)
SELECT m.id, v.code, v.name, v.paid, v.quota, v.ord
FROM merchants m
CROSS JOIN (VALUES ('CL', 'Casual leave', true, 12.0, 1), ('SL', 'Sick leave', true, 6.0, 2), ('LOP', 'Loss of pay', false, NULL::numeric, 3))
    AS v(code, name, paid, quota, ord);

-- An employee's own allowance for a year, when it isn't the type's quota
-- (a late joiner, a carried-forward balance).
CREATE TABLE leave_allocations (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id    UUID NOT NULL REFERENCES merchants(id),
    user_id        UUID NOT NULL REFERENCES users(id),
    leave_type_id  UUID NOT NULL REFERENCES leave_types(id),
    leave_year     INT NOT NULL,          -- the year the leave year starts in
    days           NUMERIC(5,1) NOT NULL CHECK (days >= 0),
    note           TEXT,
    set_by         UUID REFERENCES users(id),
    set_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, leave_type_id, leave_year)
);
ALTER TABLE leave_allocations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON leave_allocations USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON leave_allocations TO erp_app;

CREATE TABLE leave_requests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    request_number  TEXT NOT NULL,
    user_id         UUID NOT NULL REFERENCES users(id),
    branch_id       UUID REFERENCES branches(id),     -- the employee's branch when asked
    leave_type_id   UUID NOT NULL REFERENCES leave_types(id),
    leave_year      INT NOT NULL,
    from_date       DATE NOT NULL,
    to_date         DATE NOT NULL,
    half_day        BOOLEAN NOT NULL DEFAULT false,
    days            NUMERIC(5,1) NOT NULL CHECK (days > 0),
    reason          TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled')),
    source          TEXT NOT NULL DEFAULT 'web' CHECK (source IN ('pos', 'web', 'api')),
    requested_by    UUID REFERENCES users(id),        -- HR entering it for the employee, or the employee
    requested_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_days       NUMERIC(5,1),                     -- set on approval
    unpaid_days     NUMERIC(5,1),
    decided_by      UUID REFERENCES users(id),
    decided_at      TIMESTAMPTZ,
    decision_note   TEXT,
    cancelled_by    UUID REFERENCES users(id),
    cancelled_at    TIMESTAMPTZ,
    cancel_reason   TEXT,
    UNIQUE (merchant_id, request_number),
    CHECK (to_date >= from_date),
    CHECK (NOT half_day OR from_date = to_date)
);
CREATE INDEX idx_leave_requests_user ON leave_requests (user_id, from_date);
CREATE INDEX idx_leave_requests_pending ON leave_requests (merchant_id) WHERE status = 'pending';
ALTER TABLE leave_requests ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON leave_requests USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY branch_scope ON leave_requests AS RESTRICTIVE
    USING (branch_id IS NULL OR app_branch_visible(branch_id)) WITH CHECK (branch_id IS NULL OR app_branch_visible(branch_id));
GRANT SELECT, INSERT, UPDATE ON leave_requests TO erp_app;

-- A day an approved leave covers. leave_prev_status: what the row said
-- before (NULL = the leave created the row), so cancelling puts it back.
ALTER TABLE attendance
    ADD COLUMN leave_request_id   UUID REFERENCES leave_requests(id),
    ADD COLUMN leave_days         NUMERIC(3,1) CHECK (leave_days IN (0.5, 1)),
    ADD COLUMN leave_unpaid_days  NUMERIC(3,1) NOT NULL DEFAULT 0 CHECK (leave_unpaid_days >= 0 AND leave_unpaid_days <= 1),
    ADD COLUMN leave_prev_status  TEXT;
GRANT DELETE ON attendance TO erp_app;

CREATE TABLE permission_requests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    request_number  TEXT NOT NULL,
    user_id         UUID NOT NULL REFERENCES users(id),
    branch_id       UUID REFERENCES branches(id),
    work_date       DATE NOT NULL,
    from_time       TIME NOT NULL,
    to_time         TIME NOT NULL,
    minutes         INT NOT NULL CHECK (minutes > 0),
    reason          TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled')),
    source          TEXT NOT NULL DEFAULT 'web' CHECK (source IN ('pos', 'web', 'api')),
    requested_by    UUID REFERENCES users(id),
    requested_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_minutes    INT,                              -- set on approval
    unpaid_minutes  INT,
    decided_by      UUID REFERENCES users(id),
    decided_at      TIMESTAMPTZ,
    decision_note   TEXT,
    cancelled_by    UUID REFERENCES users(id),
    cancelled_at    TIMESTAMPTZ,
    cancel_reason   TEXT,
    UNIQUE (merchant_id, request_number),
    CHECK (to_time > from_time)
);
CREATE INDEX idx_permission_requests_user ON permission_requests (user_id, work_date);
ALTER TABLE permission_requests ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON permission_requests USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY branch_scope ON permission_requests AS RESTRICTIVE
    USING (branch_id IS NULL OR app_branch_visible(branch_id)) WITH CHECK (branch_id IS NULL OR app_branch_visible(branch_id));
GRANT SELECT, INSERT, UPDATE ON permission_requests TO erp_app;

-- session_id set: correct that check-in/out session (clock_in and/or
-- clock_out). NULL: add a session (both times) on work_date.
CREATE TABLE attendance_regularizations (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id        UUID NOT NULL REFERENCES merchants(id),
    request_number     TEXT NOT NULL,
    user_id            UUID NOT NULL REFERENCES users(id),
    branch_id          UUID REFERENCES branches(id),
    work_date          DATE NOT NULL,
    session_id         UUID REFERENCES attendance_sessions(id),
    clock_in           TIMESTAMPTZ,
    clock_out          TIMESTAMPTZ,
    before_clock_in    TIMESTAMPTZ,                   -- the session as it was when asked
    before_clock_out   TIMESTAMPTZ,
    reason             TEXT NOT NULL,
    status             TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled')),
    source             TEXT NOT NULL DEFAULT 'web' CHECK (source IN ('pos', 'web', 'api')),
    requested_by       UUID REFERENCES users(id),
    requested_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    applied_session_id UUID REFERENCES attendance_sessions(id),
    decided_by         UUID REFERENCES users(id),
    decided_at         TIMESTAMPTZ,
    decision_note      TEXT,
    cancelled_by       UUID REFERENCES users(id),
    cancelled_at       TIMESTAMPTZ,
    cancel_reason      TEXT,
    UNIQUE (merchant_id, request_number),
    CHECK (clock_in IS NOT NULL OR clock_out IS NOT NULL),
    CHECK (session_id IS NOT NULL OR (clock_in IS NOT NULL AND clock_out IS NOT NULL)),
    CHECK (clock_in IS NULL OR clock_out IS NULL OR clock_out > clock_in)
);
CREATE INDEX idx_attendance_regularizations_user ON attendance_regularizations (user_id, work_date);
ALTER TABLE attendance_regularizations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON attendance_regularizations USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY branch_scope ON attendance_regularizations AS RESTRICTIVE
    USING (branch_id IS NULL OR app_branch_visible(branch_id)) WITH CHECK (branch_id IS NULL OR app_branch_visible(branch_id));
GRANT SELECT, INSERT, UPDATE ON attendance_regularizations TO erp_app;

-- What a payslip shows of leave and permission (0 on every existing one).
ALTER TABLE payslips
    ADD COLUMN leave_paid_days          NUMERIC(5,1) NOT NULL DEFAULT 0,
    ADD COLUMN leave_unpaid_days        NUMERIC(5,1) NOT NULL DEFAULT 0,
    ADD COLUMN permission_unpaid_minutes INT NOT NULL DEFAULT 0,
    ADD COLUMN absent_days              NUMERIC(5,1) NOT NULL DEFAULT 0;

-- Request numbers (LV-00001, PR-00001, RG-00001), one counter per merchant
-- and kind: a branch-scoped reader can't see every row to count them.
CREATE TABLE hr_request_counters (
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    kind        TEXT NOT NULL,
    last_no     INT NOT NULL,
    PRIMARY KEY (merchant_id, kind)
);
ALTER TABLE hr_request_counters ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON hr_request_counters USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON hr_request_counters TO erp_app;
