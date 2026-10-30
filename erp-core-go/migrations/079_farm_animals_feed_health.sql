-- =====================================================================
-- Farm, round 2 (§3.82): animals one by one, breeding and milk; feed
-- management (plans, stock cover, mixing your own feed); medicine
-- management (farm items, treatments, withdrawal periods, expiry); a
-- pond's water log; schedules that repeat (a herd's 6-monthly FMD).
--
-- Nothing here changes what 078 does with an existing batch: its head
-- count still comes from its log, and the new event types (birth,
-- addition, transfers) only add to it.
--
--   farm_animals         a cow, goat, pig or breeding rabbit by its tag:
--                        sex, breed, birth date, mother, father, its herd
--                        (a batch), status (active, dead, culled,
--                        harvested). Its head is one of its batch's heads.
--   farm_breeding        heat, service (natural or AI), pregnancy checks,
--                        births (the young join the register), abortion,
--                        drying off.
--   farm_milk_records    litres per animal per milking; the day's milk
--                        goes into stock as one production event.
--   farm_items           which products are feed, medicine or vaccine,
--                        with a medicine's withdrawal days for meat, milk
--                        and eggs.
--   farm_feed_plans      grams a head a day by age (or a fish pond's % of
--                        its body weight), one or more feeds per age.
--   farm_feed_recipes /  your own feed: ingredients into a mixed feed in
--   farm_feed_mixes      stock at their combined cost (no ledger entry:
--                        stock to stock at the same value).
--   farm_water_logs      a pond's dissolved oxygen, pH, temperature,
--                        ammonia …
--
-- Withdrawal: a medicine or vaccination event keeps the dates until which
-- its batch (or one animal) mustn't give meat, milk or eggs. Recording
-- produce in that time is refused (WITHDRAWAL_PERIOD) unless someone with
-- farm.manage overrides it with a reason, which the event keeps.
--
-- The standard feed plans and the goat, cattle, pig and rabbit health
-- schedules are typical Indian practice, to confirm with a vet and a
-- nutritionist; the farm edits them.
-- =====================================================================

-- ---- a batch-tracked feed or medicine (078 left farm moves out) ----------
ALTER TABLE stock_batch_movements DROP CONSTRAINT stock_batch_movements_reference_type_check;
ALTER TABLE stock_batch_movements ADD CONSTRAINT stock_batch_movements_reference_type_check
    CHECK (reference_type IN ('adjustment', 'inventory_reconciliation', 'purchase_return', 'transfer_out',
                              'transfer_in', 'void', 'offline_sale', 'sales_return', 'farm_event', 'farm_feed_mix'));

-- ---- the log: animals, treatments, withdrawal, produce kind ------------
ALTER TABLE farm_events DROP CONSTRAINT farm_events_event_type_check;
ALTER TABLE farm_events ADD CONSTRAINT farm_events_event_type_check
    CHECK (event_type IN ('placement', 'mortality', 'cull', 'feed', 'medicine', 'vaccination', 'weight', 'production', 'expense', 'note',
                          'birth', 'addition', 'transfer_in', 'transfer_out'));

ALTER TABLE farm_events
    ADD COLUMN animal_id            UUID,
    ADD COLUMN produce_kind         TEXT CHECK (produce_kind IN ('meat', 'milk', 'eggs', 'other')),
    ADD COLUMN dose                 TEXT,
    ADD COLUMN course_days          INT CHECK (course_days IS NULL OR course_days > 0),
    ADD COLUMN vet                  TEXT,
    ADD COLUMN withdrawal_meat_until DATE,
    ADD COLUMN withdrawal_milk_until DATE,
    ADD COLUMN withdrawal_eggs_until DATE,
    ADD COLUMN withdrawal_override  TEXT,   -- produce recorded during a withdrawal: why (farm.manage)
    ADD COLUMN related_batch_id     UUID REFERENCES farm_batches(id);   -- a transfer's other batch

-- ---- schedules that repeat ---------------------------------------------
ALTER TABLE farm_schedule_items ADD COLUMN repeat_every_days INT CHECK (repeat_every_days IS NULL OR repeat_every_days > 0);

-- ---- farm settings -----------------------------------------------------
CREATE TABLE farm_settings (
    merchant_id      UUID PRIMARY KEY REFERENCES merchants(id),
    feed_low_days    INT NOT NULL DEFAULT 7 CHECK (feed_low_days > 0),       -- feed left for fewer days = low
    expiry_warn_days INT NOT NULL DEFAULT 30 CHECK (expiry_warn_days > 0),   -- medicine expiring within = warn
    updated_by       UUID REFERENCES users(id),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE farm_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_settings USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON farm_settings TO erp_app;

-- ---- animals -----------------------------------------------------------
CREATE TABLE farm_animals (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id   UUID NOT NULL REFERENCES merchants(id),
    branch_id     UUID NOT NULL REFERENCES branches(id),
    batch_id      UUID NOT NULL REFERENCES farm_batches(id),
    tag           TEXT NOT NULL,
    name          TEXT,
    species       TEXT NOT NULL CHECK (species IN ('goat', 'rabbit', 'cow', 'pig')),
    sex           TEXT NOT NULL CHECK (sex IN ('female', 'male')),
    breed         TEXT,
    born_on       DATE,
    origin        TEXT NOT NULL DEFAULT 'existing' CHECK (origin IN ('existing', 'purchased', 'born')),
    joined_on     DATE NOT NULL,
    dam_id        UUID REFERENCES farm_animals(id),
    sire_id       UUID REFERENCES farm_animals(id),
    sire_note     TEXT,                    -- a bull or buck not on the farm, AI straw …
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'dead', 'culled', 'harvested')),
    exit_on       DATE,
    exit_event_id UUID REFERENCES farm_events(id),
    notes         TEXT,
    created_by    UUID REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_farm_animals_tag ON farm_animals (merchant_id, lower(tag));
CREATE INDEX idx_farm_animals_batch ON farm_animals (batch_id, status);
ALTER TABLE farm_animals ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_animals USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY branch_scope ON farm_animals AS RESTRICTIVE USING (app_branch_visible(branch_id)) WITH CHECK (app_branch_visible(branch_id));
GRANT SELECT, INSERT, UPDATE ON farm_animals TO erp_app;

ALTER TABLE farm_events ADD CONSTRAINT farm_events_animal_fk FOREIGN KEY (animal_id) REFERENCES farm_animals(id);
CREATE INDEX idx_farm_events_animal ON farm_events (animal_id) WHERE animal_id IS NOT NULL;

CREATE TABLE farm_breeding (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id   UUID NOT NULL REFERENCES merchants(id),
    branch_id     UUID NOT NULL REFERENCES branches(id),
    animal_id     UUID NOT NULL REFERENCES farm_animals(id),
    event_date    DATE NOT NULL,
    event_type    TEXT NOT NULL CHECK (event_type IN ('heat', 'service', 'pregnancy_check', 'birth', 'abortion', 'dry_off')),
    method        TEXT CHECK (method IN ('natural', 'ai')),
    sire_id       UUID REFERENCES farm_animals(id),
    sire_note     TEXT,
    result        TEXT CHECK (result IN ('positive', 'negative')),
    born_alive    INT CHECK (born_alive IS NULL OR born_alive >= 0),
    born_dead     INT CHECK (born_dead IS NULL OR born_dead >= 0),
    birth_event_id UUID REFERENCES farm_events(id),      -- the heads the birth added to the herd
    notes         TEXT,
    status        TEXT NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'cancelled')),
    cancel_reason TEXT,
    created_by    UUID REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_farm_breeding_animal ON farm_breeding (animal_id, event_date);
ALTER TABLE farm_breeding ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_breeding USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY branch_scope ON farm_breeding AS RESTRICTIVE USING (app_branch_visible(branch_id)) WITH CHECK (app_branch_visible(branch_id));
GRANT SELECT, INSERT, UPDATE ON farm_breeding TO erp_app;

CREATE TABLE farm_milk_records (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    branch_id   UUID NOT NULL REFERENCES branches(id),
    batch_id    UUID NOT NULL REFERENCES farm_batches(id),
    animal_id   UUID NOT NULL REFERENCES farm_animals(id),
    event_id    UUID NOT NULL REFERENCES farm_events(id),   -- the day's production event
    milked_on   DATE NOT NULL,
    session     TEXT NOT NULL CHECK (session IN ('am', 'pm', 'day')),
    litres      NUMERIC(10,3) NOT NULL CHECK (litres >= 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_farm_milk_animal ON farm_milk_records (animal_id, milked_on);
CREATE INDEX idx_farm_milk_event ON farm_milk_records (event_id);
ALTER TABLE farm_milk_records ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_milk_records USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY branch_scope ON farm_milk_records AS RESTRICTIVE USING (app_branch_visible(branch_id)) WITH CHECK (app_branch_visible(branch_id));
GRANT SELECT, INSERT, UPDATE, DELETE ON farm_milk_records TO erp_app;

-- ---- farm items: feed, medicine, vaccine -------------------------------
CREATE TABLE farm_items (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id          UUID NOT NULL REFERENCES merchants(id),
    product_id           UUID NOT NULL REFERENCES products(id),
    role                 TEXT NOT NULL CHECK (role IN ('feed', 'medicine', 'vaccine')),
    species              TEXT[],                   -- NULL = any
    withdrawal_meat_days INT CHECK (withdrawal_meat_days IS NULL OR withdrawal_meat_days >= 0),
    withdrawal_milk_days INT CHECK (withdrawal_milk_days IS NULL OR withdrawal_milk_days >= 0),
    withdrawal_eggs_days INT CHECK (withdrawal_eggs_days IS NULL OR withdrawal_eggs_days >= 0),
    dose_note            TEXT,
    notes                TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, product_id)
);
ALTER TABLE farm_items ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_items USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON farm_items TO erp_app;

-- ---- feed plans ----------------------------------------------------------
CREATE TABLE farm_feed_plans (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    species     TEXT NOT NULL CHECK (species IN ('chicken', 'goat', 'rabbit', 'cow', 'pig', 'fish')),
    purpose     TEXT,
    name        TEXT NOT NULL,
    basis       TEXT NOT NULL DEFAULT 'per_head' CHECK (basis IN ('per_head', 'pct_biomass')),
    is_system   BOOLEAN NOT NULL DEFAULT false,
    status      TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE farm_feed_plans ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_feed_plans USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON farm_feed_plans TO erp_app;

CREATE TABLE farm_feed_plan_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    plan_id         UUID NOT NULL REFERENCES farm_feed_plans(id),
    day_from        INT NOT NULL CHECK (day_from >= 0),
    day_to          INT CHECK (day_to IS NULL OR day_to >= day_from),   -- NULL = onwards
    feed_name       TEXT NOT NULL,                                       -- Pre-starter, Concentrate, Green fodder …
    product_id      UUID REFERENCES products(id),                        -- the feed in stock, once chosen
    grams_per_head  NUMERIC(10,2) CHECK (grams_per_head IS NULL OR grams_per_head > 0),
    pct_body_weight NUMERIC(6,3) CHECK (pct_body_weight IS NULL OR pct_body_weight > 0),
    sort_order      INT NOT NULL DEFAULT 0,
    CHECK (grams_per_head IS NOT NULL OR pct_body_weight IS NOT NULL)
);
CREATE INDEX idx_farm_feed_plan_items ON farm_feed_plan_items (plan_id, day_from);
ALTER TABLE farm_feed_plan_items ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_feed_plan_items USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON farm_feed_plan_items TO erp_app;

ALTER TABLE farm_batches ADD COLUMN feed_plan_id UUID REFERENCES farm_feed_plans(id);

-- ---- your own feed: recipes and mixes ----------------------------------
CREATE TABLE farm_feed_recipes (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id       UUID NOT NULL REFERENCES merchants(id),
    name              TEXT NOT NULL,
    output_variant_id UUID NOT NULL REFERENCES product_variants(id),
    batch_size        NUMERIC(14,3) NOT NULL CHECK (batch_size > 0),   -- in the output's unit
    notes             TEXT,
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_farm_feed_recipes_name ON farm_feed_recipes (merchant_id, lower(name));
ALTER TABLE farm_feed_recipes ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_feed_recipes USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE ON farm_feed_recipes TO erp_app;

CREATE TABLE farm_feed_recipe_items (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    recipe_id   UUID NOT NULL REFERENCES farm_feed_recipes(id),
    variant_id  UUID NOT NULL REFERENCES product_variants(id),
    quantity    NUMERIC(14,3) NOT NULL CHECK (quantity > 0),          -- for one batch_size of output
    sort_order  INT NOT NULL DEFAULT 0
);
ALTER TABLE farm_feed_recipe_items ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_feed_recipe_items USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON farm_feed_recipe_items TO erp_app;

CREATE TABLE farm_feed_mixes (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id       UUID NOT NULL REFERENCES merchants(id),
    branch_id         UUID NOT NULL REFERENCES branches(id),
    mix_number        TEXT NOT NULL,
    recipe_id         UUID REFERENCES farm_feed_recipes(id),
    output_variant_id UUID NOT NULL REFERENCES product_variants(id),
    output_qty        NUMERIC(14,3) NOT NULL CHECK (output_qty > 0),
    total_cost        NUMERIC(14,2) NOT NULL,
    unit_cost         NUMERIC(14,2) NOT NULL,
    mixed_on          DATE NOT NULL,
    notes             TEXT,
    status            TEXT NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'cancelled')),
    cancel_reason     TEXT,
    cancelled_by      UUID REFERENCES users(id),
    cancelled_at      TIMESTAMPTZ,
    created_by        UUID REFERENCES users(id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, mix_number)
);
ALTER TABLE farm_feed_mixes ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_feed_mixes USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY branch_scope ON farm_feed_mixes AS RESTRICTIVE USING (app_branch_visible(branch_id)) WITH CHECK (app_branch_visible(branch_id));
GRANT SELECT, INSERT, UPDATE ON farm_feed_mixes TO erp_app;

CREATE TABLE farm_feed_mix_lines (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    mix_id      UUID NOT NULL REFERENCES farm_feed_mixes(id),
    variant_id  UUID NOT NULL REFERENCES product_variants(id),
    quantity    NUMERIC(14,3) NOT NULL CHECK (quantity > 0),
    unit_cost   NUMERIC(14,2) NOT NULL,
    amount      NUMERIC(14,2) NOT NULL
);
ALTER TABLE farm_feed_mix_lines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_feed_mix_lines USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT ON farm_feed_mix_lines TO erp_app;

ALTER TABLE farm_counters ADD COLUMN last_mix_no INT NOT NULL DEFAULT 0;

-- ---- a pond's water --------------------------------------------------------
CREATE TABLE farm_water_logs (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id    UUID NOT NULL REFERENCES merchants(id),
    branch_id      UUID NOT NULL REFERENCES branches(id),
    house_id       UUID NOT NULL REFERENCES farm_houses(id),
    logged_on      DATE NOT NULL,
    logged_time    TIME,
    do_mg_l        NUMERIC(6,2),    -- dissolved oxygen
    ph             NUMERIC(4,2),
    temp_c         NUMERIC(5,2),
    ammonia_mg_l   NUMERIC(6,3),
    nitrite_mg_l   NUMERIC(6,3),
    transparency_cm NUMERIC(6,1),
    notes          TEXT,
    created_by     UUID REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_farm_water_house ON farm_water_logs (house_id, logged_on);
ALTER TABLE farm_water_logs ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON farm_water_logs USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY branch_scope ON farm_water_logs AS RESTRICTIVE USING (app_branch_visible(branch_id)) WITH CHECK (app_branch_visible(branch_id));
GRANT SELECT, INSERT, UPDATE, DELETE ON farm_water_logs TO erp_app;

-- ---- standard feed plans (typical practice; confirm with a nutritionist) --
-- subscriptions.systemFarmFeedPlans gives a new merchant the same.
INSERT INTO farm_feed_plans (merchant_id, species, purpose, name, basis, is_system)
SELECT m.id, v.species, v.purpose, v.name, v.basis, true FROM merchants m
CROSS JOIN (VALUES
    ('chicken', 'broiler',   'Broiler feeding (42 days)', 'per_head'),
    ('chicken', 'layer',     'Layer feeding',             'per_head'),
    ('goat',    NULL,        'Goat feeding',              'per_head'),
    ('cow',     'dairy',     'Dairy cattle feeding',      'per_head'),
    ('pig',     'fattening', 'Pig fattening',             'per_head'),
    ('rabbit',  NULL,        'Rabbit feeding',            'per_head'),
    ('fish',    'grow_out',  'Fish grow-out (% of body weight)', 'pct_biomass')
) AS v(species, purpose, name, basis);

INSERT INTO farm_feed_plan_items (merchant_id, plan_id, day_from, day_to, feed_name, grams_per_head, pct_body_weight, sort_order)
SELECT p.merchant_id, p.id, v.d1, v.d2, v.feed, v.grams, v.pct, v.ord
FROM farm_feed_plans p
JOIN (VALUES
    ('Broiler feeding (42 days)', 0, 10, 'Broiler pre-starter', 25::numeric, NULL::numeric, 1),
    ('Broiler feeding (42 days)', 11, 24, 'Broiler starter', 85, NULL, 2),
    ('Broiler feeding (42 days)', 25, NULL, 'Broiler finisher', 160, NULL, 3),
    ('Layer feeding', 0, 56, 'Chick mash', 30, NULL, 1),
    ('Layer feeding', 57, 126, 'Grower mash', 65, NULL, 2),
    ('Layer feeding', 127, 140, 'Pre-layer mash', 90, NULL, 3),
    ('Layer feeding', 141, NULL, 'Layer mash', 110, NULL, 4),
    ('Goat feeding', 15, 90, 'Kid creep feed', 100, NULL, 1),
    ('Goat feeding', 91, 365, 'Concentrate', 250, NULL, 2),
    ('Goat feeding', 91, 365, 'Green fodder', 2000, NULL, 3),
    ('Goat feeding', 366, NULL, 'Concentrate', 400, NULL, 4),
    ('Goat feeding', 366, NULL, 'Green fodder', 4000, NULL, 5),
    ('Goat feeding', 366, NULL, 'Dry fodder', 1000, NULL, 6),
    ('Dairy cattle feeding', 0, 90, 'Calf starter', 500, NULL, 1),
    ('Dairy cattle feeding', 91, 365, 'Concentrate', 1500, NULL, 2),
    ('Dairy cattle feeding', 91, 365, 'Green fodder', 10000, NULL, 3),
    ('Dairy cattle feeding', 366, NULL, 'Concentrate (cattle feed)', 4000, NULL, 4),
    ('Dairy cattle feeding', 366, NULL, 'Green fodder', 25000, NULL, 5),
    ('Dairy cattle feeding', 366, NULL, 'Dry fodder (straw)', 6000, NULL, 6),
    ('Pig fattening', 0, 90, 'Pig starter', 1000, NULL, 1),
    ('Pig fattening', 91, 140, 'Pig grower', 1800, NULL, 2),
    ('Pig fattening', 141, NULL, 'Pig finisher', 2500, NULL, 3),
    ('Rabbit feeding', 30, 60, 'Rabbit pellets', 80, NULL, 1),
    ('Rabbit feeding', 61, 90, 'Rabbit pellets', 120, NULL, 2),
    ('Rabbit feeding', 91, NULL, 'Rabbit pellets', 150, NULL, 3),
    ('Rabbit feeding', 30, NULL, 'Green fodder', 200, NULL, 4),
    ('Fish grow-out (% of body weight)', 0, 30, 'Fish starter feed', NULL, 5, 1),
    ('Fish grow-out (% of body weight)', 31, 90, 'Fish grower feed', NULL, 3, 2),
    ('Fish grow-out (% of body weight)', 91, 180, 'Fish grower feed', NULL, 2, 3),
    ('Fish grow-out (% of body weight)', 181, NULL, 'Fish finisher feed', NULL, 1.5, 4)
) AS v(plan, d1, d2, feed, grams, pct, ord) ON v.plan = p.name
WHERE p.is_system;

-- ---- health schedules for the other species (typical; confirm with a vet) --
-- subscriptions.systemFarmSchedules gives a new merchant the same.
INSERT INTO farm_schedules (merchant_id, species, purpose, name, is_system)
SELECT m.id, v.species, NULL, v.name, true FROM merchants m
CROSS JOIN (VALUES ('goat', 'Goat health'), ('cow', 'Cattle health'), ('pig', 'Pig health'), ('rabbit', 'Rabbit health')) AS v(species, name);

INSERT INTO farm_schedule_items (merchant_id, schedule_id, day_of_age, kind, name, method, repeat_every_days, sort_order)
SELECT s.merchant_id, s.id, v.day, v.kind, v.name, v.method, v.rep, v.ord
FROM farm_schedules s
JOIN (VALUES
    ('Goat health', 60, 'medicine', 'Deworming', 'Oral drench', 90, 1),
    ('Goat health', 90, 'vaccination', 'Goat pox', 'Injection', 365, 2),
    ('Goat health', 120, 'vaccination', 'PPR', 'Injection', 1095, 3),
    ('Goat health', 120, 'vaccination', 'Enterotoxaemia (ET)', 'Injection', 365, 4),
    ('Goat health', 141, 'vaccination', 'Enterotoxaemia (ET) booster', 'Injection', NULL, 5),
    ('Goat health', 120, 'vaccination', 'Foot and mouth disease (FMD)', 'Injection', 180, 6),
    ('Goat health', 180, 'vaccination', 'Haemorrhagic septicaemia (HS)', 'Injection', 365, 7),
    ('Cattle health', 10, 'medicine', 'Deworming', 'Oral', 90, 1),
    ('Cattle health', 90, 'vaccination', 'Theileriosis', 'Injection', NULL, 2),
    ('Cattle health', 120, 'vaccination', 'Foot and mouth disease (FMD)', 'Injection', 180, 3),
    ('Cattle health', 150, 'vaccination', 'FMD booster', 'Injection', NULL, 4),
    ('Cattle health', 150, 'vaccination', 'Brucellosis (female calves, once)', 'Injection', NULL, 5),
    ('Cattle health', 120, 'vaccination', 'Lumpy skin disease (LSD)', 'Injection', 365, 6),
    ('Cattle health', 180, 'vaccination', 'Haemorrhagic septicaemia (HS)', 'Injection', 365, 7),
    ('Cattle health', 180, 'vaccination', 'Black quarter (BQ)', 'Injection', 365, 8),
    ('Pig health', 3, 'medicine', 'Iron injection', 'Injection', NULL, 1),
    ('Pig health', 7, 'task', 'Castration (male piglets)', NULL, NULL, 2),
    ('Pig health', 42, 'vaccination', 'Classical swine fever (CSF)', 'Injection', 365, 3),
    ('Pig health', 45, 'medicine', 'Deworming', 'Oral', 90, 4),
    ('Pig health', 60, 'vaccination', 'Foot and mouth disease (FMD)', 'Injection', 180, 5),
    ('Rabbit health', 30, 'medicine', 'Coccidiosis prevention', 'Drinking water', NULL, 1),
    ('Rabbit health', 45, 'medicine', 'Deworming', 'Oral', 90, 2)
) AS v(sched, day, kind, name, method, rep, ord) ON v.sched = s.name
WHERE s.is_system AND s.species IN ('goat', 'cow', 'pig', 'rabbit');

-- ---- permissions -----------------------------------------------------------
-- farm.manage / farm.record cover all of it (078); nothing new to grant.
