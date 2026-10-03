-- Tax-inclusive vs tax-exclusive pricing.
--
-- Until now every selling_price was implicitly tax-EXCLUSIVE: AddLine
-- added GST on top. Indian retail commonly prices tax-INCLUSIVE instead
-- (MRP-printed FMCG goods, most shelf-priced retail), so a ₹1000 shelf
-- price must bill ₹1000.00, with tax extracted from it rather than added.
--
-- Configured per product, with a merchant-wide default that pre-fills new
-- products (the Tally/Zoho/Marg convention — a mixed catalog needs both
-- modes at once). Existing products and the default both stay `false`, so
-- nothing already priced changes meaning.
--
-- The math lives in exactly one place: internal/taxcalc.ComputeLine.

-- 1. The per-product flag.
ALTER TABLE products ADD COLUMN price_includes_tax BOOLEAN NOT NULL DEFAULT false;

-- 2. The merchant-wide default for newly created products.
CREATE TABLE pricing_settings (
    merchant_id                 UUID PRIMARY KEY REFERENCES merchants(id),
    prices_include_tax_default  BOOLEAN NOT NULL DEFAULT false,
    updated_by                  UUID REFERENCES users(id),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE pricing_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pricing_settings
    USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

GRANT SELECT, INSERT, UPDATE ON pricing_settings TO erp_app;

-- 3. Sale lines snapshot the mode at time of sale (same reasoning as
-- unit_price's own "price snapshot" — flipping a product's flag later must
-- not reinterpret historical sales).
--
-- For an inclusive line, unit_price is the tax-INCLUSIVE shelf price and
-- line_total = unit_price*quantity - discount_amount (tax is inside it).
-- For an exclusive line, nothing changes.
ALTER TABLE sales_order_lines ADD COLUMN price_includes_tax BOOLEAN NOT NULL DEFAULT false;

-- taxable_value is GENERATED, not written by application code, so no
-- writer (the online cart, offline sync push, discount layers) can ever
-- forget it or compute it differently. GST returns, e-invoicing, payroll
-- commission and order totals read this instead of re-deriving
-- unit_price*quantity - discount_amount, which is wrong for an inclusive
-- line (that expression includes tax there).
ALTER TABLE sales_order_lines ADD COLUMN taxable_value NUMERIC(14,2) GENERATED ALWAYS AS (
    CASE WHEN price_includes_tax THEN line_total - tax_amount
         ELSE unit_price * quantity - discount_amount
    END
) STORED;

-- 4. Quotations follow the same snapshot rule (a converted quote's cart
-- re-resolves through sales.AddLineToCart, which reads the product flag
-- itself — this column is for the quote's own display/totals).
ALTER TABLE quotation_lines ADD COLUMN price_includes_tax BOOLEAN NOT NULL DEFAULT false;
