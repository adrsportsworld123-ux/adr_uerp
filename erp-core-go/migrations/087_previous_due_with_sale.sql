-- =====================================================================
-- Collecting a customer's previous outstanding with the current sale
-- (§3.92).
--
-- When a sale has a customer who still owes on earlier credit sales, the
-- till and the back office show that outstanding, and the cashier keeps it
-- on this bill (all or part of it collected now) or leaves it off. What is
-- collected is applied to the customer's open credit sales oldest first,
-- through customers.ApplyReceivablePayment — the same payment rows and
-- journal (Dr cash/bank, Cr Accounts Receivable) as collecting each sale
-- on its own — so the current sale's own total, tax and tenders are
-- untouched.
--
--   sales_orders.previous_due            the customer's outstanding before
--                                        this sale, when it was kept on
--                                        the bill (0 = not kept: every
--                                        existing sale);
--   sales_orders.previous_due_collected  how much of it was collected;
--   customer_receivable_payments.collected_with_order_id
--                                        the sale a payment was collected
--                                        with, for its receipt (NULL for
--                                        every other payment).
-- Additive: a sale that doesn't keep it reads and books as before.
-- =====================================================================

ALTER TABLE sales_orders
    ADD COLUMN IF NOT EXISTS previous_due NUMERIC(14,2) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS previous_due_collected NUMERIC(14,2) NOT NULL DEFAULT 0;

ALTER TABLE customer_receivable_payments
    ADD COLUMN IF NOT EXISTS collected_with_order_id UUID REFERENCES sales_orders(id);
CREATE INDEX IF NOT EXISTS idx_customer_receivable_payments_collected_with
    ON customer_receivable_payments (collected_with_order_id) WHERE collected_with_order_id IS NOT NULL;
