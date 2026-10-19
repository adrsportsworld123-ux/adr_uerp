-- =====================================================================
-- Follow-ups to the GST split (migrations/060):
--
-- 1. Currency per merchant, with a branch override (tax_settings): the
--    ISO 4217 code and the symbol documents, the POS and the back office
--    print. NULL on a branch row inherits; no row at all = INR / ₹, so
--    nothing changes for an existing merchant. Every amount stays
--    NUMERIC(14,2): this names the money, it never converts it.
--
-- 2. Ship-to state on a sale (sales_orders.ship_to_state): for goods
--    delivered to another state the place of supply is where the goods
--    go, not the counter. Set on a cart or held order; when set it wins
--    over the customer's GSTIN state, so a walk-in delivery to another
--    state is inter-state (IGST) and a B2B customer's goods shipped
--    somewhere else follow the delivery. Frozen with the rest of the
--    place of supply at checkout. NULL (every existing sale) = the
--    migrations/060 rule, unchanged.
-- =====================================================================

-- 1. Currency ------------------------------------------------------------

ALTER TABLE tax_settings ADD COLUMN currency_code TEXT CHECK (currency_code IS NULL OR currency_code ~ '^[A-Z]{3}$');
ALTER TABLE tax_settings ADD COLUMN currency_symbol TEXT CHECK (currency_symbol IS NULL OR char_length(btrim(currency_symbol)) BETWEEN 1 AND 5);

-- 2. Ship-to state ------------------------------------------------------

ALTER TABLE sales_orders ADD COLUMN ship_to_state TEXT CHECK (ship_to_state IS NULL OR ship_to_state ~ '^[0-9]{2}$');

CREATE OR REPLACE FUNCTION sale_supply_snapshot() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    branch_state text;
    customer_state text;
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.status NOT IN ('cart', 'held') THEN
        RETURN NEW;  -- finalized (or voided / refunded): the invoice's place of supply is fixed
    END IF;
    branch_state := app_branch_state(NEW.branch_id);
    IF NEW.ship_to_state IS NOT NULL THEN
        customer_state := NEW.ship_to_state;  -- goods delivered elsewhere: the delivery state decides
    ELSIF NEW.customer_id IS NOT NULL THEN
        SELECT gst_state_of(c.gstin) INTO customer_state FROM customers c WHERE c.id = NEW.customer_id;
    END IF;
    NEW.place_of_supply := COALESCE(customer_state, branch_state);
    NEW.inter_state := customer_state IS NOT NULL AND branch_state IS NOT NULL AND customer_state <> branch_state;
    RETURN NEW;
END $$;

DROP TRIGGER sales_orders_supply_snapshot ON sales_orders;
CREATE TRIGGER sales_orders_supply_snapshot
    BEFORE INSERT OR UPDATE OF status, customer_id, branch_id, ship_to_state ON sales_orders
    FOR EACH ROW EXECUTE FUNCTION sale_supply_snapshot();
