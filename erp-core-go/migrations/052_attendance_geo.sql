-- =====================================================================
-- Attendance: check-in / check-out from the POS and the web admin with
-- the evidence a punch needs to be trusted — where (device GPS:
-- latitude, longitude and the fix's accuracy), from what network (the
-- IP the server saw, not one the client claims), on which device (POS
-- terminal, user agent) — plus branch geofences and per-merchant rules.
--
-- migrations/023 already has one attendance row per employee per day and
-- self-service clock-in/out (internal/hr/attendance.go); payroll counts
-- those rows. This keeps that model and adds to it: payroll is untouched.
--
-- Geofence (branch_geofences): a branch's location and radius. A punch
-- is "inside" when it is within the radius allowing for the fix's own
-- accuracy (a phone that says ±40 m, 230 m from a 200 m fence, gets the
-- benefit of the doubt), "outside" otherwise, "unknown" without a
-- location, "not_configured" when the branch has no fence. What happens
-- to an outside punch is the merchant's choice (attendance_settings.
-- geofence_mode): off, flag it for the manager (the default — a real
-- shop's GPS is often poor indoors, and blocking a cashier from clocking
-- in at the counter is worse than a flag), or block it.
--
-- A POS terminal is itself evidence: it is paired to one branch, so a
-- punch from a till carries that terminal, and a desktop till without
-- GPS is still placed.
--
-- HR corrections and manual marks (leave, a forgotten punch) record who
-- made them and why, on the row and in audit_logs.
-- =====================================================================

CREATE TABLE attendance_settings (
    merchant_id            UUID PRIMARY KEY REFERENCES merchants(id),
    require_location       BOOLEAN NOT NULL DEFAULT false,
    geofence_mode          TEXT NOT NULL DEFAULT 'flag' CHECK (geofence_mode IN ('off', 'flag', 'block')),
    allow_web              BOOLEAN NOT NULL DEFAULT true,
    allow_pos              BOOLEAN NOT NULL DEFAULT true,
    updated_by             UUID REFERENCES users(id),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE attendance_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON attendance_settings USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON attendance_settings TO erp_app;

CREATE TABLE branch_geofences (
    branch_id   UUID PRIMARY KEY REFERENCES branches(id),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    latitude    NUMERIC(9,6) NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude   NUMERIC(9,6) NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    radius_m    INT NOT NULL CHECK (radius_m BETWEEN 10 AND 5000),
    enabled     BOOLEAN NOT NULL DEFAULT true,
    updated_by  UUID REFERENCES users(id),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE branch_geofences ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON branch_geofences USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON branch_geofences TO erp_app;

ALTER TABLE attendance
    ADD COLUMN clock_in_latitude     NUMERIC(9,6),
    ADD COLUMN clock_in_longitude    NUMERIC(9,6),
    ADD COLUMN clock_in_accuracy_m   NUMERIC(8,1),
    ADD COLUMN clock_in_ip           TEXT,
    ADD COLUMN clock_in_user_agent   TEXT,
    ADD COLUMN clock_in_source       TEXT CHECK (clock_in_source IN ('pos', 'web', 'api', 'manual')),
    ADD COLUMN clock_in_terminal_id  UUID REFERENCES pos_terminals(id),
    ADD COLUMN clock_in_distance_m   INT,
    ADD COLUMN clock_in_geofence     TEXT CHECK (clock_in_geofence IN ('inside', 'outside', 'unknown', 'not_configured')),
    ADD COLUMN clock_out_latitude    NUMERIC(9,6),
    ADD COLUMN clock_out_longitude   NUMERIC(9,6),
    ADD COLUMN clock_out_accuracy_m  NUMERIC(8,1),
    ADD COLUMN clock_out_ip          TEXT,
    ADD COLUMN clock_out_user_agent  TEXT,
    ADD COLUMN clock_out_source      TEXT CHECK (clock_out_source IN ('pos', 'web', 'api', 'manual')),
    ADD COLUMN clock_out_terminal_id UUID REFERENCES pos_terminals(id),
    ADD COLUMN clock_out_distance_m  INT,
    ADD COLUMN clock_out_geofence    TEXT CHECK (clock_out_geofence IN ('inside', 'outside', 'unknown', 'not_configured')),
    ADD COLUMN corrected_by          UUID REFERENCES users(id),
    ADD COLUMN corrected_at          TIMESTAMPTZ,
    ADD COLUMN correction_reason     TEXT;

-- The day register (GET /hr/attendance/register) reads a date across a
-- branch; UNIQUE (user_id, work_date) already serves the per-employee reads.
CREATE INDEX IF NOT EXISTS idx_attendance_date ON attendance (merchant_id, work_date, branch_id);
