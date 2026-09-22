-- =====================================================================
-- Phase 4, sub-area 1: B2B Credit Facility
-- (phased_roadmap.md Phase 4; pos_frd_complete.md §6's "Credit Facility
-- (B2B)" and §8's "Payment Terms"/"Aging"/"Credit Hold")
--
-- This is the piece migrations/011_customers.sql explicitly deferred to
-- Phase 4 ("credit facility/B2B payment terms (bundled with Phase 4's
-- accounting depth)") and internal/notifications' payment-reminder
-- sweeper explicitly deferred too (migrations/013's header comment:
-- "customer-facing payment reminders stay deferred to Phase 4 with credit
-- facility itself") — both close here.
--
-- Design: a checkout payment can now use method='credit' instead of
-- cash/card/upi/bank_transfer/cheque — that portion books to the
-- Receivable account (already seeded since Phase 2's
-- migrations/007_accounting.sql, just never used until now) instead of a
-- cash/clearing account, tagged with party_type='customer' so it shows on
-- that customer's existing GET /accounting/party-ledger (built in Phase
-- 2, generic from day one — this migration is its first real customer-side
-- user). sales_orders.credit_amount/credit_paid are a fast, denormalized
-- source for credit-limit checks and aging — mirroring purchase_bills'
-- own amount_paid/status columns (Phase 2) for the payable side of this
-- exact same shape — while journal_lines stays the source of truth for
-- the actual ledger. Deliberately NOT a stored "status" column the way
-- purchase_bills has one: outstanding = credit_amount - credit_paid is
-- cheap to compute on read and sales_orders.status already means
-- something else (cart/finalized/voided/refunded) — a second
-- similarly-named column would be confusing, not clarifying.
--
-- Also NOT built here, matching the roadmap's own "if you have suppliers
-- who need it" hedge: formal PO approval workflow. Genuinely optional
-- per the roadmap's own wording, unlike the rest of this migration.
-- =====================================================================

ALTER TABLE customers
    ADD COLUMN credit_limit  NUMERIC(14,2) NOT NULL DEFAULT 0,
    ADD COLUMN payment_terms TEXT NOT NULL DEFAULT 'due_on_receipt'
        CHECK (payment_terms IN ('due_on_receipt', 'net_7', 'net_15', 'net_30', 'net_60', 'net_90')),
    -- Manual override, independent of the auto-block credit_limit check
    -- below — staff can freeze a customer with a dispute even if they're
    -- technically still within limit.
    ADD COLUMN credit_hold   BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE sales_orders
    ADD COLUMN due_date              DATE,                     -- set only when this order has a credit portion
    ADD COLUMN credit_amount         NUMERIC(14,2) NOT NULL DEFAULT 0,
    ADD COLUMN credit_paid           NUMERIC(14,2) NOT NULL DEFAULT 0,
    ADD COLUMN last_reminder_sent_at TIMESTAMPTZ;               -- same dedup shape as purchase_bills' own column

-- payments.method's original CHECK (001_schema.sql) was ('cash','card','upi')
-- with an explicit "expand in later phases" comment — this is that
-- expansion. 'credit' is the only addition: a checkout payment using it
-- books to Receivable (internal/sales/handlers.go's Checkout) instead of
-- being captured immediately.
ALTER TABLE payments DROP CONSTRAINT payments_method_check;
ALTER TABLE payments ADD CONSTRAINT payments_method_check CHECK (method IN ('cash', 'card', 'upi', 'credit'));

-- journal_entries.source_type (007_accounting.sql) also needs widening —
-- internal/customers.RecordReceivablePayment posts under 'receivable_payment',
-- the receivable-side mirror of 'bill_payment' (the payable side, already
-- in this list).
ALTER TABLE journal_entries DROP CONSTRAINT journal_entries_source_type_check;
ALTER TABLE journal_entries ADD CONSTRAINT journal_entries_source_type_check
    CHECK (source_type IN ('sale', 'sale_void', 'purchase_bill', 'bill_payment', 'purchase_return', 'inventory_adjustment', 'manual', 'receivable_payment'));

CREATE TABLE customer_receivable_payments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    customer_id     UUID NOT NULL REFERENCES customers(id),
    sales_order_id  UUID NOT NULL REFERENCES sales_orders(id),
    amount          NUMERIC(14,2) NOT NULL,
    method          TEXT NOT NULL CHECK (method IN ('cash', 'card', 'upi', 'bank_transfer', 'cheque')),
    reference_no    TEXT,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    recorded_by     UUID NOT NULL REFERENCES users(id)
);
CREATE INDEX idx_customer_receivable_payments_order ON customer_receivable_payments(sales_order_id);

ALTER TABLE customer_receivable_payments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_receivable_payments USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON customer_receivable_payments TO erp_app;

-- New permission: credit_limit/payment_terms/credit_hold are the controls
-- that decide whether a customer can walk out with unpaid goods — same
-- "real business risk" reasoning as pricing.manage/promotions.manage.
-- Recording a payment against an existing receivable (POST
-- /customers/{id}/payments) is deliberately NOT gated, mirroring
-- POST /purchase/bills/{id}/payments' own precedent (a routine operational
-- entry, not a configuration change).
INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000009', 'credit.manage', 'Change a customer''s credit limit, payment terms, or credit hold')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.merchant_id = '11111111-1111-1111-1111-111111111111'
  AND r.name IN ('Branch Manager', 'Merchant Admin')
  AND p.code = 'credit.manage'
ON CONFLICT DO NOTHING;
