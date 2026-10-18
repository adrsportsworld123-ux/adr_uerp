-- =====================================================================
-- Releases a stuck, unpaid sales order (status 'cart' or 'held') — the
-- ops counterpart of POST /sales/orders/{id}/void-held
-- (internal/sales/void_held.go), for a cart the POS can no longer reach
-- (the API has no endpoint to drop an open cart). Run it through
-- scripts/release-order.ps1, which sets the psql variables:
--
--   order_id  the sales_orders.id
--   reason    why (stored in void_reason and the audit trail)
--   by_email  the user it's recorded against ('' = nobody)
--   commit    'yes' to commit; anything else rolls back (dry run)
--
-- One transaction, same steps as VoidHeld: active stock reservations are
-- released (stock_levels.reserved goes down), remaining batch allocations
-- are restored, coupon use and redeemed loyalty points are given back,
-- and the order becomes held_voided with who/when/why — kept out of sales
-- history, reports and void counts, and never recallable. Lines and
-- totals stay as the record. A paid or finalized order is refused: a
-- finished sale is voided from the web admin (POST /sales/orders/{id}/void).
-- =====================================================================

\set ON_ERROR_STOP on
SET client_min_messages = notice;

BEGIN;

SELECT set_config('ops.order_id', :'order_id', true) AS ops_a,
       set_config('ops.reason', :'reason', true) AS ops_b,
       set_config('ops.by_email', :'by_email', true) AS ops_c \gset

DO $$
DECLARE
    v_order_id uuid := current_setting('ops.order_id')::uuid;
    v_reason   text := btrim(current_setting('ops.reason'));
    v_email    text := btrim(current_setting('ops.by_email'));
    o          record;
    v_by       uuid;
    r          record;
    n_res      int := 0;
    n_batch    int := 0;
    n_coupon   int := 0;
    n_loyalty  int := 0;
    v_prev     text;
    v_now      timestamptz := now();
    v_before   jsonb;
    v_after    jsonb;
BEGIN
    IF v_reason = '' THEN
        RAISE EXCEPTION 'a reason is required';
    END IF;

    SELECT so.id, so.merchant_id, so.branch_id, so.status, so.order_number, so.grand_total, so.cashier_id
      INTO o FROM sales_orders so WHERE so.id = v_order_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'order % not found', v_order_id;
    END IF;
    IF o.status NOT IN ('cart', 'held') THEN
        RAISE EXCEPTION 'order % is %, not an open cart or held order — nothing to release (a finalized sale is voided from the web admin)', o.order_number, o.status;
    END IF;
    IF EXISTS (SELECT 1 FROM payments WHERE sales_order_id = o.id AND status = 'captured') THEN
        RAISE EXCEPTION 'order % has a captured payment — refusing; settle it through the POS / web admin', o.order_number;
    END IF;

    IF v_email <> '' THEN
        SELECT id INTO v_by FROM users WHERE merchant_id = o.merchant_id AND lower(email) = lower(v_email);
        IF v_by IS NULL THEN
            RAISE EXCEPTION 'user % not found in this merchant', v_email;
        END IF;
    END IF;

    -- RLS context, as the API sets it (audit_logs reads app.tenant_id).
    PERFORM set_config('app.tenant_id', o.merchant_id::text, true);

    -- 1. Active reservations back to sellable stock.
    FOR r IN SELECT id, variant_id, quantity FROM stock_reservations
             WHERE sales_order_id = o.id AND status = 'active' LOOP
        UPDATE stock_levels
           SET reserved = GREATEST(reserved - r.quantity, 0), version = version + 1, updated_at = now()
         WHERE branch_id = o.branch_id AND variant_id = r.variant_id;
        UPDATE stock_reservations SET status = 'released' WHERE id = r.id;
        n_res := n_res + 1;
    END LOOP;

    -- 2. Batch allocations still held by the lines (the expiry sweeper
    --    deletes the ones it already restored, so nothing is restored twice).
    FOR r IN SELECT b.batch_id, b.quantity FROM sales_order_line_batches b
             JOIN sales_order_lines l ON l.id = b.sales_order_line_id
             WHERE l.sales_order_id = o.id LOOP
        UPDATE stock_batches SET quantity_remaining = quantity_remaining + r.quantity WHERE id = r.batch_id;
        n_batch := n_batch + 1;
    END LOOP;
    DELETE FROM sales_order_line_batches b USING sales_order_lines l
     WHERE l.id = b.sales_order_line_id AND l.sales_order_id = o.id;

    -- 3. Coupon use and redeemed loyalty points given back.
    UPDATE coupons c SET usage_count = GREATEST(c.usage_count - x.n, 0)
      FROM (SELECT coupon_id, count(*) AS n FROM coupon_redemptions WHERE sales_order_id = o.id GROUP BY coupon_id) x
     WHERE c.id = x.coupon_id;
    DELETE FROM coupon_redemptions WHERE sales_order_id = o.id;
    GET DIAGNOSTICS n_coupon = ROW_COUNT;
    DELETE FROM loyalty_ledger WHERE sales_order_id = o.id AND entry_type = 'redeem';
    GET DIAGNOSTICS n_loyalty = ROW_COUNT;

    -- 4. The order itself.
    UPDATE sales_orders SET status = 'held_voided', void_reason = v_reason, voided_at = v_now, voided_by = v_by
     WHERE id = o.id;

    -- 5. Hash-chained audit entry — the same checksum formula as
    --    internal/audit/log.go, so the chain still verifies.
    v_before := jsonb_build_object('status', o.status, 'order_number', o.order_number,
                                   'grand_total', o.grand_total::text, 'cashier_id', COALESCE(o.cashier_id::text, ''));
    v_after  := jsonb_build_object('status', 'held_voided', 'reservations_released', n_res, 'via', 'scripts/release-order');
    SELECT checksum INTO v_prev FROM audit_logs WHERE merchant_id = o.merchant_id ORDER BY created_at DESC, id DESC LIMIT 1;
    INSERT INTO audit_logs (merchant_id, entity_type, entity_id, action, performed_by, before_value, after_value, reason, created_at, checksum, prev_checksum)
    VALUES (o.merchant_id, 'sales_orders', o.id, 'update', v_by, v_before, v_after, v_reason, v_now,
            encode(digest(concat_ws('|', v_prev, o.merchant_id::text, 'sales_orders', o.id::text, 'update', v_by::text,
                                    v_before::text, v_after::text, v_reason, v_now::text), 'sha256'), 'hex'),
            v_prev);

    RAISE NOTICE 'order % (%): % -> held_voided; reservations released: %, batch allocations restored: %, coupon redemptions given back: %, loyalty redemptions given back: %',
        o.order_number, o.id, o.status, n_res, n_batch, n_coupon, n_loyalty;
END $$;

SELECT id, order_number, status, void_reason, voided_at FROM sales_orders WHERE id = :'order_id'::uuid;

\if :{?commit}
\else
\set commit no
\endif
SELECT (:'commit' = 'yes') AS do_commit \gset
\if :do_commit
COMMIT;
\echo 'COMMITTED.'
\else
ROLLBACK;
\echo 'DRY RUN - rolled back. Re-run with -Commit to apply.'
\endif
