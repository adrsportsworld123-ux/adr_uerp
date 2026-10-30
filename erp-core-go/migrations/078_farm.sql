-- =====================================================================
-- Farm management (§3.81): one model for every species — chicken, goat,
-- rabbit, cow, pig and fish — built and proven on chicken first.
--
--   farm_houses     where animals are kept: a shed, pen, pond, tank…
--   farm_batches    a flock, herd or pond stocking: species, purpose
--                   (broiler, layer, dairy, grow-out…), breed, placed on,
--                   how many, from which stock.
--   farm_events     the daily log: mortality, culls, feed, medicine,
--                   vaccination, weights, production (eggs, milk, a
--                   harvest), expenses, notes.
--   farm_schedules  vaccination / medicine plans by age (seeded: broiler
--                   and layer chicken — a typical plan, to confirm with a vet).
--
-- Stock and books: feed, medicine and the animals placed come out of the
-- branch's stock at cost into 1500 Livestock & Farm Work-in-Progress (Dr
-- 1500, Cr 1200 Inventory); what the farm produces goes into stock (Dr
-- 1200, Cr 1500) at its cost; closing a batch writes what is left in 1500
-- for it to 5200 Farm Losses (or 4100 Farm Gains when output was valued
-- above cost). Mortality moves no money: its cost stays on the survivors
-- until the batch closes. An expense event is cost-only (pay it in
-- Expenses).
--
-- Only a branch with a farm business type (migrations/077) can hold
-- these, and a batch only of a species the branch farms. Nothing here
-- touches a shop.
-- =====================================================================

INSERT INTO chart_of_accounts (merchant_id, code, name, account_type, is_system)
SELECT DISTINCT c.merchant_id, v.code, v.name, v.account_type, true
FROM chart_of_accounts c
CROSS JOIN (VALUES ('1500', 'Livestock & Farm Work-in-Progress', 'asset'),
                   ('4100', 'Farm Gains', 'income'),
                   ('5200', 'Farm Losses & Mortality', 'expense')) AS v(code, name, account_type)
ON CONFLICT DO NOTHING;

ALTER TABLE stock_movements DROP CONSTRAINT stock_movements_movement_type_check;
ALTER TABLE stock_movements ADD CONSTRAINT stock_movements_movement_type_check
    CHECK (movement_type IN ('sale', 'adjustment', 'transfer_in', 'transfer_out', 'purchase', 'return', 'farm_use', 'farm_output'));

ALTER TABLE journal_entries DROP CONSTRAINT journal_entries_source_type_check;
ALTER TABLE journal_entries ADD CONSTRAINT journal_entries_source_type_check
    CHECK (source_type IN ('sale', 'sale_void', 'purchase_bill', 'bill_payment', 'purchase_return',
                           'inventory_adjustment', 'manual', 'receivable_payment', 'cash_reconciliation',
                           'payment_gateway_settlement', 'payment_gateway_settlement_reversal',
                           'inventory_reconciliation', 'sales_return', 'cash_movement', 'payment_correction',
                           'manual_reversal', 'cheque_bounce', 'loan_disbursement', 'loan_repayment',
                           'expense', 'expense_payment', 'expense_cancel', 'partner_pay_in', 'partner_pay_out',
                           'farm_use', 'farm_output', 'farm_close', 'farm_cancel'));

CREATE TABLE farm_houses (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id  UUID NOT NULL REFERENCES merchants(id),
    branch_id    UUID NOT NULL REFERENCES branches(id),
    name         TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('shed', 'pen', 'cage', 'paddock', 'pond', 'tank', 'other')),
    species      TEXT NOT NULL CHECK (species IN ('chicken', 'goat', 'rabbit', 'cow', 'pig', 'fish')),
    capacity     INT CHECK (capacity IS NULL OR capacity > 0),
    area_sqft    NUMERIC(12,2) CHECK (area_sqft IS NULL OR area_sqft > 0),
    notes        TEXT,
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_farm_houses_name ON farm_houses (branch_id, lower(name));
ALTER TABLE farm_houses ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_houses USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY branch_scope ON farm_houses AS RESTRICTIVE USING (app_branch_visible(branch_id)) WITH CHECK (app_branch_visible(branch_id));
GRANT SELECT, INSERT, UPDATE ON farm_houses TO erp_app;

CREATE TABLE farm_schedules (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id  UUID NOT NULL REFERENCES merchants(id),
    species      TEXT NOT NULL CHECK (species IN ('chicken', 'goat', 'rabbit', 'cow', 'pig', 'fish')),
    purpose      TEXT,                -- null = any purpose of the species
    name         TEXT NOT NULL,
    is_system    BOOLEAN NOT NULL DEFAULT false,
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE farm_schedules ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_schedules USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON farm_schedules TO erp_app;

CREATE TABLE farm_schedule_items (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id  UUID NOT NULL REFERENCES merchants(id),
    schedule_id  UUID NOT NULL REFERENCES farm_schedules(id),
    day_of_age   INT NOT NULL CHECK (day_of_age >= 0),
    kind         TEXT NOT NULL DEFAULT 'vaccination' CHECK (kind IN ('vaccination', 'medicine', 'task')),
    name         TEXT NOT NULL,
    method       TEXT,                -- eye drop, drinking water, injection, wing web …
    notes        TEXT,
    sort_order   INT NOT NULL DEFAULT 0
);
CREATE INDEX idx_farm_schedule_items ON farm_schedule_items (schedule_id, day_of_age);
ALTER TABLE farm_schedule_items ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_schedule_items USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON farm_schedule_items TO erp_app;

CREATE TABLE farm_batches (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id           UUID NOT NULL REFERENCES merchants(id),
    branch_id             UUID NOT NULL REFERENCES branches(id),
    house_id              UUID REFERENCES farm_houses(id),
    batch_number          TEXT NOT NULL,
    name                  TEXT,
    species               TEXT NOT NULL CHECK (species IN ('chicken', 'goat', 'rabbit', 'cow', 'pig', 'fish')),
    purpose               TEXT NOT NULL,
    breed                 TEXT,
    supplier_id           UUID REFERENCES suppliers(id),
    source                TEXT,                          -- the hatchery, the market … as a note
    placed_on             DATE NOT NULL,
    age_at_placement_days INT NOT NULL DEFAULT 0 CHECK (age_at_placement_days >= 0),
    initial_count         INT NOT NULL CHECK (initial_count > 0),
    initial_avg_weight_kg NUMERIC(10,3) CHECK (initial_avg_weight_kg IS NULL OR initial_avg_weight_kg >= 0),
    schedule_id           UUID REFERENCES farm_schedules(id),
    status                TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'closed')),
    closed_on             DATE,
    close_note            TEXT,
    close_journal_id      UUID REFERENCES journal_entries(id),
    notes                 TEXT,
    created_by            UUID REFERENCES users(id),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, batch_number)
);
CREATE INDEX idx_farm_batches_branch ON farm_batches (branch_id, status);
ALTER TABLE farm_batches ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_batches USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY branch_scope ON farm_batches AS RESTRICTIVE USING (app_branch_visible(branch_id)) WITH CHECK (app_branch_visible(branch_id));
GRANT SELECT, INSERT, UPDATE ON farm_batches TO erp_app;

CREATE TABLE farm_events (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id        UUID NOT NULL REFERENCES merchants(id),
    branch_id          UUID NOT NULL REFERENCES branches(id),
    batch_id           UUID NOT NULL REFERENCES farm_batches(id),
    event_date         DATE NOT NULL,
    event_type         TEXT NOT NULL CHECK (event_type IN ('placement', 'mortality', 'cull', 'feed', 'medicine', 'vaccination',
                                                           'weight', 'production', 'expense', 'note')),
    head_count         INT CHECK (head_count IS NULL OR head_count > 0),   -- animals that died, were culled or left in a harvest
    variant_id         UUID REFERENCES product_variants(id),              -- what was used (feed …) or produced (eggs, milk, live weight)
    quantity           NUMERIC(14,3) CHECK (quantity IS NULL OR quantity > 0),
    unit_cost          NUMERIC(14,2),
    amount             NUMERIC(14,2),                                     -- the cost moved (or an expense's amount)
    avg_weight_kg      NUMERIC(10,3) CHECK (avg_weight_kg IS NULL OR avg_weight_kg > 0),
    sample_size        INT CHECK (sample_size IS NULL OR sample_size > 0),
    cause              TEXT,
    vaccine            TEXT,
    method             TEXT,
    schedule_item_id   UUID REFERENCES farm_schedule_items(id),
    notes              TEXT,
    journal_entry_id   UUID REFERENCES journal_entries(id),
    status             TEXT NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'cancelled')),
    cancelled_by       UUID REFERENCES users(id),
    cancelled_at       TIMESTAMPTZ,
    cancel_reason      TEXT,
    created_by         UUID REFERENCES users(id),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_farm_events_batch ON farm_events (batch_id, event_date);
CREATE INDEX idx_farm_events_branch_date ON farm_events (branch_id, event_date);
ALTER TABLE farm_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_events USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY branch_scope ON farm_events AS RESTRICTIVE USING (app_branch_visible(branch_id)) WITH CHECK (app_branch_visible(branch_id));
GRANT SELECT, INSERT, UPDATE ON farm_events TO erp_app;

CREATE TABLE farm_counters (
    merchant_id UUID PRIMARY KEY REFERENCES merchants(id),
    last_no     INT NOT NULL
);
ALTER TABLE farm_counters ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_counters USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON farm_counters TO erp_app;

-- Typical chicken plans (subscriptions.systemFarmSchedules gives a new
-- merchant the same). Common Indian practice; confirm with your vet.
INSERT INTO farm_schedules (merchant_id, species, purpose, name, is_system)
SELECT m.id, 'chicken', v.purpose, v.name, true FROM merchants m
CROSS JOIN (VALUES ('broiler', 'Broiler (42 days)'), ('layer', 'Layer (to point of lay)')) AS v(purpose, name);

INSERT INTO farm_schedule_items (merchant_id, schedule_id, day_of_age, kind, name, method, sort_order)
SELECT s.merchant_id, s.id, v.day, 'vaccination', v.name, v.method, v.ord
FROM farm_schedules s
CROSS JOIN (VALUES
    (1, 'Marek''s disease (HVT)', 'Injection, at the hatchery', 1),
    (6, 'Newcastle disease (F1 / Lasota)', 'Eye or nasal drop', 2),
    (13, 'Gumboro (IBD, intermediate)', 'Drinking water', 3),
    (21, 'Newcastle disease booster (Lasota)', 'Drinking water', 4),
    (26, 'Gumboro booster (IBD)', 'Drinking water', 5)
) AS v(day, name, method, ord)
WHERE s.species = 'chicken' AND s.purpose = 'broiler' AND s.is_system;

INSERT INTO farm_schedule_items (merchant_id, schedule_id, day_of_age, kind, name, method, sort_order)
SELECT s.merchant_id, s.id, v.day, 'vaccination', v.name, v.method, v.ord
FROM farm_schedules s
CROSS JOIN (VALUES
    (1, 'Marek''s disease (HVT)', 'Injection, at the hatchery', 1),
    (5, 'Newcastle disease (F1 / Lasota)', 'Eye or nasal drop', 2),
    (12, 'Gumboro (IBD, intermediate)', 'Drinking water', 3),
    (21, 'Newcastle disease booster (Lasota)', 'Drinking water', 4),
    (26, 'Gumboro booster (IBD)', 'Drinking water', 5),
    (35, 'Newcastle disease (Lasota)', 'Drinking water', 6),
    (49, 'Fowl pox', 'Wing web', 7),
    (56, 'Newcastle disease (R2B)', 'Injection', 8),
    (70, 'Infectious coryza', 'Injection', 9),
    (112, 'Newcastle disease (R2B) booster', 'Injection', 10),
    (126, 'EDS + ND + IB (killed, before lay)', 'Injection', 11)
) AS v(day, name, method, ord)
WHERE s.species = 'chicken' AND s.purpose = 'layer' AND s.is_system;

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000078', 'farm.manage', 'Set up sheds and ponds, flocks and herds, schedules, and close a batch'),
    ('f0000000-0000-0000-0000-000000000079', 'farm.record', 'Record the farm''s daily log: mortality, feed, medicine, vaccination, weights, eggs, milk')
ON CONFLICT (code) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name IN ('Merchant Admin', 'Branch Manager') AND p.code IN ('farm.manage', 'farm.record')
ON CONFLICT DO NOTHING;
INSERT INTO role_templates (role_name, permission_code) VALUES ('Branch Manager', 'farm.manage'), ('Branch Manager', 'farm.record')
ON CONFLICT DO NOTHING;
