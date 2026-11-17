-- =====================================================================
-- Whether barcode labels print the selling price (§3.89). Per branch, in
-- its print settings: branch_print_settings.label_show_price. true (the
-- default, and every existing branch) = labels print it, as today; false
-- = the label carries the MRP (and the rest) but no selling price, e.g.
-- for a shop that sells below MRP and changes prices often. A Label
-- Studio job can still choose for itself.
-- =====================================================================

ALTER TABLE branch_print_settings ADD COLUMN IF NOT EXISTS label_show_price BOOLEAN NOT NULL DEFAULT true;

-- The default for a branch that never saved print settings.
CREATE OR REPLACE FUNCTION app_label_show_price(b uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT COALESCE((SELECT label_show_price FROM branch_print_settings WHERE branch_id = b), true)
$$;
