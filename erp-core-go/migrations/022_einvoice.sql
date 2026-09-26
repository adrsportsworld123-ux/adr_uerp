-- =====================================================================
-- Phase 4's last sub-area: E-Invoicing (IRN generation, QR code) and
-- E-Way Bill (phased_roadmap.md; pos_frd_complete.md's Tax & GST
-- section: "E-Invoicing (IRN generation, QR code)", "E-Way Bill
-- (auto-generate for interstate >Rs.50k)").
--
-- Real IRN/e-way-bill generation requires a licensed GSP (GST Suvidha
-- Provider) integration — there is no direct-to-NIC-portal path for a
-- typical taxpayer, and which GSP to integrate (ClearTax, Cygnet,
-- Vayana, MasterGST, ...) is a vendor decision only the merchant/
-- operator can make: different pricing, different onboarding, and a
-- handful of very large taxpayers get direct NIC API access instead of
-- going through a GSP at all. That decision was explicitly deferred
-- (see phased_roadmap.md's Phase 4 "Not yet started" note) — this
-- migration and internal/einvoice build the vendor-agnostic side of the
-- integration now: schema, business rules (B2B-GSTIN precondition for
-- e-invoicing, the 24-hour cancel window, the interstate/threshold
-- check for e-way bills), and a GSPClient interface, behind a
-- StubGSPClient that returns realistic, well-formed fake IRNs/QR
-- payloads/e-way-bill numbers so the rest of the system — the API
-- contracts, the UI, the audit trail — can be built and tested end to
-- end today. Swapping in a real vendor later is a new GSPClient
-- implementation wired in cmd/api/main.go, not a schema or handler
-- change.
-- =====================================================================

CREATE TABLE e_invoices (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    sales_order_id      UUID NOT NULL REFERENCES sales_orders(id),
    gsp_provider        TEXT NOT NULL,              -- which GSPClient implementation produced this row ("stub" today)
    irn                 TEXT,                       -- Invoice Reference Number (64-char hex per NIC spec)
    ack_no              TEXT,
    ack_date            TIMESTAMPTZ,
    signed_invoice      TEXT,                       -- IRP's signed JSON — large, kept as text, never parsed back
    signed_qr_code      TEXT,                       -- base64 QR payload the FRD's "QR code" requirement means
    status              TEXT NOT NULL DEFAULT 'generated' CHECK (status IN ('generated','cancelled','failed')),
    error_message       TEXT,
    cancel_reason       TEXT,
    cancelled_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (sales_order_id)                          -- one e-invoice per order; a cancelled one can't be regenerated
                                                      -- (real NIC rule: a cancelled IRN can never be reused) — the
                                                      -- order would need a credit note + fresh invoice in a real
                                                      -- deployment, out of scope here same as credit/debit notes
                                                      -- already are for internal/gst's GSTR-1 export
);

CREATE TABLE e_way_bills (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id         UUID NOT NULL REFERENCES merchants(id),
    sales_order_id      UUID NOT NULL REFERENCES sales_orders(id),
    gsp_provider        TEXT NOT NULL,
    ewb_no              TEXT,
    ewb_date            TIMESTAMPTZ,
    valid_until         TIMESTAMPTZ,
    vehicle_no          TEXT,
    transporter_id      TEXT,
    distance_km         INT,
    interstate          BOOLEAN NOT NULL DEFAULT false,    -- computed once at generation time from supplier/buyer GSTIN state codes, then persisted — see this file's own comment on GET/replay needing the same value the original generate response gave
    required_by_rule    BOOLEAN NOT NULL DEFAULT false,    -- interstate AND grand_total > the FRD's Rs.50k threshold
    status              TEXT NOT NULL DEFAULT 'generated' CHECK (status IN ('generated','cancelled','failed')),
    error_message       TEXT,
    cancel_reason       TEXT,
    cancelled_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (sales_order_id)
);

ALTER TABLE e_invoices ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON e_invoices USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON e_invoices TO erp_app;

ALTER TABLE e_way_bills ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON e_way_bills USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON e_way_bills TO erp_app;

-- New permission: generating/cancelling a real e-invoice or e-way bill
-- calls (in a real deployment) a paid, rate-limited GSP API and creates
-- a document with real regulatory consequences — gated the same way
-- pricing.manage and inventory.adjust already are, not open to every
-- POS user.
INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-00000000000e', 'einvoice.manage', 'Generate/cancel e-invoices (IRN) and e-way bills (POST/DELETE /sales/orders/{id}/e-invoice, /e-way-bill)')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.merchant_id = '11111111-1111-1111-1111-111111111111'
  AND r.name IN ('Branch Manager', 'Merchant Admin')
  AND p.code = 'einvoice.manage'
ON CONFLICT DO NOTHING;
