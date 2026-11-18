-- =====================================================================
-- Reading a supplier's invoice PDF or photo into the import (§3.90).
--
-- The Import Invoice screen can now read the supplier's own document
-- instead of a hand-filled CSV: a typed PDF is read on the server from
-- its text and column positions (the browser extracts them), a photo or
-- scanned PDF by the AI reader when one is configured. Either way the
-- lines land in an editable table and then in the existing CSV preview
-- and commit — nothing is booked without the user checking it.
--
-- How a supplier's invoice is read is saved per supplier, the "template":
--   column_map — which printed column holds what (item, quantity, MRP,
--                rate, tax, total, HSN, and the "variant" column some
--                billing software calls BATCH NO. but fills with sizes);
--   options    — the conversion rules: what the variant column means
--                (size appended to the name / a real batch / ignored),
--                how sizes are written, the cost basis (worked back from
--                the line total, or the printed rate), the brand rule,
--                the sale GST and category for new items, words to drop.
-- No row = the built-in defaults, so nothing changes until one is saved.
-- =====================================================================

CREATE TABLE IF NOT EXISTS supplier_invoice_templates (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id   UUID NOT NULL REFERENCES merchants(id),
    supplier_id   UUID NOT NULL REFERENCES suppliers(id),
    column_map    JSONB NOT NULL DEFAULT '{}'::jsonb,
    options       JSONB NOT NULL DEFAULT '{}'::jsonb,
    uses          INTEGER NOT NULL DEFAULT 0,
    last_used_at  TIMESTAMPTZ,
    updated_by    UUID REFERENCES users(id),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, supplier_id),
    CHECK (jsonb_typeof(column_map) = 'object' AND jsonb_typeof(options) = 'object')
);
ALTER TABLE supplier_invoice_templates ENABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON supplier_invoice_templates;
CREATE POLICY tenant_isolation ON supplier_invoice_templates USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON supplier_invoice_templates TO erp_app;
