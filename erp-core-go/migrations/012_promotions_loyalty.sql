-- =====================================================================
-- Phase 3, sub-area 2: Promotions & Loyalty
-- (phased_roadmap.md Phase 3; pos_frd_complete.md §5)
--
-- Scope notes, matching this codebase's habit of documenting what a real
-- FRD section is deliberately narrowed to for a solo-builder pass:
--
--   * "Bundles" (FRD's "Kit pricing: Bat+Ball+Gloves = ₹3,999") is NOT a
--     promotion type here. Phase 1 already models exactly this as a
--     composite product (products.product_type='composite' +
--     product_components, see 001_schema.sql) with its own selling_price
--     — reusing that avoids a second, competing way to express "these
--     items sold together at a fixed price."
--   * Discount hierarchy item 1 ("Product/Category/Subcategory base
--     discounts") is folded into item 2 (promotional discounts) below —
--     this codebase has no separate "standing base discount" concept on
--     products/categories distinct from a product/category-scoped
--     promotion, and the FRD doesn't define one beyond the name.
--   * Coupon "Payment method restrictions" (FRD §5's Coupon Constraints)
--     is deferred: in this codebase's cart flow, discounts (including
--     coupons) are applied to a cart BEFORE checkout's payments array
--     exists, so there is no payment method to restrict against at
--     apply-time. Every other documented coupon constraint (usage caps,
--     validity window, min purchase, channel, branch) is implemented.
--   * "Time-based" promotions (happy hours, flash sales) are covered by
--     days_of_week/time_start/time_end below, applied on top of any of
--     the other promo_types, not as a separate type.
--
-- Discount hierarchy items 2-5 (promotional, coupon, customer-level,
-- loyalty redemption) all apply through sales.ApplyDiscountLayer, the
-- same additive per-line mechanism item 6 (manual, sales/discounts.go)
-- now also uses — see that function's doc comment for why manual
-- discount's SET discount_amount = $1 had to become additive once other
-- layers could legitimately stack on top of it.
-- =====================================================================

CREATE TABLE promotions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    name                TEXT NOT NULL,
    promo_type          TEXT NOT NULL CHECK (promo_type IN ('percent', 'fixed', 'bogo', 'volume', 'min_value')),
    application_level   TEXT NOT NULL DEFAULT 'order' CHECK (application_level IN ('order', 'product', 'category')),
    product_id          UUID REFERENCES products(id),
    category_id         UUID REFERENCES categories(id),
    -- Discount hierarchy item 4 ("Customer-level discounts (VIP/wholesale)")
    -- folded in here rather than a separate table: a promotion additionally
    -- scoped to one auto-computed segment (internal/customers' segmentation,
    -- reused via customers.FetchSegment) IS a customer-level discount.
    target_segment      TEXT CHECK (target_segment IN ('vip', 'regular', 'new', 'dormant')),
    -- Type-specific parameters, shaped per promo_type (validated in Go, not
    -- in SQL, since CHECK can't easily express "this JSON matches this
    -- promo_type's schema"):
    --   percent:   {"value_percent": 10}
    --   fixed:     {"value_amount": 500}
    --   bogo:      {"buy_qty": 2, "get_qty": 1, "get_discount_pct": 100}
    --   volume:    {"tiers": [{"min_qty":3,"max_qty":5,"discount_pct":5}, ...]}
    --   min_value: {"min_purchase_amount": 2000, "discount_amount": 200}
    --              (or "discount_pct" instead of "discount_amount")
    config              JSONB NOT NULL DEFAULT '{}',
    stacking            TEXT NOT NULL DEFAULT 'exclusive' CHECK (stacking IN ('exclusive', 'stackable')),
    starts_at           TIMESTAMPTZ,
    ends_at             TIMESTAMPTZ,
    days_of_week        SMALLINT[],   -- Postgres EXTRACT(DOW): 0=Sunday..6=Saturday; NULL = every day
    time_start          TIME,
    time_end            TIME,
    active              BOOLEAN NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((application_level = 'product') = (product_id IS NOT NULL)),
    CHECK ((application_level = 'category') = (category_id IS NOT NULL))
);
CREATE INDEX idx_promotions_merchant_active ON promotions(merchant_id) WHERE active;

CREATE TABLE coupons (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id                 UUID NOT NULL REFERENCES merchants(id),
    code                        TEXT NOT NULL,
    promo_type                  TEXT NOT NULL CHECK (promo_type IN ('percent', 'fixed')),
    value                       NUMERIC(14,2) NOT NULL,
    min_purchase_amount         NUMERIC(14,2),
    usage_limit_total           INT,
    usage_limit_per_customer    INT,
    usage_count                 INT NOT NULL DEFAULT 0,
    valid_from                  TIMESTAMPTZ,
    valid_until                 TIMESTAMPTZ,
    channels                    TEXT[],                          -- 'pos'|'online'|'mobile'; NULL = any
    branch_id                   UUID REFERENCES branches(id),     -- NULL = all branches
    active                      BOOLEAN NOT NULL DEFAULT true,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, code)
);

CREATE TABLE coupon_redemptions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    coupon_id           UUID NOT NULL REFERENCES coupons(id),
    customer_id         UUID REFERENCES customers(id),
    sales_order_id      UUID NOT NULL REFERENCES sales_orders(id),
    discount_amount     NUMERIC(14,2) NOT NULL,
    redeemed_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One coupon per order: the FRD's hierarchy has exactly one "Coupon
    -- codes" slot, not a stack of coupons on top of each other.
    UNIQUE (sales_order_id)
);

CREATE TABLE loyalty_config (
    merchant_id                 UUID PRIMARY KEY REFERENCES merchants(id),
    earn_rupees_per_point       NUMERIC(14,2) NOT NULL DEFAULT 100,   -- FRD default: ₹100 = 1 point
    redeem_points_per_rupee     NUMERIC(14,4) NOT NULL DEFAULT 10,    -- FRD default: 100 points = ₹10
    expiry_months               INT NOT NULL DEFAULT 12,
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- FRD §5's loyalty expiry is "1 year from last transaction (rolling)" — the
-- customer's WHOLE balance lapses if they go expiry_months without a new
-- ledger entry, not a per-earned-batch FIFO expiry. That means expiry only
-- ever depends on "when was this customer's most recent ledger row,"
-- computed on read (internal/loyalty.AvailableBalance) exactly like
-- Customers' segment and Pricing's margin_pct already are — no expires_at
-- column and no sweeper needed. balance_after is a historical running
-- total (like a bank statement line), distinct from "available now," which
-- also depends on whether the balance has since gone dormant.
CREATE TABLE loyalty_ledger (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    customer_id         UUID NOT NULL REFERENCES customers(id),
    sales_order_id      UUID REFERENCES sales_orders(id),
    entry_type          TEXT NOT NULL CHECK (entry_type IN ('earn', 'redeem')),
    points              INT NOT NULL,      -- positive for earn, negative for redeem
    balance_after       INT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_loyalty_ledger_customer ON loyalty_ledger(merchant_id, customer_id, created_at);

ALTER TABLE promotions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON promotions USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE coupons ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON coupons USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE coupon_redemptions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON coupon_redemptions USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE loyalty_config ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON loyalty_config USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE loyalty_ledger ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON loyalty_ledger USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON promotions, coupons, coupon_redemptions, loyalty_config, loyalty_ledger TO erp_app;

-- sales_order_discounts (003_hardening.sql) previously only ever recorded
-- 'manual'/'coupon' (and coupon was really just a label on a manual-style
-- percent discount — see internal/sales/discounts.go's old discountRequest.
-- Type comment). Now that promotions and loyalty redemption are real,
-- independent discount sources with their own audit needs, widen the type
-- check and add nullable back-references so an entry can point at exactly
-- which promotion/coupon/loyalty-ledger row produced it.
ALTER TABLE sales_order_discounts DROP CONSTRAINT sales_order_discounts_type_check;
ALTER TABLE sales_order_discounts ADD CONSTRAINT sales_order_discounts_type_check
    CHECK (type IN ('manual', 'coupon', 'promotion', 'loyalty'));
ALTER TABLE sales_order_discounts
    ADD COLUMN promotion_id UUID REFERENCES promotions(id),
    ADD COLUMN coupon_id UUID REFERENCES coupons(id),
    ALTER COLUMN value_percent DROP NOT NULL;
COMMENT ON COLUMN sales_order_discounts.value_percent IS
    'Only meaningful for manual/percent-style entries; promotion/coupon/loyalty rows that resolve straight to a rupee amount (fixed, bogo, volume, min_value, loyalty redemption) leave this NULL rather than back-computing a fake percent.';

-- New permissions, same "this is a real business-risk operation" reasoning
-- already applied to pricing.manage/inventory.adjust/branch_transfer.approve.
-- .../000005 is already taken (migrations/010_search.sql's search.reindex)
-- — 006/007 are the next free ids in this sequence.
INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000006', 'promotions.manage', 'Create/update promotions and coupons'),
    ('f0000000-0000-0000-0000-000000000007', 'loyalty.manage', 'Change loyalty earn/redeem/expiry configuration')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.merchant_id = '11111111-1111-1111-1111-111111111111'
  AND r.name IN ('Branch Manager', 'Merchant Admin')
  AND p.code IN ('promotions.manage', 'loyalty.manage')
ON CONFLICT DO NOTHING;

-- Default loyalty config for the one seeded merchant, matching the FRD's
-- own defaults (₹100=1 point earn, 100 points=₹10 redeem, 12-month expiry)
-- — every merchant needs a config row for loyalty.EarnForOrder to work,
-- and there's no merchant-onboarding flow yet to create one automatically
-- (the same gap 005_permissions.sql's header comment already flags for
-- roles/permissions).
INSERT INTO loyalty_config (merchant_id) VALUES ('11111111-1111-1111-1111-111111111111')
ON CONFLICT (merchant_id) DO NOTHING;
