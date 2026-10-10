-- =====================================================================
-- Phase 15: configurable approval policies (phased_roadmap.md Phase 15:
-- "price override, discount tiers, refund, stock adjustment, JV posting
-- stored per merchant and read by the existing inline-PIN pattern").
--
-- One row per merchant, the policy as JSON (internal/approvals.Policy
-- validates it). No row = internal/approvals.Default(), which is exactly
-- the behaviour that was hardcoded before: discounts up to 5% free, 15%
-- with a Branch Manager, 25% with a Merchant Admin; every refund approved
-- by a manager when the return policy asks; below-cost prices for anyone
-- with pricing.manage; no approval on stock adjustments; every JV approved
-- when JV approval is on.
-- =====================================================================

CREATE TABLE approval_policies (
    merchant_id UUID PRIMARY KEY REFERENCES merchants(id),
    policy      JSONB NOT NULL,
    updated_by  UUID REFERENCES users(id),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE approval_policies ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON approval_policies USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON approval_policies TO erp_app;

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000045', 'approvals.manage', 'Set approval thresholds: discounts, refunds, below-cost prices, stock adjustments, journal vouchers')
ON CONFLICT (code) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'Merchant Admin' AND p.code = 'approvals.manage'
ON CONFLICT DO NOTHING;
