-- =====================================================================
-- Whole-order promotions narrowed by category, brand and product
-- (phase0_1_design.md §3.75).
--
-- A promotion with application_level = 'order' may list categories (each
-- with its sub-categories), brands and products. A cart line qualifies
-- when it matches every list that isn't empty: "Badminton" + "YONEX" is
-- YONEX's badminton items; "Badminton" alone is every badminton item. All
-- three empty (the default, and every existing promotion) is the whole
-- order, as before. Product- and category-level promotions ignore them.
--
-- Arrays, not join tables: the lists are read whole with the promotion,
-- never queried on their own, and a category, brand or product deleted
-- later simply stops matching (the promotion never widens to "all").
-- =====================================================================

ALTER TABLE promotions
    ADD COLUMN scope_category_ids UUID[] NOT NULL DEFAULT '{}',
    ADD COLUMN scope_brand_ids    UUID[] NOT NULL DEFAULT '{}',
    ADD COLUMN scope_product_ids  UUID[] NOT NULL DEFAULT '{}';
