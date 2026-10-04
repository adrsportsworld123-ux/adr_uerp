-- =====================================================================
-- Phase 10: Pricing & quotation gaps (phased_roadmap.md Phase 10)
--
-- Closes the three items migrations/009_pricing.sql and
-- 026_wholesale_b2b.sql named as deferred (pos_frd_complete.md §11):
--   1. Scheduled price changes (future effective date)
--   2. Dynamic pricing: time-based (happy hours), day-specific (weekend
--      specials), seasonal with auto-activation
--   3. Editing a draft quotation's lines (Go only — no schema change)
--
-- Why price rules aren't just promotions: promotions (012) already have
-- days_of_week/time windows, but they're DISCOUNTS — a separate discount
-- line, never an increase, never "this item costs ₹X on weekends". A
-- price rule changes the line's unit price itself, and can go up as well
-- as down (capped at MRP in Go: selling above MRP is illegal in India).
-- Promotions still apply on top of a rule-priced line, exactly as they
-- apply on top of a base-priced one.
-- =====================================================================

-- 1. Scheduled price changes ------------------------------------------
-- A pending cost/MRP/selling-price change applied by
-- pricing.RunScheduledPriceSweeper once effective_at passes, through the
-- same applyVariantPricing path PATCH /pricing/variants/{id} uses (margin
-- guard, price_history, search reindex). NULL field = leave unchanged.
CREATE TABLE scheduled_price_changes (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id    UUID NOT NULL REFERENCES merchants(id),
    variant_id     UUID NOT NULL REFERENCES product_variants(id),
    cost_price     NUMERIC(14,2) CHECK (cost_price >= 0),
    mrp            NUMERIC(14,2) CHECK (mrp >= 0),
    selling_price  NUMERIC(14,2) CHECK (selling_price >= 0),
    effective_at   TIMESTAMPTZ NOT NULL,
    reason         TEXT,
    -- Allow a below-cost result at apply time. Re-checked when applied,
    -- not only when scheduled: cost can change in between (a GRN's
    -- weighted-average cost update, say).
    override       BOOLEAN NOT NULL DEFAULT false,
    status         TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending', 'applied', 'cancelled', 'failed')),
    failure_reason TEXT,
    created_by     UUID REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    applied_at     TIMESTAMPTZ,
    CHECK (cost_price IS NOT NULL OR mrp IS NOT NULL OR selling_price IS NOT NULL)
);

ALTER TABLE scheduled_price_changes ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON scheduled_price_changes
    USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON scheduled_price_changes TO erp_app;

-- The sweeper's query: due, still-pending changes, oldest first.
CREATE INDEX idx_scheduled_price_changes_due ON scheduled_price_changes(effective_at) WHERE status = 'pending';
CREATE INDEX idx_scheduled_price_changes_variant ON scheduled_price_changes(variant_id, effective_at);

-- 2. Price rules (dynamic pricing) ------------------------------------
CREATE TABLE price_rules (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id  UUID NOT NULL REFERENCES merchants(id),
    name         TEXT NOT NULL,
    -- Exactly one scope. A variant rule beats a category rule when both
    -- match (more specific wins), then priority, then newest.
    variant_id   UUID REFERENCES product_variants(id),
    category_id  UUID REFERENCES categories(id),
    branch_id    UUID REFERENCES branches(id),   -- NULL = every branch
    -- 'fixed_price': value IS the unit price (variant scope only — one
    --   price across a whole category is never what anyone means).
    -- 'percent': signed adjustment to the base selling price; -20 = 20%
    --   off, +10 = 10% up. Floor 0, capped at MRP in Go.
    rule_type    TEXT NOT NULL CHECK (rule_type IN ('fixed_price', 'percent')),
    value        NUMERIC(14,2) NOT NULL,
    priority     INT NOT NULL DEFAULT 0,
    -- Seasonal auto-activation: the rule is only live inside this range.
    starts_at    TIMESTAMPTZ,
    ends_at      TIMESTAMPTZ,
    -- Day-specific / happy hours, evaluated in the BRANCH's own timezone
    -- (branches.timezone), not the database server's — a 2-5 PM rule must
    -- mean 2-5 PM at the store regardless of where Postgres runs.
    days_of_week SMALLINT[],   -- EXTRACT(DOW): 0=Sunday..6=Saturday; NULL = every day
    time_start   TIME,
    time_end     TIME,         -- may be < time_start for a window crossing midnight
    active       BOOLEAN NOT NULL DEFAULT true,
    created_by   UUID REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((variant_id IS NULL) <> (category_id IS NULL)),
    CHECK (rule_type = 'percent' OR variant_id IS NOT NULL),
    CHECK (rule_type <> 'fixed_price' OR value >= 0),
    CHECK (rule_type <> 'percent' OR value >= -100),
    CHECK ((time_start IS NULL) = (time_end IS NULL)),
    CHECK (starts_at IS NULL OR ends_at IS NULL OR starts_at < ends_at)
);

ALTER TABLE price_rules ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON price_rules
    USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON price_rules TO erp_app;

CREATE INDEX idx_price_rules_variant  ON price_rules(variant_id)  WHERE active AND variant_id IS NOT NULL;
CREATE INDEX idx_price_rules_category ON price_rules(category_id) WHERE active AND category_id IS NOT NULL;

-- Which rule (if any) priced a sale line — "how much did happy hour
-- actually sell" is answerable, and a rule that ever priced a real sale
-- can't be hard-deleted out from under that record (DELETE refuses with
-- PRICE_RULE_IN_USE; deactivate it instead).
ALTER TABLE sales_order_lines ADD COLUMN price_rule_id UUID REFERENCES price_rules(id);
