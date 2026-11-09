-- =====================================================================
-- Collecting credit sales from the sale itself, and negative sales.
--
-- 1. A credit sale's payment can now be recorded from the sale (the POS
--    sales history and the admin's sale page), not only from the
--    customer's page: POST /sales/orders/{id}/credit-payments, through the
--    same customers.ApplyReceivablePayment as POST /customers/{id}/payments.
--    customer_receivable_payments gains
--      pos_terminal_id  the till that took the money (NULL for a payment
--                       recorded in the back office, and every existing
--                       row). Cash a till takes counts in that till's
--                       drawer (accounting.ComputeCashFlow); back-office
--                       payments stay out of it, as before.
--      idempotency_key  a POS retry records the payment once.
--
-- 2. branch_sales_settings.allow_negative_stock: the branch may sell more
--    than it has in stock (on_hand goes negative, as an offline sale
--    already can, §3.5). NULL / false (the default, every existing branch)
--    = refuse, as today. A batch-tracked item still needs real, unexpired
--    batch stock: expiry and recall depend on knowing the batch.
--    app_allow_negative_stock(branch) resolves it. A sale that takes stock
--    below zero opens a stock review item, the same queue an offline
--    oversell uses (migrations/041).
-- =====================================================================

ALTER TABLE customer_receivable_payments
    ADD COLUMN pos_terminal_id UUID REFERENCES pos_terminals(id),
    ADD COLUMN idempotency_key TEXT;
CREATE UNIQUE INDEX uq_customer_receivable_payments_idem
    ON customer_receivable_payments (merchant_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX idx_customer_receivable_payments_terminal
    ON customer_receivable_payments (pos_terminal_id, received_at) WHERE pos_terminal_id IS NOT NULL;

ALTER TABLE branch_sales_settings ADD COLUMN allow_negative_stock BOOLEAN;

CREATE OR REPLACE FUNCTION app_allow_negative_stock(b uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT COALESCE((SELECT allow_negative_stock FROM branch_sales_settings WHERE branch_id = b), false)
$$;
