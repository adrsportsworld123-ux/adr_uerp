-- =====================================================================
-- Several check-ins and check-outs per day: a lunch break, a bank run, a
-- split shift. Each check-in/check-out pair is an attendance_sessions row
-- carrying its own evidence (location, accuracy, IP, device, terminal,
-- geofence — the columns migrations/052 put on attendance).
--
-- attendance keeps one row per employee per day as the day's summary,
-- so payroll (which counts those rows' status) and every existing read
-- are unchanged:
--   clock_in      the first session's check-in (and its evidence)
--   clock_out     the last session's check-out (and its evidence);
--                 NULL while a session is still open
--   hours_worked  the sum of the closed sessions — breaks between
--                 sessions are not worked time
-- internal/hr/attendance.go recomputes the summary from the sessions
-- whenever one changes (recalcDay).
--
-- Every existing punch becomes that day's single session.
-- =====================================================================

CREATE TABLE attendance_sessions (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id           UUID NOT NULL REFERENCES merchants(id),
    attendance_id         UUID NOT NULL REFERENCES attendance(id),
    user_id               UUID NOT NULL REFERENCES users(id),
    branch_id             UUID REFERENCES branches(id),
    clock_in              TIMESTAMPTZ NOT NULL,
    clock_out             TIMESTAMPTZ,
    clock_in_latitude     NUMERIC(9,6),
    clock_in_longitude    NUMERIC(9,6),
    clock_in_accuracy_m   NUMERIC(8,1),
    clock_in_ip           TEXT,
    clock_in_user_agent   TEXT,
    clock_in_source       TEXT CHECK (clock_in_source IN ('pos', 'web', 'api', 'manual')),
    clock_in_terminal_id  UUID REFERENCES pos_terminals(id),
    clock_in_distance_m   INT,
    clock_in_geofence     TEXT CHECK (clock_in_geofence IN ('inside', 'outside', 'unknown', 'not_configured')),
    clock_out_latitude    NUMERIC(9,6),
    clock_out_longitude   NUMERIC(9,6),
    clock_out_accuracy_m  NUMERIC(8,1),
    clock_out_ip          TEXT,
    clock_out_user_agent  TEXT,
    clock_out_source      TEXT CHECK (clock_out_source IN ('pos', 'web', 'api', 'manual')),
    clock_out_terminal_id UUID REFERENCES pos_terminals(id),
    clock_out_distance_m  INT,
    clock_out_geofence    TEXT CHECK (clock_out_geofence IN ('inside', 'outside', 'unknown', 'not_configured')),
    corrected_by          UUID REFERENCES users(id),
    corrected_at          TIMESTAMPTZ,
    correction_reason     TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (clock_out IS NULL OR clock_out > clock_in)
);
CREATE INDEX idx_attendance_sessions_day ON attendance_sessions (attendance_id, clock_in);
-- A second check-in while checked in is the same session, never a
-- parallel one. Enforced in Go under a per-employee advisory lock rather
-- than a unique index: a check-out forgotten days ago must not block every
-- later check-in (it stays open, and the register shows it as a missed
-- check-out for HR to correct).
CREATE INDEX idx_attendance_sessions_open ON attendance_sessions (user_id, clock_in DESC) WHERE clock_out IS NULL;
ALTER TABLE attendance_sessions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON attendance_sessions USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON attendance_sessions TO erp_app;

-- Existing punches: one session each.
INSERT INTO attendance_sessions (merchant_id, attendance_id, user_id, branch_id, clock_in, clock_out,
    clock_in_latitude, clock_in_longitude, clock_in_accuracy_m, clock_in_ip, clock_in_user_agent, clock_in_source,
    clock_in_terminal_id, clock_in_distance_m, clock_in_geofence,
    clock_out_latitude, clock_out_longitude, clock_out_accuracy_m, clock_out_ip, clock_out_user_agent, clock_out_source,
    clock_out_terminal_id, clock_out_distance_m, clock_out_geofence, corrected_by, corrected_at, correction_reason, created_at)
SELECT a.merchant_id, a.id, a.user_id, a.branch_id, a.clock_in, a.clock_out,
    a.clock_in_latitude, a.clock_in_longitude, a.clock_in_accuracy_m, a.clock_in_ip, a.clock_in_user_agent, a.clock_in_source,
    a.clock_in_terminal_id, a.clock_in_distance_m, a.clock_in_geofence,
    a.clock_out_latitude, a.clock_out_longitude, a.clock_out_accuracy_m, a.clock_out_ip, a.clock_out_user_agent, a.clock_out_source,
    a.clock_out_terminal_id, a.clock_out_distance_m, a.clock_out_geofence, a.corrected_by, a.corrected_at, a.correction_reason, a.created_at
FROM attendance a
WHERE a.clock_in IS NOT NULL
  AND (a.clock_out IS NULL OR a.clock_out > a.clock_in);
