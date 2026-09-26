-- =====================================================================
-- Phase 7: Wholesale/B2B & Omnichannel
-- (phased_roadmap.md Phase 7)
--
-- Scope for this pass, confirmed explicitly with the user before writing
-- any code (the same "don't unilaterally decide a big scope question"
-- posture as the e-invoicing GSP choice and Phase 6's LLM vendor choice):
--
--   1. Formal B2B quotations (quotations/quotation_lines below). A quote
--      converts into a REAL sales_order through the exact same
--      cart/add-line pipeline every other order already uses
--      (internal/quotations.ConvertQuotation calls into internal/sales) —
--      no separate B2B order table, no parallel stock-reservation path,
--      so "no double-entry" is true by construction, not by convention.
--
--   2. Wholesale price lists at scale (price_lists/price_list_items,
--      customers.price_list_id below). Resolved by ONE function
--      (internal/pricing.ResolvePrice) called from both POS add-line and
--      quotation creation, so a wholesale customer's price is never
--      computed two different ways in two different code paths.
--
--   3. Channel-agnostic backend (pos_terminals.channel/sales_orders.channel
--      below). A real customer-facing storefront is a whole separate
--      application (a 5th repo alongside erp-core-go/erp-business-py/
--      erp-web-admin/erp-pos-flutter) — explicitly NOT built this pass,
--      per the user's own choice. What IS built: the one thing that
--      would otherwise block it later — an "online" channel can create
--      real orders through this same API and reserve stock through the
--      exact same stock_levels optimistic-locking path, never a second
--      inventory system that would need reconciling with the first.
--
-- Deliberately deferred, named rather than silently dropped (matching
-- migrations/009_pricing.sql's own header, which named this exact
-- follow-up two phases ago): scheduled future-dated price changes and
-- dynamic/time-based pricing. Neither is needed to satisfy this phase's
-- actual Definition of Done ("the same catalog and stock serve both a
-- walk-in retail customer and a B2B wholesale order without
-- double-entry") — both are real, valuable, separate builds.
-- =====================================================================

-- ---------------------------------------------------------------------
-- Wholesale price lists
-- ---------------------------------------------------------------------

CREATE TABLE price_lists (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, name)
);

CREATE TABLE price_list_items (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    price_list_id UUID NOT NULL REFERENCES price_lists(id) ON DELETE CASCADE,
    variant_id    UUID NOT NULL REFERENCES product_variants(id),
    price         NUMERIC(14,2) NOT NULL CHECK (price >= 0),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (price_list_id, variant_id)
);

ALTER TABLE price_lists ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON price_lists
    USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

-- Same "join through the parent" RLS shape as sales_order_lines/payments
-- (migrations/003_hardening.sql) — this table has no merchant_id of its
-- own, price_lists already enforces the tenant boundary one hop up.
ALTER TABLE price_list_items ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON price_list_items
    USING (price_list_id IN (SELECT id FROM price_lists));

GRANT SELECT, INSERT, UPDATE, DELETE ON price_lists TO erp_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON price_list_items TO erp_app;

-- NULL (every existing customer, today) means "plain retail
-- product_variants.selling_price" — zero behavior change for anyone not
-- explicitly assigned a price list.
ALTER TABLE customers ADD COLUMN price_list_id UUID REFERENCES price_lists(id);

-- ---------------------------------------------------------------------
-- Channel-agnostic orders
-- ---------------------------------------------------------------------

ALTER TABLE pos_terminals ADD COLUMN channel TEXT NOT NULL DEFAULT 'pos' CHECK (channel IN ('pos','online'));
ALTER TABLE sales_orders ADD COLUMN channel TEXT NOT NULL DEFAULT 'pos' CHECK (channel IN ('pos','online'));

-- One virtual "online" terminal per existing branch, so a future
-- storefront (or this migration's own live verification) has a real
-- pos_terminal_id to create orders against without a physical device —
-- satisfies sales_orders.pos_terminal_id's existing NOT NULL FK exactly
-- the way every other order already does, no schema relaxation needed.
-- internal/branches.CreateBranch provisions the same thing for every new
-- branch from here on.
INSERT INTO pos_terminals (id, merchant_id, branch_id, name, channel, status)
SELECT gen_random_uuid(), b.merchant_id, b.id, 'Online Storefront', 'online', 'active'
FROM branches b
WHERE NOT EXISTS (
    SELECT 1 FROM pos_terminals pt WHERE pt.branch_id = b.id AND pt.channel = 'online'
);

-- ---------------------------------------------------------------------
-- B2B quotations
-- ---------------------------------------------------------------------

CREATE TABLE quotations (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id              UUID NOT NULL REFERENCES merchants(id),
    branch_id                UUID NOT NULL REFERENCES branches(id),
    customer_id              UUID NOT NULL REFERENCES customers(id),
    quote_number             TEXT NOT NULL,
    status                   TEXT NOT NULL DEFAULT 'draft'
                                 CHECK (status IN ('draft','sent','accepted','rejected','expired','converted')),
    valid_until              DATE NOT NULL,
    subtotal                 NUMERIC(14,2) NOT NULL DEFAULT 0,
    tax_total                NUMERIC(14,2) NOT NULL DEFAULT 0,
    grand_total              NUMERIC(14,2) NOT NULL DEFAULT 0,
    notes                    TEXT,
    created_by               UUID NOT NULL REFERENCES users(id),
    -- Set only on conversion — the real sales_order this quote became,
    -- through the same cart pipeline as any other order (see header).
    converted_sales_order_id UUID REFERENCES sales_orders(id),
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (branch_id, quote_number)
);

CREATE TABLE quotation_lines (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    quotation_id UUID NOT NULL REFERENCES quotations(id) ON DELETE CASCADE,
    variant_id   UUID NOT NULL REFERENCES product_variants(id),
    quantity     NUMERIC(14,3) NOT NULL CHECK (quantity > 0),
    unit_price   NUMERIC(14,2) NOT NULL,  -- price snapshot at quote-creation time, same convention as sales_order_lines
    tax_amount   NUMERIC(14,2) NOT NULL DEFAULT 0,
    line_total   NUMERIC(14,2) NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE quotations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON quotations
    USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE quotation_lines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON quotation_lines
    USING (quotation_id IN (SELECT id FROM quotations));

GRANT SELECT, INSERT, UPDATE, DELETE ON quotations TO erp_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON quotation_lines TO erp_app;

CREATE INDEX idx_quotations_customer ON quotations(customer_id);
CREATE INDEX idx_quotations_status_valid_until ON quotations(status, valid_until);
