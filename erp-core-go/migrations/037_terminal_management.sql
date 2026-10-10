-- =====================================================================
-- Phase 12: POS terminal management and pairing (phased_roadmap.md Phase 12)
--
-- pos_terminals has existed since migrations/001_schema.sql, but nothing
-- could create, rename, retire or pair one: the Flutter POS hardcodes the
-- seed terminal's id (session.dart's TODO(phase 2)), and every device
-- that ever PIN-logs-in to it binds on first use. This adds:
--
--   * pairing: an admin generates a short-lived, single-use code for a
--     terminal; a device presents merchant code + pairing code once and
--     gets back its terminal/branch identity, and its fingerprint becomes
--     the terminal's bound device (replacing any previous one). Only a
--     SHA-256 of the code is stored, never the code itself.
--   * terminal.manage, the permission for all of the above.
--
-- pos_terminals deliberately has no RLS (migrations/003_hardening.sql:
-- PIN login must resolve a terminal before any tenant context exists), so
-- every Phase 12 query on it filters merchant_id explicitly in Go.
-- =====================================================================

ALTER TABLE pos_terminals
    ADD COLUMN pairing_code_hash  TEXT,
    ADD COLUMN pairing_expires_at TIMESTAMPTZ,
    ADD COLUMN paired_at          TIMESTAMPTZ,
    ADD COLUMN updated_at         TIMESTAMPTZ NOT NULL DEFAULT now();

-- A pairing lookup is (merchant, code hash) while the code is live.
CREATE INDEX idx_pos_terminals_pairing ON pos_terminals (merchant_id, pairing_code_hash)
    WHERE pairing_code_hash IS NOT NULL;

INSERT INTO permissions (id, code, description) VALUES
    ('f0000000-0000-0000-0000-000000000037', 'terminal.manage',
     'Create, rename, activate/deactivate POS terminals, issue pairing codes and reset device bindings')
ON CONFLICT (code) DO NOTHING;

-- Granted by role NAME across every merchant (earlier migrations granted
-- only the seed merchant's roles by id, which leaves any other merchant's
-- admins without the new permission).
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM roles r
CROSS JOIN permissions p
WHERE r.name IN ('Merchant Admin', 'Branch Manager')
  AND p.code = 'terminal.manage'
ON CONFLICT DO NOTHING;
