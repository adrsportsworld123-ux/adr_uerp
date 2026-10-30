-- =====================================================================
-- What each branch does (§3.80): one or more business types per branch —
-- a retail shop, wholesale, grocery, pharmacy, a warehouse, or a farm
-- (poultry, goat, rabbit, dairy, pig, fish). A type switches on its
-- features; the farm screens and endpoints work only at a branch with a
-- farm type, and a flock or herd only at a branch farming that species.
--
-- business_types is the platform's catalogue (no merchant, like
-- permissions). Every existing branch becomes a Retail shop, and every
-- screen it uses today stays exactly as it is: the existing screens are
-- never hidden by a type; only the new farm ones need one.
-- =====================================================================

CREATE TABLE business_types (
    code        TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL,
    features    TEXT[] NOT NULL,
    species     TEXT,            -- a farm type: the animal it farms
    sort_order  INT NOT NULL DEFAULT 0
);
GRANT SELECT ON business_types TO erp_app;

INSERT INTO business_types (code, name, description, features, species, sort_order) VALUES
    ('retail',       'Retail shop',             'Sells to walk-in customers at the till.',                         '{retail,pos,inventory,purchase}', NULL, 1),
    ('wholesale',    'Wholesale',               'Sells in bulk to businesses: quotations, price lists, credit.',   '{wholesale,pos,inventory,purchase}', NULL, 2),
    ('grocery',      'Grocery / FMCG',          'Loose and packed goods by weight, batches and expiry.',           '{grocery,pos,inventory,purchase,batches,weighing}', NULL, 3),
    ('pharmacy',     'Pharmacy',                'Medicines with batches, expiry and prescriptions.',               '{pharmacy,pos,inventory,purchase,batches,prescriptions}', NULL, 4),
    ('warehouse',    'Warehouse',               'Holds and moves stock; no till sales.',                           '{warehouse,inventory,purchase}', NULL, 5),
    ('poultry_farm', 'Poultry farm',            'Broiler and layer flocks: feed, vaccination, mortality, eggs.',   '{farm,farm_poultry,inventory,purchase}', 'chicken', 10),
    ('goat_farm',    'Goat farm',               'Goat herds: feed, health, kidding, weights.',                     '{farm,farm_goat,inventory,purchase}', 'goat', 11),
    ('rabbit_farm',  'Rabbit farm',             'Rabbit colonies: feed, health, litters, weights.',                '{farm,farm_rabbit,inventory,purchase}', 'rabbit', 12),
    ('dairy_farm',   'Dairy farm',              'Cattle: feed, health, milk.',                                     '{farm,farm_dairy,inventory,purchase}', 'cow', 13),
    ('pig_farm',     'Pig farm',                'Pig herds: feed, health, farrowing, weights.',                    '{farm,farm_pig,inventory,purchase}', 'pig', 14),
    ('fish_farm',    'Fish farm (aquaculture)', 'Ponds and tanks: stocking, feed, water, harvest.',                '{farm,farm_fish,inventory,purchase}', 'fish', 15);

CREATE TABLE branch_business_types (
    merchant_id  UUID NOT NULL REFERENCES merchants(id),
    branch_id    UUID NOT NULL REFERENCES branches(id),
    type_code    TEXT NOT NULL REFERENCES business_types(code),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (branch_id, type_code)
);
ALTER TABLE branch_business_types ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON branch_business_types USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, DELETE ON branch_business_types TO erp_app;

INSERT INTO branch_business_types (merchant_id, branch_id, type_code)
SELECT merchant_id, id, 'retail' FROM branches;

-- The features of a branch, and whether it has one.
CREATE FUNCTION app_branch_features(b uuid) RETURNS text[] LANGUAGE sql STABLE AS $$
    SELECT COALESCE(array_agg(DISTINCT f), '{}') FROM branch_business_types bt
    JOIN business_types t ON t.code = bt.type_code, unnest(t.features) f
    WHERE bt.branch_id = b
$$;

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000077', 'branches.configure', 'Set what each branch does (its business types: retail, wholesale, a farm …)')
ON CONFLICT (code) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'Merchant Admin' AND p.code = 'branches.configure'
ON CONFLICT DO NOTHING;
