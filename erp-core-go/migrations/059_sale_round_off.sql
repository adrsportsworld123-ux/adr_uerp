-- =====================================================================
-- Sale round-off waiver: a bill of ₹105.75 settled for ₹105, the ₹0.75
-- waived. Configurable per merchant (sales_settings), at most ₹5 a sale.
--
-- The invoice itself is not changed. subtotal, discount, tax and
-- grand_total stay exactly as billed, so GST is untouched and
-- subtotal - discount + tax = grand_total still holds. The waiver is a
-- separate, signed adjustment:
--
--     sales_orders.round_off   0.00 or negative (-0.75 = ₹0.75 waived)
--     amount payable           grand_total + round_off   (₹105.00)
--
-- The tenders cover the amount payable. The sale's journal entry debits
-- the waiver to Round Off (5005, the account supplier round-off already
-- uses), so the entry still balances: Dr tenders + Dr Round Off =
-- Cr Sales Revenue + Cr GST Payable. A void or a full return gives the
-- waiver back (sales_returns.round_off), so a customer is never refunded
-- more than they paid.
--
-- Additive: every existing sale has round_off 0, so payable equals
-- grand_total and every report reads exactly as before. The feature is
-- off until a merchant sets round_off_max above 0.
-- =====================================================================

ALTER TABLE sales_orders ADD COLUMN round_off NUMERIC(14,2) NOT NULL DEFAULT 0;
ALTER TABLE sales_orders ADD CONSTRAINT sales_orders_round_off_check CHECK (round_off <= 0 AND round_off >= -5);

-- The part of the sale's waiver a return gave back: the refund is the
-- returned goods' value less this (never negative).
ALTER TABLE sales_returns ADD COLUMN round_off NUMERIC(14,2) NOT NULL DEFAULT 0;
ALTER TABLE sales_returns ADD CONSTRAINT sales_returns_round_off_check CHECK (round_off >= 0 AND round_off <= 5);

-- round_off_max: the most a cashier may waive on one sale; 0 = off.
-- round_off_auto: the till offers rounding the bill down to the rupee by
-- default (still within round_off_max).
ALTER TABLE sales_settings ADD COLUMN round_off_max NUMERIC(4,2) NOT NULL DEFAULT 0;
ALTER TABLE sales_settings ADD CONSTRAINT sales_settings_round_off_max_check CHECK (round_off_max >= 0 AND round_off_max <= 5);
ALTER TABLE sales_settings ADD COLUMN round_off_auto BOOLEAN NOT NULL DEFAULT false;

-- Round Off exists for every merchant since migrations/048 and new ones
-- get it at signup (subscriptions/provision.go); repeated here so this
-- migration is self-contained.
INSERT INTO chart_of_accounts (merchant_id, code, name, account_type, is_system)
SELECT DISTINCT c.merchant_id, '5005', 'Round Off', 'expense', true
FROM chart_of_accounts c
WHERE c.code = '1001'
ON CONFLICT (merchant_id, code) DO NOTHING;
