-- =====================================================================
-- Phase 2, sub-area 3: Multi-Branch
-- (phased_roadmap.md Phase 2; pos_frd_complete.md's Inter-Branch Transfer
-- workflow under Inventory Management)
--
-- "Branch hierarchy fully live" turned out to mean something concrete and
-- previously missing: branches table has existed since 001_schema.sql,
-- but there has never been an API to create or manage one — only raw SQL
-- seeding. That gap closes here alongside the transfer workflow.
--
-- Workflow, exactly as FRD specifies: pending_approval -> approved ->
-- in_transit (stock deducted from source) -> completed (stock added to
-- destination) — plus rejected/cancelled off-ramps before dispatch.
-- Deliberately no journal entry for a normal transfer: both branches
-- share one merchant-level chart of accounts, so moving inventory between
-- them doesn't change the total Inventory Asset value — only a
-- discrepancy (received != sent, i.e. breakage/loss in transit) is a real
-- economic event and gets journaled (see internal/branches/transfers.go).
-- =====================================================================

CREATE TABLE branch_transfers (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    from_branch_id      UUID NOT NULL REFERENCES branches(id),
    to_branch_id        UUID NOT NULL REFERENCES branches(id),
    transfer_number     TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'pending_approval'
                            CHECK (status IN ('pending_approval','approved','rejected','cancelled','in_transit','completed')),
    notes               TEXT,
    rejection_reason    TEXT,
    requested_by        UUID REFERENCES users(id),
    approved_by         UUID REFERENCES users(id),
    dispatched_by       UUID REFERENCES users(id),
    completed_by        UUID REFERENCES users(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    approved_at         TIMESTAMPTZ,
    dispatched_at       TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    UNIQUE (merchant_id, transfer_number),
    CHECK (from_branch_id != to_branch_id)
);

CREATE TABLE branch_transfer_lines (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    branch_transfer_id      UUID NOT NULL REFERENCES branch_transfers(id) ON DELETE CASCADE,
    variant_id              UUID NOT NULL REFERENCES product_variants(id),
    requested_quantity      NUMERIC(14,3) NOT NULL,
    sent_quantity           NUMERIC(14,3),   -- set on dispatch; may be less than requested if source can't fully cover it
    received_quantity       NUMERIC(14,3),   -- set on completion; may differ from sent_quantity (transit loss/breakage)
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_branch_transfers_from    ON branch_transfers(from_branch_id, status);
CREATE INDEX idx_branch_transfers_to      ON branch_transfers(to_branch_id, status);
CREATE INDEX idx_branch_transfer_lines_bt ON branch_transfer_lines(branch_transfer_id);

ALTER TABLE branch_transfers ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON branch_transfers
    USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE branch_transfer_lines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON branch_transfer_lines
    USING (branch_transfer_id IN (SELECT id FROM branch_transfers));

GRANT SELECT, INSERT, UPDATE, DELETE ON branch_transfers, branch_transfer_lines TO erp_app;

-- New permission: approving a transfer is the one FRD-specified approval
-- gate in this workflow ("Manager/Admin approves"). Dispatch/complete are
-- deliberately left open to any authenticated user, same as GRN/bills —
-- branch-scoping who can act on which branch's transfer would need
-- password-login sessions to carry a branch_id, which they don't today
-- (see internal/authn/claims.go — only PIN-login sets one). Documented
-- simplification, not an oversight.
INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000003', 'branch_transfer.approve', 'Approve or reject an inter-branch transfer request')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.merchant_id = '11111111-1111-1111-1111-111111111111'
  AND r.name IN ('Branch Manager', 'Merchant Admin')
  AND p.code = 'branch_transfer.approve'
ON CONFLICT DO NOTHING;

-- A second branch for the seeded merchant, so the transfer workflow is
-- actually testable out of the box instead of requiring manual SQL first.
INSERT INTO branches (id, merchant_id, name, code, timezone)
VALUES ('33333333-1111-2222-3333-444444444444', '11111111-1111-1111-1111-111111111111', 'Koramangala', 'KOR', 'Asia/Kolkata');
