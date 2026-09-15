-- =====================================================================
-- Universal Retail & Wholesale ERP — Phase 0 / Phase 1 Database Schema
-- Target: PostgreSQL 16+
-- Scope: Multi-tenancy foundation, Auth/RBAC, Catalog, Sales & Billing,
--        Inventory (with stock reservation), Audit trail.
-- Deferred to later phases: Purchase, Accounting/Ledger, CRM/Loyalty,
--        HR/Payroll, AI tables, e-invoicing/e-way bill fields.
-- Multi-tenancy model: shared database, row-level tenancy (see RLS
--        policies at the bottom of this file).
-- =====================================================================

CREATE EXTENSION IF NOT EXISTS "pgcrypto";   -- for gen_random_uuid()

-- =====================================================================
-- SECTION 1: TENANCY & AUTH
-- =====================================================================

CREATE TABLE merchants (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code                TEXT NOT NULL UNIQUE,       -- short, human-typeable tenant key (e.g. "acme-sports");
                                                     -- resolves which tenant a login belongs to BEFORE the
                                                     -- user/password lookup, since email is only unique
                                                     -- per-merchant (see users.email below), not globally
    legal_name          TEXT NOT NULL,
    trade_name          TEXT,
    gstin               TEXT,
    subscription_tier   TEXT NOT NULL DEFAULT 'freemium'
                            CHECK (subscription_tier IN ('freemium','starter','professional','enterprise')),
    status              TEXT NOT NULL DEFAULT 'active'
                            CHECK (status IN ('active','suspended','cancelled')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE branches (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    name                TEXT NOT NULL,
    code                TEXT NOT NULL,               -- short code, e.g. "BLR01"
    address             JSONB,
    gstin               TEXT,                        -- branch may carry its own GSTIN (different state)
    timezone            TEXT NOT NULL DEFAULT 'Asia/Kolkata',
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, code)
);

CREATE TABLE pos_terminals (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    branch_id           UUID NOT NULL REFERENCES branches(id),
    name                TEXT NOT NULL,
    device_fingerprint  TEXT,                        -- for device binding (FRD §18)
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive','blocked')),
    last_synced_at      TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE roles (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID REFERENCES merchants(id),   -- NULL = system default role (shared template)
    name                TEXT NOT NULL,                    -- "Merchant Admin", "Branch Manager", "POS User", custom...
    is_system_default   BOOLEAN NOT NULL DEFAULT false,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE permissions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code                TEXT NOT NULL UNIQUE,   -- "sales.create", "sales.discount.override", "inventory.adjust"
    description         TEXT
);

CREATE TABLE role_permissions (
    role_id             UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id       UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE users (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id             UUID NOT NULL REFERENCES merchants(id),
    branch_id               UUID REFERENCES branches(id),  -- NULL = all-branch access (admin-level)
    name                    TEXT NOT NULL,
    email                   TEXT,
    phone                   TEXT,
    password_hash           TEXT,
    pin_hash                TEXT,               -- 4-6 digit quick-login PIN, hashed
    status                  TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive','locked')),
    failed_login_attempts   INT NOT NULL DEFAULT 0,
    locked_until            TIMESTAMPTZ,
    password_changed_at     TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, email),
    UNIQUE (merchant_id, phone)
);

CREATE TABLE user_roles (
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id             UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

CREATE TABLE refresh_tokens (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id),
    token_hash          TEXT NOT NULL,
    device_fingerprint  TEXT,
    pos_terminal_id     UUID REFERENCES pos_terminals(id),
    expires_at          TIMESTAMPTZ NOT NULL,
    revoked_at          TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =====================================================================
-- SECTION 2: CATALOG & TAX
-- =====================================================================

CREATE TABLE tax_slabs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    name                TEXT NOT NULL,          -- "GST 18%"
    cgst_rate           NUMERIC(5,2) NOT NULL DEFAULT 0,
    sgst_rate           NUMERIC(5,2) NOT NULL DEFAULT 0,
    igst_rate           NUMERIC(5,2) NOT NULL DEFAULT 0,
    cess_rate           NUMERIC(5,2) NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE categories (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    parent_id           UUID REFERENCES categories(id),
    name                TEXT NOT NULL,
    path                TEXT,                    -- materialized path, e.g. "/sports/cricket/bats"
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE brands (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    name                TEXT NOT NULL
);

CREATE TABLE attributes (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    name                TEXT NOT NULL,            -- "Size", "Color"
    input_type          TEXT NOT NULL DEFAULT 'select' CHECK (input_type IN ('select','text','number'))
);

CREATE TABLE attribute_values (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    attribute_id        UUID NOT NULL REFERENCES attributes(id) ON DELETE CASCADE,
    value               TEXT NOT NULL,            -- "M", "Red"
    sort_order          INT NOT NULL DEFAULT 0
);

CREATE TABLE products (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    category_id         UUID REFERENCES categories(id),
    brand_id            UUID REFERENCES brands(id),
    tax_slab_id         UUID REFERENCES tax_slabs(id),
    name                TEXT NOT NULL,
    short_description   TEXT,
    hsn_code            TEXT,
    product_type        TEXT NOT NULL DEFAULT 'simple' CHECK (product_type IN ('simple','variant','composite')),
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive','discontinued')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE product_variants (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    product_id          UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    sku                 TEXT NOT NULL,
    attribute_combo     JSONB NOT NULL DEFAULT '{}',   -- {"Size":"M","Color":"Red"}
    cost_price          NUMERIC(14,2) NOT NULL DEFAULT 0,
    mrp                 NUMERIC(14,2) NOT NULL,
    selling_price       NUMERIC(14,2) NOT NULL,
    track_serial        BOOLEAN NOT NULL DEFAULT false,
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive','discontinued','out_of_stock')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, sku)
);

CREATE TABLE product_components (           -- composite products / kits (e.g. "Cricket Kit")
    parent_variant_id      UUID NOT NULL REFERENCES product_variants(id),
    component_variant_id   UUID NOT NULL REFERENCES product_variants(id),
    quantity                NUMERIC(10,3) NOT NULL DEFAULT 1,
    PRIMARY KEY (parent_variant_id, component_variant_id)
);

CREATE TABLE barcodes (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    variant_id          UUID NOT NULL REFERENCES product_variants(id) ON DELETE CASCADE,
    code                TEXT NOT NULL,
    symbology           TEXT NOT NULL DEFAULT 'EAN13' CHECK (symbology IN ('EAN13','EAN8','UPCA','UPCE','CODE128','CODE39','QR')),
    is_primary          BOOLEAN NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, code)
);

-- =====================================================================
-- SECTION 3: SALES & BILLING
-- =====================================================================

CREATE TABLE customers (         -- minimal stub; full CRM/segmentation arrives in Phase 3
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    name                TEXT,
    phone               TEXT,
    email                TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sales_orders (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    branch_id           UUID NOT NULL REFERENCES branches(id),
    pos_terminal_id     UUID NOT NULL REFERENCES pos_terminals(id),
    cashier_id          UUID NOT NULL REFERENCES users(id),
    customer_id         UUID REFERENCES customers(id),
    order_number        TEXT NOT NULL,             -- human-readable, branch-scoped sequence
    status              TEXT NOT NULL DEFAULT 'cart'
                            CHECK (status IN ('cart','finalized','voided','refunded')),
    subtotal            NUMERIC(14,2) NOT NULL DEFAULT 0,
    discount_total       NUMERIC(14,2) NOT NULL DEFAULT 0,
    tax_total            NUMERIC(14,2) NOT NULL DEFAULT 0,
    grand_total          NUMERIC(14,2) NOT NULL DEFAULT 0,
    idempotency_key      TEXT,                      -- client-generated; dedupes offline retry storms
    device_created_at    TIMESTAMPTZ,               -- POS device's local clock at creation (may be offline)
    synced_at            TIMESTAMPTZ,               -- when this record reached the server
    finalized_at         TIMESTAMPTZ,
    void_reason          TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, idempotency_key),
    UNIQUE (branch_id, order_number)
);

CREATE TABLE sales_order_lines (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sales_order_id      UUID NOT NULL REFERENCES sales_orders(id) ON DELETE CASCADE,
    variant_id          UUID NOT NULL REFERENCES product_variants(id),
    quantity            NUMERIC(14,3) NOT NULL,
    unit_price          NUMERIC(14,2) NOT NULL,    -- price snapshot at time of sale
    discount_amount     NUMERIC(14,2) NOT NULL DEFAULT 0,
    tax_amount          NUMERIC(14,2) NOT NULL DEFAULT 0,
    line_total          NUMERIC(14,2) NOT NULL,
    serial_no           TEXT,                       -- populated if variant.track_serial = true
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE payments (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sales_order_id      UUID NOT NULL REFERENCES sales_orders(id),
    method              TEXT NOT NULL CHECK (method IN ('cash','card','upi')),  -- expand in later phases
    amount              NUMERIC(14,2) NOT NULL,
    reference_no        TEXT,                        -- gateway/transaction reference
    status              TEXT NOT NULL DEFAULT 'captured' CHECK (status IN ('captured','failed','refunded')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =====================================================================
-- SECTION 4: INVENTORY
-- =====================================================================

CREATE TABLE stock_levels (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    branch_id           UUID NOT NULL REFERENCES branches(id),
    variant_id          UUID NOT NULL REFERENCES product_variants(id),
    on_hand             NUMERIC(14,3) NOT NULL DEFAULT 0,
    reserved            NUMERIC(14,3) NOT NULL DEFAULT 0,   -- available = on_hand - reserved (computed, not stored)
    reorder_point       NUMERIC(14,3) NOT NULL DEFAULT 0,
    version             INT NOT NULL DEFAULT 0,             -- optimistic-locking token
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (branch_id, variant_id)
);

CREATE TABLE stock_reservations (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    branch_id           UUID NOT NULL REFERENCES branches(id),
    variant_id          UUID NOT NULL REFERENCES product_variants(id),
    sales_order_id      UUID REFERENCES sales_orders(id),
    quantity            NUMERIC(14,3) NOT NULL,
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','released','consumed','expired')),
    expires_at          TIMESTAMPTZ NOT NULL,               -- created_at + 15 minutes (FRD default)
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE stock_movements (      -- append-only ledger; source of truth for stock_levels reconciliation
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    branch_id           UUID NOT NULL REFERENCES branches(id),
    variant_id          UUID NOT NULL REFERENCES product_variants(id),
    movement_type       TEXT NOT NULL CHECK (movement_type IN ('sale','adjustment','transfer_in','transfer_out','purchase')),
    quantity_delta      NUMERIC(14,3) NOT NULL,             -- signed
    reference_type       TEXT,                               -- 'sales_order' | 'adjustment' | ...
    reference_id         UUID,
    reason               TEXT,
    performed_by         UUID REFERENCES users(id),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE serial_numbers (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    variant_id          UUID NOT NULL REFERENCES product_variants(id),
    branch_id           UUID REFERENCES branches(id),
    serial_no           TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'in_stock' CHECK (status IN ('in_stock','sold','returned')),
    sold_in_order_id      UUID REFERENCES sales_orders(id),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, serial_no)
);

-- =====================================================================
-- SECTION 5: AUDIT TRAIL
-- =====================================================================

CREATE TABLE audit_logs (
    id                  BIGSERIAL,
    merchant_id         UUID NOT NULL,
    entity_type         TEXT NOT NULL,
    entity_id           UUID NOT NULL,
    action               TEXT NOT NULL CHECK (action IN ('create','update','delete')),
    performed_by          UUID,
    before_value          JSONB,
    after_value            JSONB,
    reason                 TEXT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);
-- Create monthly partitions going forward, e.g.:
-- CREATE TABLE audit_logs_2026_09 PARTITION OF audit_logs
--     FOR VALUES FROM ('2026-09-01') TO ('2026-10-01');
-- Automate partition creation via a scheduled job or pg_partman once on real infra.

-- =====================================================================
-- SECTION 6: INDEXES
-- =====================================================================

CREATE INDEX idx_branches_merchant           ON branches(merchant_id);
CREATE INDEX idx_pos_terminals_branch         ON pos_terminals(branch_id);
CREATE INDEX idx_users_merchant               ON users(merchant_id);
CREATE INDEX idx_products_merchant_category   ON products(merchant_id, category_id);
CREATE INDEX idx_product_variants_product     ON product_variants(product_id);
CREATE INDEX idx_barcodes_code                ON barcodes(code);
CREATE INDEX idx_sales_orders_branch_created  ON sales_orders(branch_id, created_at DESC);
CREATE INDEX idx_sales_orders_status          ON sales_orders(merchant_id, status);
CREATE INDEX idx_sales_order_lines_order      ON sales_order_lines(sales_order_id);
CREATE INDEX idx_stock_levels_branch_variant  ON stock_levels(branch_id, variant_id);
CREATE INDEX idx_stock_reservations_expiry    ON stock_reservations(status, expires_at);
CREATE INDEX idx_stock_movements_variant      ON stock_movements(branch_id, variant_id, created_at DESC);
CREATE INDEX idx_audit_logs_entity            ON audit_logs(merchant_id, entity_type, entity_id);

-- =====================================================================
-- SECTION 7: ROW-LEVEL SECURITY (multi-tenancy enforcement)
-- =====================================================================
-- Pattern: every tenant-scoped table gets RLS enabled, keyed off
-- current_setting('app.tenant_id'). The application layer (Go core)
-- executes `SET LOCAL app.tenant_id = '<merchant_id>'` as the first
-- statement of every transaction, immediately after resolving the
-- tenant from the authenticated JWT — never trust a client-supplied
-- tenant_id directly.

DO $$
DECLARE
    t TEXT;
BEGIN
    FOR t IN
        SELECT unnest(ARRAY[
            'branches','pos_terminals','roles','users',
            'tax_slabs','categories','brands','attributes','products',
            'product_variants','barcodes','customers',
            'sales_orders','stock_levels','stock_reservations',
            'stock_movements','serial_numbers'
        ])
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY;', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I USING (merchant_id = current_setting(''app.tenant_id'', true)::uuid);',
            t
        );
    END LOOP;
END $$;

-- Tables without a direct merchant_id column (sales_order_lines, payments,
-- role_permissions, user_roles, refresh_tokens, attribute_values,
-- product_components) inherit isolation via their parent FK — enforce
-- through joins in application queries, or add RLS policies against a
-- subquery to the parent table if you want defense-in-depth at the DB layer.
