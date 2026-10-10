-- =====================================================================
-- Phase 15: data-scoped access, "own branch vs all branches"
-- (phased_roadmap.md Phase 15; pos_frd_complete.md §18). Closes Phase 2's
-- recorded "known simplification" that only a PIN session knew its branch.
--
-- Branch scope is a second row-level-security layer under the tenant one,
-- enforced by the database for every list, report, export and write, not
-- per handler:
--
--   * roles.data_scope: 'all' or 'branch'. A user sees all branches if ANY
--     of their roles is 'all'; otherwise their branches are their home
--     branch (users.branch_id), any extra ones in user_branches, and for a
--     PIN session the terminal's branch.
--   * The API puts the result in app.branch_scope for each request's
--     transaction ('all', or a comma list of branch ids; 'none' when a
--     scoped user has no branch at all). Unset means no restriction:
--     background jobs and unauthenticated paths, which never act for a user.
--   * A RESTRICTIVE policy on each branch-bearing transactional table ANDs
--     with the tenant policy: rows of other branches are invisible, and
--     can't be inserted or updated into either (WITH CHECK). Child tables
--     (order lines, payments, return lines ...) reach their rows through
--     their parent's policy and are scoped with it.
--
-- Deliberately merchant-wide (no branch policy): master data (products,
-- customers, suppliers, branches, users: approvers and names must resolve
-- across branches), promotions/coupons/price rules (configuration), HR
-- shift definitions. pos_terminals has no RLS (pairing is unauthenticated)
-- and is scoped in its handler instead.
--
-- Defaults keep every existing role's behaviour (data_scope 'all') except
-- Branch Manager, which FRD §18 describes as own-branch.
-- =====================================================================

ALTER TABLE roles ADD COLUMN data_scope TEXT NOT NULL DEFAULT 'all' CHECK (data_scope IN ('all', 'branch'));
UPDATE roles SET data_scope = 'branch' WHERE name = 'Branch Manager';

-- Extra branches a scoped user covers (an area manager over three stores).
CREATE TABLE user_branches (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    branch_id   UUID NOT NULL REFERENCES branches(id),
    merchant_id UUID NOT NULL REFERENCES merchants(id),
    PRIMARY KEY (user_id, branch_id)
);
ALTER TABLE user_branches ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON user_branches USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, DELETE ON user_branches TO erp_app;

-- True when this transaction may see/write rows of branch b. Unset or
-- 'all' = everything; otherwise b must be in the list (a NULL branch, a
-- merchant-level row, is only for unrestricted sessions).
CREATE OR REPLACE FUNCTION app_branch_visible(b uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT CASE
        WHEN COALESCE(current_setting('app.branch_scope', true), '') IN ('', 'all') THEN true
        ELSE b IS NOT NULL AND b::text = ANY (string_to_array(current_setting('app.branch_scope', true), ','))
    END
$$;

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'attendance', 'cash_reconciliations', 'goods_receipt_notes', 'inventory_reconciliations',
        'journal_entries', 'journal_vouchers', 'pos_cash_movements', 'pos_shifts', 'purchase_bills',
        'purchase_returns', 'quotations', 'sales_orders', 'sales_returns', 'serial_numbers',
        'stock_batches', 'stock_levels', 'stock_movements', 'stock_reservations', 'stock_review_items']
    LOOP
        EXECUTE format('CREATE POLICY branch_scope ON %I AS RESTRICTIVE USING (app_branch_visible(branch_id)) WITH CHECK (app_branch_visible(branch_id))', t);
    END LOOP;
END $$;

-- A transfer belongs to both ends: either branch's people see it and act on it.
CREATE POLICY branch_scope ON branch_transfers AS RESTRICTIVE
    USING (app_branch_visible(from_branch_id) OR app_branch_visible(to_branch_id))
    WITH CHECK (app_branch_visible(from_branch_id) OR app_branch_visible(to_branch_id));

-- The sent-notification log has no branch of its own; a notification is
-- visible when what it's about is (found by the Phase 15 leak sweep: a
-- receipt to a branch 2 customer named branch 2's order to branch 1).
-- The EXISTS subqueries run under the same scope, so they answer exactly
-- "can this session see that row".
CREATE POLICY branch_scope ON notifications AS RESTRICTIVE
    USING (
        COALESCE(current_setting('app.branch_scope', true), '') IN ('', 'all')
        OR CASE reference_type
            WHEN 'sales_order'   THEN EXISTS (SELECT 1 FROM sales_orders so WHERE so.id = reference_id)
            WHEN 'stock_levels'  THEN EXISTS (SELECT 1 FROM stock_levels sl WHERE sl.id = reference_id)
            WHEN 'purchase_bill' THEN EXISTS (SELECT 1 FROM purchase_bills pb WHERE pb.id = reference_id)
            ELSE true
        END)
    WITH CHECK (true);
