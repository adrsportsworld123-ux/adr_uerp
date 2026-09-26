-- Dev seed data — one merchant, branch, POS terminal, user, and one
-- barcoded product, matching the walk-through in README.md.
--
-- IMPORTANT: password_hash below is a PLACEHOLDER, not a real bcrypt hash
-- of any password — there is no "default password" that works against it.
-- Before you can log in, set a real one. Easiest path (see README.md
-- "Dev-only password endpoints"): with the server running and
-- DEV_AUTH_TOOLS_ENABLED=true (already set in docker-compose.yml),
--
--   curl -s localhost:8080/dev/set-password \
--     -H 'Content-Type: application/json' \
--     -d '{"merchant_code":"acme-sports","email":"ravi@acme-sports.test","new_password":"Passw0rd!"}'
--
-- That endpoint does not exist unless DEV_AUTH_TOOLS_ENABLED=true, and
-- must stay false/unset anywhere but your own machine (see README.md for
-- why). Without it, generate a hash yourself and UPDATE this row directly:
--   go run -exec "" - <<'GO'   // or any throwaway main.go
--   package main
--   import ("fmt"; "golang.org/x/crypto/bcrypt")
--   func main() {
--       h, _ := bcrypt.GenerateFromPassword([]byte("Passw0rd!"), bcrypt.DefaultCost)
--       fmt.Println(string(h))
--   }
--   GO
-- then UPDATE users SET password_hash = '<output>' WHERE email = 'ravi@acme-sports.test';

INSERT INTO merchants (id, code, legal_name, trade_name)
VALUES ('11111111-1111-1111-1111-111111111111', 'acme-sports', 'Acme Sports Pvt Ltd', 'Acme Sports');

INSERT INTO branches (id, merchant_id, name, code)
VALUES ('22222222-2222-2222-2222-222222222222', '11111111-1111-1111-1111-111111111111', 'MG Road', 'MGR');

INSERT INTO pos_terminals (id, merchant_id, branch_id, name)
VALUES ('33333333-3333-3333-3333-333333333333', '11111111-1111-1111-1111-111111111111', '22222222-2222-2222-2222-222222222222', 'Till 1');

INSERT INTO roles (id, merchant_id, name) VALUES
  ('44444444-4444-4444-4444-444444444444', '11111111-1111-1111-1111-111111111111', 'POS User'),
  ('bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb', '11111111-1111-1111-1111-111111111111', 'Branch Manager'),
  ('dddddddd-dddd-dddd-dddd-dddddddddddd', '11111111-1111-1111-1111-111111111111', 'Merchant Admin');

-- employee_code/pin_hash are for PIN quick-login (POST /auth/pin-login) —
-- pin_hash below is the same kind of placeholder as password_hash: not a
-- real bcrypt hash of any PIN. Set a real one via the /dev/set-password-style
-- flow before testing pin-login (see README.md).
INSERT INTO users (id, merchant_id, branch_id, name, email, employee_code, password_hash, pin_hash)
VALUES ('55555555-5555-5555-5555-555555555555', '11111111-1111-1111-1111-111111111111',
        '22222222-2222-2222-2222-222222222222', 'Ravi Kumar', 'ravi@acme-sports.test', 'EMP001',
        '$2a$10$placeholderplaceholderplaceholderplaceholderp',
        '$2a$10$placeholderplaceholderplaceholderplaceholderp');

INSERT INTO user_roles (user_id, role_id)
VALUES ('55555555-5555-5555-5555-555555555555', '44444444-4444-4444-4444-444444444444');

-- A Branch Manager and Merchant Admin, so the tiered discount
-- (POST /sales/orders/{id}/discounts) and inventory-adjustment
-- authorization paths are testable without hand-inserting roles/users
-- first. Same password/PIN placeholder caveat as Ravi's row above.
INSERT INTO users (id, merchant_id, branch_id, name, email, employee_code, password_hash, pin_hash)
VALUES
  ('cccccccc-cccc-cccc-cccc-cccccccccccc', '11111111-1111-1111-1111-111111111111',
   '22222222-2222-2222-2222-222222222222', 'Meera Nair', 'meera@acme-sports.test', 'EMP002',
   '$2a$10$placeholderplaceholderplaceholderplaceholderp',
   '$2a$10$placeholderplaceholderplaceholderplaceholderp'),
  ('eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee', '11111111-1111-1111-1111-111111111111',
   NULL, 'Arjun Rao', 'arjun@acme-sports.test', 'EMP003',
   '$2a$10$placeholderplaceholderplaceholderplaceholderp',
   '$2a$10$placeholderplaceholderplaceholderplaceholderp');

INSERT INTO user_roles (user_id, role_id) VALUES
  ('cccccccc-cccc-cccc-cccc-cccccccccccc', 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb'),
  ('eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee', 'dddddddd-dddd-dddd-dddd-dddddddddddd');

-- Real superadmin login, unlike every other row in this file — added
-- 2026-09-24 after the account was created live via POST /hr/employees
-- (Phase 5) and then lost when this dev environment's Postgres volume
-- reset (confirmed 4 times now: the container's start timestamp jumps
-- forward and only this file's seed rows survive, since
-- docker-entrypoint-initdb.d replays every migration on a genuinely
-- fresh volume — see docker-compose.yml's comment on the mount — but a
-- runtime-created row like this admin account, made through the API
-- rather than a migration, has nothing to replay it from). Putting it
-- here means the NEXT reset recreates it automatically instead of
-- needing another manual POST /hr/employees + role-assign cycle.
-- password_hash below is a REAL bcrypt hash (via POST /dev/hash-password),
-- not the placeholder every other row in this file uses — this is the
-- one seed account meant to actually be logged into on a fresh
-- environment without any extra setup step.
--
-- Deliberately only the base columns 001_schema.sql already defines
-- (no designation/department/employment_type — those are
-- migrations/023_hr_payroll.sql ALTER TABLE additions that don't exist
-- yet at the point in a fresh-volume replay where this file runs,
-- 002 before 023) — a real bug caught before it shipped: the first
-- version of this INSERT referenced those columns and would have broken
-- every fresh install, not just this account.
INSERT INTO users (id, merchant_id, branch_id, name, email, employee_code, password_hash, pin_hash)
VALUES ('00000000-0000-0000-0000-000000000001', '11111111-1111-1111-1111-111111111111',
        NULL, 'Super Admin', 'admin@adrsw.com', 'SUPERADMIN',
        '$2a$10$2ewqndn9FP2Xzo1wQG187O3Hny8JlC9ycvhZO8jgc6w4REQRdW.dW', NULL);

INSERT INTO user_roles (user_id, role_id)
VALUES ('00000000-0000-0000-0000-000000000001', 'dddddddd-dddd-dddd-dddd-dddddddddddd');

INSERT INTO tax_slabs (id, merchant_id, name, cgst_rate, sgst_rate)
VALUES ('66666666-6666-6666-6666-666666666666', '11111111-1111-1111-1111-111111111111', 'GST 18%', 9, 9);

INSERT INTO categories (id, merchant_id, name)
VALUES ('77777777-7777-7777-7777-777777777777', '11111111-1111-1111-1111-111111111111', 'Cricket');

INSERT INTO products (id, merchant_id, category_id, tax_slab_id, name, hsn_code, product_type)
VALUES ('88888888-8888-8888-8888-888888888888', '11111111-1111-1111-1111-111111111111',
        '77777777-7777-7777-7777-777777777777', '66666666-6666-6666-6666-666666666666',
        'SG Cricket Bat', '9506', 'variant');

INSERT INTO product_variants (id, merchant_id, product_id, sku, attribute_combo, cost_price, mrp, selling_price)
VALUES ('99999999-9999-9999-9999-999999999999', '11111111-1111-1111-1111-111111111111',
        '88888888-8888-8888-8888-888888888888', 'SG-BAT-SH', '{"Size":"Short Handle"}', 1200.00, 2499.00, 2299.00);

INSERT INTO barcodes (id, merchant_id, variant_id, code, symbology)
VALUES ('aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa', '11111111-1111-1111-1111-111111111111',
        '99999999-9999-9999-9999-999999999999', '8901234567890', 'EAN13');

-- Initial stock so the cart/checkout walkthrough in README.md has something
-- to reserve against. In Phase 2 this comes from Purchase/GRN instead of a
-- manual seed row.
INSERT INTO stock_levels (merchant_id, branch_id, variant_id, on_hand, reserved, version)
VALUES ('11111111-1111-1111-1111-111111111111', '22222222-2222-2222-2222-222222222222',
        '99999999-9999-9999-9999-999999999999', 10, 0, 0);
