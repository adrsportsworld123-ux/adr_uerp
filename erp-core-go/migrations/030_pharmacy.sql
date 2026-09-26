-- =====================================================================
-- Phase 8: Vertical Expansion — Pharmacy
-- (phased_roadmap.md Phase 8: "activate prescription linkage, stricter
-- expiry compliance, regulatory reporting")
--
-- "Stricter expiry compliance" is built as a genuinely generic policy
-- knob, not a pharmacy-specific one: categories.min_shelf_life_days lets
-- ANY category refuse a batch that's technically not yet expired but
-- close to it — internal/inventory.AllocateBatchesFIFO (Grocery/FMCG,
-- migrations/029_grocery.sql) already refuses a literally-expired batch
-- for every batch-tracked variant; this adds an optional, per-category
-- buffer on top. Pharmacy is simply the vertical that sets it high (e.g.
-- 30 days) for schedule-drug categories — internal/sales and
-- internal/inventory stay fully vertical-agnostic, reading one plain
-- integer, never a "is this pharmacy" branch.
--
-- Drug scheduling itself (OTC/Schedule H/Schedule H1/Narcotic) is NOT new
-- schema — it reuses the attribute-set mechanism the earlier Phase 8 pass
-- built (a "Drug Schedule" select-type attribute assigned to a Pharmacy
-- category). Prescription-required enforcement (see internal/sales'
-- Checkout) keys off that attribute's value directly: a disclosed
-- convention, not a fully general checkout-policy engine.
-- =====================================================================

ALTER TABLE categories ADD COLUMN min_shelf_life_days INT NOT NULL DEFAULT 0;

CREATE TABLE prescriptions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id   UUID NOT NULL REFERENCES merchants(id),
    customer_id   UUID NOT NULL REFERENCES customers(id),
    doctor_name   TEXT NOT NULL,
    doctor_reg_no TEXT,
    notes         TEXT,
    created_by    UUID NOT NULL REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE prescriptions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON prescriptions
    USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON prescriptions TO erp_app;

CREATE INDEX idx_prescriptions_customer ON prescriptions(customer_id);

-- NULL (every existing order, today) means "no prescription attached" —
-- Checkout only requires one be set when a line's variant actually needs
-- it (see above), zero behavior change for every merchant not selling
-- scheduled drugs.
ALTER TABLE sales_orders ADD COLUMN prescription_id UUID REFERENCES prescriptions(id);
