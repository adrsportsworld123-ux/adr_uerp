-- =====================================================================
-- Phase 3, sub-area 1: Customer Management
-- (phased_roadmap.md Phase 3; pos_frd_complete.md §6)
--
-- Turns the Phase 1 customers stub (name/phone/email only, created
-- inline by POST /sales/orders/{id}/customer for a walk-in sale — see
-- internal/sales/customer.go, unchanged by this migration) into a real
-- CRM profile: B2C/B2B typing and the FRD's optional fields. Deliberately
-- NOT adding a stored "segment" column — VIP/Regular/New/Dormant is
-- computed on read from actual sales_orders history (internal/customers'
-- shared SQL, see its header comment), the same "compute it, don't cache
-- and risk staleness" choice already made for Pricing's margin_pct/
-- markup_pct.
--
-- Deferred, matching the roadmap's own scoping for this phase: credit
-- facility/B2B payment terms (bundled with Phase 4's accounting depth,
-- per the roadmap's explicit "Deferred" line for Phase 3), loyalty
-- points earn/redeem and customer-level discounts (Promotions & Loyalty
-- — this same phase's NEXT sub-area, not this one).
-- =====================================================================

ALTER TABLE customers
    ADD COLUMN customer_type TEXT NOT NULL DEFAULT 'b2c' CHECK (customer_type IN ('b2c', 'b2b')),
    ADD COLUMN address TEXT,
    ADD COLUMN date_of_birth DATE,
    ADD COLUMN anniversary DATE,
    ADD COLUMN company_name TEXT,
    ADD COLUMN gstin TEXT,
    ADD COLUMN status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- Partial indexes (most walk-in stub rows have a NULL phone/email) to
-- keep the common "look this customer up by phone at checkout" path fast
-- without indexing rows that can never match it anyway.
CREATE INDEX idx_customers_phone ON customers(merchant_id, phone) WHERE phone IS NOT NULL;
CREATE INDEX idx_customers_email ON customers(merchant_id, email) WHERE email IS NOT NULL;
