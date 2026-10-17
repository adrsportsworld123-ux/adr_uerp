-- =====================================================================
-- One lifecycle for every master: create, edit, activate/deactivate and
-- delete — the Phase 11 pattern categories, brands and tax slabs already
-- follow, now for customers, suppliers, branches, employees, the chart of
-- accounts, shifts and product attributes.
--
-- POS terminals keep activate/deactivate only: pos_terminals has no RLS
-- (PIN login reads it before any tenant is known), so it can't take the
-- not_deleted policy. Deleting a branch deactivates its terminals.
--
-- Delete is the soft delete of migrations/050 (deleted_at/deleted_by, an
-- audit_logs entry with the row, hidden by a RESTRICTIVE RLS policy,
-- restorable from the Recycle Bin), and only for a record nothing uses:
-- every handler counts the foreign keys pointing at it first
-- (internal/dependencies) and answers 409 IN_USE, "deactivate instead",
-- otherwise. Deactivate keeps a record and its history but takes it out
-- of pickers and new transactions.
--
-- Shifts and attributes had no status; they get one. Uniqueness over live
-- rows only, so a deleted branch code, employee email or phone doesn't
-- block reusing it. Chart of accounts codes stay fully unique: migrations
-- seed accounts with ON CONFLICT (merchant_id, code), and a ledger code
-- shouldn't be reused anyway.
-- =====================================================================

ALTER TABLE shifts
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive'));
ALTER TABLE attributes
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive'));

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'customers', 'suppliers', 'branches', 'users', 'chart_of_accounts', 'shifts', 'attributes'
    ] LOOP
        EXECUTE format('ALTER TABLE %I ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ', t);
        EXECUTE format('ALTER TABLE %I ADD COLUMN IF NOT EXISTS deleted_by UUID REFERENCES users(id)', t);
        EXECUTE format('DROP POLICY IF EXISTS not_deleted ON %I', t);
        EXECUTE format(
            'CREATE POLICY not_deleted ON %I AS RESTRICTIVE USING (deleted_at IS NULL OR app_include_deleted()) '
            'WITH CHECK (deleted_at IS NULL OR app_include_deleted())', t);
    END LOOP;
END $$;

ALTER TABLE branches DROP CONSTRAINT IF EXISTS branches_merchant_id_code_key;
CREATE UNIQUE INDEX IF NOT EXISTS uq_branches_code ON branches (merchant_id, code) WHERE deleted_at IS NULL;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_merchant_id_email_key;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_merchant_id_phone_key;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_merchant_id_employee_code_key;
CREATE UNIQUE INDEX IF NOT EXISTS uq_users_email ON users (merchant_id, email) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_users_phone ON users (merchant_id, phone) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_users_employee_code ON users (merchant_id, employee_code) WHERE deleted_at IS NULL;

-- Deleting customers, suppliers and branches (the masters with no
-- domain permission of their own; the others use hr.manage,
-- accounting.manage, catalog.manage and terminal.manage).
INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000054', 'master_data.delete', 'Delete unused customers, suppliers and branches (moved to the Recycle Bin)')
ON CONFLICT (code) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'Merchant Admin' AND p.code = 'master_data.delete'
ON CONFLICT DO NOTHING;
