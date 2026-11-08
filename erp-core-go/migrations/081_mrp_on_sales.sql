-- =====================================================================
-- MRP on the sales screen and receipts.
--
-- 1. sales_order_lines.mrp: the item's MRP (with GST, per unit) as it was
--    when the line went into the cart, so a receipt reprinted after an MRP
--    change still shows what the shelf said. A BEFORE INSERT trigger copies
--    it from product_variants for every writer (till, web sale, offline
--    sync) — none of them change. NULL on every existing line and for an
--    item without an MRP: those print exactly as before.
--
-- 2. branch_print_settings.show_mrp_discount: whether the branch shows
--    "Discount on MRP" (MRP less the selling price) and "You saved" on the
--    sales screen and on receipts (A4, 80 mm, thermal slip). false (the
--    default, and every existing row) = as before. The MRP itself shows
--    in the sales grid either way; it is information, not a total.
--
-- Neither changes any amount: subtotal, discount, tax and total are what
-- they were. The savings are worked out when a sale is read.
-- =====================================================================

-- 1. ---------------------------------------------------------------------

ALTER TABLE sales_order_lines ADD COLUMN mrp NUMERIC(14,2) CHECK (mrp IS NULL OR mrp > 0);

CREATE OR REPLACE FUNCTION app_sales_line_mrp() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.mrp IS NULL THEN
        SELECT NULLIF(pv.mrp, 0) INTO NEW.mrp FROM product_variants pv WHERE pv.id = NEW.variant_id;
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER sales_order_lines_mrp BEFORE INSERT ON sales_order_lines
    FOR EACH ROW EXECUTE FUNCTION app_sales_line_mrp();

-- 2. ---------------------------------------------------------------------

ALTER TABLE branch_print_settings ADD COLUMN show_mrp_discount BOOLEAN NOT NULL DEFAULT false;
