-- =====================================================================
-- Volume tiers with a fixed discount, and Buy X Get Y across products
-- (phase0_1_design.md §3.76).
--
-- Volume: each tier in promotions.config.tiers may now carry
-- discount_type: "percent" (the default, so every existing tier keeps
-- discount_pct), "amount_per_unit" (₹ off each piece) or "amount" (₹ off
-- the lot), with discount_amount for the last two. Config is JSONB and
-- validated in Go, so that needs no DDL.
--
-- buy_x_get_y: a new promo_type. config: buy_product_ids + buy_qty (the
-- items that qualify), get_product_ids + get_qty (the items rewarded),
-- get_discount_pct (100 = free) and optional max_sets. Buying buy_qty of
-- any X products earns get_qty of the Y products at that discount, the
-- cheapest Y first; the discount sits on the Y lines only. Like bogo it
-- names its own products, so it is stored as application_level 'order'
-- with no category/brand/product filters.
-- =====================================================================

ALTER TABLE promotions DROP CONSTRAINT promotions_promo_type_check;
ALTER TABLE promotions ADD CONSTRAINT promotions_promo_type_check
    CHECK (promo_type IN ('percent', 'fixed', 'bogo', 'volume', 'min_value', 'buy_x_get_y'));
