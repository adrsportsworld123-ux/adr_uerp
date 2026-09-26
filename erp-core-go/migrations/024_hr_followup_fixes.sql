-- =====================================================================
-- Phase 5 follow-up: found during an explicit "double-check HR & Payroll
-- is actually complete" pass (2026-09-24) that cross-referenced
-- pos_frd_complete.md's full §7 text, not just phased_roadmap.md's own
-- condensed bullet list. One real, in-scope gap found and closed here:
--
-- employment_type's CHECK constraint only allowed 'full_time'/
-- 'part_time'/'contract' — the FRD's own "Employee Types" line
-- (§7) names five: "Full-time, Part-time, Contract, Commission-based,
-- Daily Wage (each with different payroll rules)". Widened below.
--
-- Payroll computation itself still treats every employment_type
-- uniformly through the same prorated-salary-structure model — a
-- true per-diem daily-wage rate (paid only for days actually worked,
-- no salary structure at all) and employment-type-specific payroll
-- rules are a real, larger feature this pass does NOT build; widening
-- the constraint lets a merchant at least correctly classify and
-- report on these employee types today, without silently claiming the
-- payroll math itself already branches on it. See
-- phased_roadmap.md's Phase 5 section for the full list of what this
-- audit pass found and deliberately left as a flagged follow-up
-- (loans/advances deductions, multi-frequency payroll, overtime pay,
-- commission target multipliers, leave management workflow).
-- =====================================================================

ALTER TABLE users DROP CONSTRAINT users_employment_type_check;
ALTER TABLE users ADD CONSTRAINT users_employment_type_check
    CHECK (employment_type IN ('full_time', 'part_time', 'contract', 'commission_based', 'daily_wage'));
