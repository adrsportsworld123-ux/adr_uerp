-- =====================================================================
-- Merchant profile, a user's own language, promotions on MRP.
--
-- merchants: the company profile a Merchant Admin edits (Settings →
-- Company Profile) and the platform console edits on the merchant's
-- behalf: PAN, contact e-mail/phone, website and the registered address.
-- legal_name / trade_name / gstin already exist. The address is the
-- letterhead's fallback when a branch's print settings give none.
-- status_reason / status_changed_at record why the console suspended or
-- cancelled a merchant (login already refuses a merchant that isn't
-- active).
--
-- users.language / text_direction: one user's own choice (NULL = the
-- merchant's localization, migrations/064). The business clock and the
-- denominations stay the merchant's.
--
-- promotions.apply_on: what a promotion's percent or amount is taken
-- from. 'selling_price' (default: what every promotion did until now) or
-- 'mrp': the promotional price is that much off the MRP, and it applies
-- only where it comes out below the selling price — the discount is the
-- difference. MRP ₹1,000, selling price ₹900, 20% off MRP: promo price
-- ₹800, the customer saves ₹100.
-- =====================================================================

ALTER TABLE merchants ADD COLUMN pan TEXT;
ALTER TABLE merchants ADD COLUMN email TEXT;
ALTER TABLE merchants ADD COLUMN phone TEXT;
ALTER TABLE merchants ADD COLUMN website TEXT;
-- {"line1","line2","city","state","state_code","pincode","country"}
ALTER TABLE merchants ADD COLUMN address JSONB;
ALTER TABLE merchants ADD COLUMN status_reason TEXT;
ALTER TABLE merchants ADD COLUMN status_changed_at TIMESTAMPTZ;

ALTER TABLE users ADD COLUMN language TEXT CHECK (language IN ('en', 'ta', 'hi', 'ar', 'ml', 'te', 'kn', 'fr', 'es'));
ALTER TABLE users ADD COLUMN text_direction TEXT CHECK (text_direction IN ('auto', 'ltr', 'rtl'));

ALTER TABLE promotions ADD COLUMN apply_on TEXT NOT NULL DEFAULT 'selling_price'
    CHECK (apply_on IN ('selling_price', 'mrp'));

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000065', 'merchant.profile',
     'Edit the company profile: legal and trade name, GSTIN, PAN, contact and registered address')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p
WHERE r.name = 'Merchant Admin' AND p.code = 'merchant.profile'
ON CONFLICT DO NOTHING;
