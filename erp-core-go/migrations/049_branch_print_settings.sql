-- =====================================================================
-- Branded, downloadable documents (internal/documents): sale receipts,
-- GRNs, purchase bills and purchase returns as PDF, each carrying its
-- branch's letterhead — logo, display name, address, contact lines and a
-- custom header and footers (one for customer-facing sale receipts, one
-- for supplier-facing purchase documents). One row per branch; a branch
-- without one prints with defaults (merchant trade/legal name, branch
-- name, GSTIN, "Thank you, visit again!").
--
-- The logo lives in the row (BYTEA, capped at 512 KB, normalised to
-- PNG/JPEG on upload) rather than object storage: it is one small image
-- per branch, read on every render, and keeping it in Postgres keeps it
-- under the same tenant + branch RLS as everything else.
--
-- Branch-scoped like the transactional tables (migrations/044): a Branch
-- Manager configures their own branch only. Rendering a document needs
-- the row too, and a scoped user can only open their own branch's
-- documents anyway.
-- =====================================================================

CREATE TABLE branch_print_settings (
    branch_id       UUID PRIMARY KEY REFERENCES branches(id) ON DELETE CASCADE,
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    display_name    TEXT,                -- overrides the merchant's trade/legal name on documents
    address         TEXT,                -- free text, one printed line per line
    phone           TEXT,
    email           TEXT,
    header_text     TEXT,                -- extra lines under the letterhead (tagline, licence no.)
    receipt_footer  TEXT,                -- sale receipts: return policy, thank-you lines (NULL = "Thank you, visit again!")
    purchase_footer TEXT,                -- GRNs, purchase bills and returns: terms for the supplier
    show_logo       BOOLEAN NOT NULL DEFAULT true,
    show_gstin      BOOLEAN NOT NULL DEFAULT true,
    receipt_format  TEXT NOT NULL DEFAULT 'a4' CHECK (receipt_format IN ('a4', 'thermal')),
    logo            BYTEA CHECK (octet_length(logo) <= 524288),
    logo_mime       TEXT CHECK (logo_mime IN ('image/png', 'image/jpeg')),
    logo_updated_at TIMESTAMPTZ,
    updated_by      UUID REFERENCES users(id),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((logo IS NULL) = (logo_mime IS NULL))
);
ALTER TABLE branch_print_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON branch_print_settings USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
CREATE POLICY branch_scope ON branch_print_settings AS RESTRICTIVE
    USING (app_branch_visible(branch_id)) WITH CHECK (app_branch_visible(branch_id));
GRANT SELECT, INSERT, UPDATE, DELETE ON branch_print_settings TO erp_app;

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000049', 'branch.print_settings', 'Configure a branch''s document letterhead: logo, header and footer')
ON CONFLICT (code) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name IN ('Merchant Admin', 'Branch Manager') AND p.code = 'branch.print_settings'
ON CONFLICT DO NOTHING;
INSERT INTO role_templates (role_name, permission_code) VALUES ('Branch Manager', 'branch.print_settings') ON CONFLICT DO NOTHING;
