-- =====================================================================
-- One place to see approvals (phase0_1_design.md §3.77): what is waiting
-- for someone (GET /approvals/waiting) and what was approved by PIN at the
-- till or in the back office (GET /approvals/given).
--
-- approvals.view: see both lists (Branch Manager and Merchant Admin, as
-- every other approval). Acting on a waiting item still goes through that
-- item's own endpoint and permission (branch_transfer.approve,
-- accounting.jv_approve, inventory.adjust).
--
-- stock_movements.approved_by: the manager who approved a stock adjustment
-- above the approval policy's limit (Phase 15). It was checked but not
-- kept until now, so earlier adjustments show no approver. NULL = none was
-- needed.
-- =====================================================================

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000073', 'approvals.view',
     'See the approvals waiting for someone and the approvals given (discounts, refunds, prices, cash and stock variances)')
ON CONFLICT (code) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name IN ('Branch Manager', 'Merchant Admin') AND p.code = 'approvals.view'
ON CONFLICT DO NOTHING;
-- New merchants' Branch Managers too (Merchant Admin gets every permission).
INSERT INTO role_templates (role_name, permission_code) VALUES ('Branch Manager', 'approvals.view')
ON CONFLICT DO NOTHING;

ALTER TABLE stock_movements ADD COLUMN approved_by UUID REFERENCES users(id);
