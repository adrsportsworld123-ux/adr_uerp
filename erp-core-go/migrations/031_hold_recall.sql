-- Phase 9: POS Hold & Recall (internal/sales/hold.go).
--
-- A cashier can park an in-progress cart ("hold") to serve another customer
-- and come back to it later ("recall") without losing the lines already
-- added. This is a genuinely new status, not a reuse of 'cart', because a
-- held order must be excluded from the normal cart-editing endpoints
-- (AddLine/DeleteLine/Checkout all require status = 'cart') until it's
-- explicitly recalled back into one.
ALTER TABLE sales_orders DROP CONSTRAINT sales_orders_status_check;
ALTER TABLE sales_orders ADD CONSTRAINT sales_orders_status_check
    CHECK (status IN ('cart','held','finalized','voided','refunded'));

-- held_at drives the "held N minutes ago" display on the recall list;
-- hold_note is an optional free-text label the cashier can attach (e.g. a
-- customer name) so a busy recall list is actually navigable.
ALTER TABLE sales_orders ADD COLUMN held_at   TIMESTAMPTZ;
ALTER TABLE sales_orders ADD COLUMN hold_note TEXT;

-- Powers GET /sales/orders/held?branch_id= (the recall list) without a
-- full-table scan as held carts accumulate across a busy day.
CREATE INDEX idx_sales_orders_held ON sales_orders(merchant_id, branch_id, status) WHERE status = 'held';
