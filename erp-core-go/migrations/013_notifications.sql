-- =====================================================================
-- Phase 3, sub-area 3: Notifications (expanded)
-- (phased_roadmap.md Phase 3; tech_stack_decision.md §3.1's "Notification
-- dispatch at volume (fan-out to Email/SMS/WhatsApp/Push providers)")
--
-- Scope, and one deliberate substitution from the roadmap's own wording
-- ("SMS/WhatsApp for receipts, low-stock alerts, payment reminders"):
--
--   * Receipts and low-stock alerts are exactly what the roadmap names.
--     Low-stock uses stock_levels.reorder_point, which has existed since
--     001_schema.sql but was never read or settable by any endpoint until
--     this migration's internal/inventory changes.
--   * "Payment reminders," in FRD terms (pos_frd_complete.md §6, under
--     B2B Credit Facility: "Payment reminders (pre-due and overdue)"), is
--     about a B2B CUSTOMER's own outstanding balance to this merchant —
--     but that needs the credit_facility/outstanding-balance data model,
--     which migrations/011_customers.sql's own header comment already
--     deferred to Phase 4 alongside the rest of the accounting depth.
--     Building customer payment reminders now would mean inventing that
--     model early and outside its planned phase. Substituted instead:
--     supplier BILL payment reminders, using Phase 2's
--     purchase_bills.due_date (real, unused data already sitting there) —
--     reminds accounts staff of this merchant's own payables coming due
--     or overdue, not customers of theirs. Customer-facing payment
--     reminders stay deferred to Phase 4 with credit facility itself.
--
-- Real vendor integration for SMS/WhatsApp needs a chosen provider
-- (Twilio, MSG91, Gupshup, ...) and real credentials neither of which
-- exist in this solo-builder sandbox — internal/notifications ships a
-- real SMTP email provider (any standard SMTP server/relay, no vendor
-- lock-in) plus a console/log stand-in for SMS/WhatsApp, the same
-- "real interface, dev-only stand-in for the piece that needs
-- credentials/hardware this environment doesn't have" pattern already
-- used for DEV_AUTH_TOOLS_ENABLED and internal/printing's ESC/POS driver.
-- =====================================================================

CREATE TABLE notifications (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    channel         TEXT NOT NULL CHECK (channel IN ('email', 'sms', 'whatsapp')),
    category        TEXT NOT NULL CHECK (category IN ('receipt', 'low_stock', 'payment_reminder')),
    recipient       TEXT NOT NULL,
    subject         TEXT,               -- email only
    body            TEXT NOT NULL,
    reference_type  TEXT,               -- 'sales_order' | 'stock_levels' | 'purchase_bill'
    reference_id    UUID,
    status          TEXT NOT NULL DEFAULT 'sent' CHECK (status IN ('sent', 'failed')),
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_notifications_merchant_created ON notifications(merchant_id, created_at DESC);

ALTER TABLE notifications ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON notifications USING (merchant_id = current_setting('app.tenant_id', true)::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON notifications TO erp_app;

-- Dedup markers so the two sweepers (internal/inventory, internal/purchase)
-- alert once per "episode" instead of every tick: low_stock_alerted_at is
-- cleared the moment on_hand recovers above reorder_point (so a future dip
-- alerts again); last_reminder_sent_at gates one reminder per bill per
-- calendar day while it stays unpaid/partially_paid.
ALTER TABLE stock_levels ADD COLUMN low_stock_alerted_at TIMESTAMPTZ;
ALTER TABLE purchase_bills ADD COLUMN last_reminder_sent_at TIMESTAMPTZ;

-- New permission: the notification log can contain customer emails/phone
-- numbers and message bodies — real PII, same reasoning that gates
-- pricing.manage/promotions.manage rather than leaving this open to any
-- authenticated user.
INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000008', 'notifications.view', 'View the sent-notification audit log (GET /notifications)')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.merchant_id = '11111111-1111-1111-1111-111111111111'
  AND r.name IN ('Branch Manager', 'Merchant Admin')
  AND p.code = 'notifications.view'
ON CONFLICT DO NOTHING;
