-- =====================================================================
-- Voiding a held order (POST /sales/orders/{id}/void-held,
-- internal/sales/void_held.go). A held order is a parked cart: no stock
-- has left the shelf (only reservations), nothing is posted to the
-- ledger, no payment was taken. Voiding it releases the reservations and
-- batch allocations and gives back any coupon use or loyalty points it
-- claimed — and keeps the order, its lines and totals as they were, as
-- the record of what was parked and dropped.
--
-- Its own status, held_voided, not 'voided': 'voided' is a reversed sale,
-- counted in sales history totals, the shift X/Z report's void count and
-- the AI anomaly detector's per-cashier void rate (internal/ai). A cart
-- that never became a sale must not inflate any of those — a cashier
-- clearing an abandoned hold isn't voiding a sale. Sales history lists a
-- held_voided order only when asked for that status.
--
-- voided_at/voided_by record who voided and when, for both kinds of void
-- (void_reason already holds the why).
-- =====================================================================

ALTER TABLE sales_orders DROP CONSTRAINT IF EXISTS sales_orders_status_check;
ALTER TABLE sales_orders ADD CONSTRAINT sales_orders_status_check
    CHECK (status IN ('cart', 'held', 'finalized', 'voided', 'refunded', 'partially_refunded', 'held_voided'));

ALTER TABLE sales_orders
    ADD COLUMN IF NOT EXISTS voided_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS voided_by UUID REFERENCES users(id);
