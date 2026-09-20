-- =====================================================================
-- Phase 2, sub-area 4: Pricing Management
-- (phased_roadmap.md Phase 2; pos_frd_complete.md §11)
--
-- Scope, matching the roadmap's explicit "In" list AND its "Deferred"
-- line for this same phase ("wholesale pricing" is named as deferred,
-- alongside CRM) — this is cost/MRP/selling price management, margin
-- calculation, and bulk updates against the ONE selling_price
-- product_variants already has. NOT built here, because the roadmap
-- defers it: multiple named price tiers (Retail/Wholesale/VIP/Special),
-- customer-specific pricing, price lists assigned to
-- segments/branches/channels, scheduled future-dated price changes, and
-- dynamic (time/day/season) pricing — all real FRD §11 content, all
-- Phase 7 ("Wholesale/B2B & Omnichannel") or Phase 3 (CRM segments)
-- territory, not this pass's.
-- =====================================================================

CREATE TABLE price_history (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    variant_id      UUID NOT NULL REFERENCES product_variants(id),
    field           TEXT NOT NULL CHECK (field IN ('cost_price','mrp','selling_price')),
    old_value       NUMERIC(14,2),
    new_value       NUMERIC(14,2) NOT NULL,
    reason          TEXT,
    changed_by      UUID REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_price_history_variant ON price_history(variant_id, created_at DESC);

ALTER TABLE price_history ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON price_history
    USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON price_history TO erp_app;

-- New permission: changing prices (individually or in bulk) is gated —
-- pricing mistakes are a real business risk, same reasoning as
-- inventory.adjust and branch_transfer.approve.
INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000004', 'pricing.manage', 'Change a product variant''s cost/MRP/selling price, individually or in bulk')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.merchant_id = '11111111-1111-1111-1111-111111111111'
  AND r.name IN ('Branch Manager', 'Merchant Admin')
  AND p.code = 'pricing.manage'
ON CONFLICT DO NOTHING;
