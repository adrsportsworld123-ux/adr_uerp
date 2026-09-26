-- =====================================================================
-- Phase 1's last named-but-never-built item: "Password-reuse history, a
-- real forgot-password flow (superseding the dev-only /dev/set-password
-- tool)" — on the roadmap's own "Still open" list since the original
-- Phase 1 gap analysis, found still open during a 2026-09-22 "review
-- previous phases for anything missing" audit and closed here.
--
-- Two tables:
--   password_reset_tokens — mirrors refresh_tokens' own shape exactly
--     (a high-entropy random token, only its sha256 hash ever stored,
--     looked up by exact hash match — see internal/authn/refresh_tokens.go's
--     own comment on why a fast hash, not bcrypt, is correct for a
--     high-entropy token). Single-use (used_at) and short-lived
--     (expires_at) — a password reset token is a stronger credential than
--     a session refresh token, so it gets a much shorter TTL (1 hour, set
--     in Go, not here) than the 30-day refresh token default.
--   password_history — every password a user has ever set, so a reset
--     can refuse to let them set it right back to one of their last N.
--     Stores the same bcrypt hash the live password_hash column would
--     have held, never the plaintext — reuse-checking bcrypt-compares
--     the new plaintext against each stored hash, the same operation
--     login already does against the current one.
-- =====================================================================

CREATE TABLE password_reset_tokens (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    user_id         UUID NOT NULL REFERENCES users(id),
    token_hash      TEXT NOT NULL UNIQUE,
    expires_at      TIMESTAMPTZ NOT NULL,
    used_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_password_reset_tokens_user ON password_reset_tokens(user_id);

-- No RLS policy — same reasoning as refresh_tokens (migrations/003_hardening.sql's
-- comment): the token hash itself is the proof of identity here, looked
-- up before the caller's tenant is even known, the same pool-level read
-- lookupRefreshToken already uses.
GRANT SELECT, INSERT, UPDATE ON password_reset_tokens TO erp_app;

CREATE TABLE password_history (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id     UUID NOT NULL REFERENCES merchants(id),
    user_id         UUID NOT NULL REFERENCES users(id),
    password_hash   TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_password_history_user ON password_history(user_id, created_at DESC);

ALTER TABLE password_history ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON password_history USING (merchant_id = current_setting('app.tenant_id', true)::uuid);

GRANT SELECT, INSERT ON password_history TO erp_app;

-- internal/notifications.Handler.DispatchEmail's category is a real CHECK
-- constraint (migrations/013_notifications.sql), the same recurring
-- widen-it-again pattern journal_entries.source_type has hit repeatedly
-- in internal/accounting — one more category for the reset email itself.
ALTER TABLE notifications DROP CONSTRAINT notifications_category_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_category_check
    CHECK (category IN ('receipt', 'low_stock', 'payment_reminder', 'password_reset'));
