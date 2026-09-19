package authn

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/db"
)

// refreshTokenTTL is how long a refresh token stays valid before the client
// must log in again — separate from (and much longer than) the tiered
// access-token TTLs in session_tiers.go. Not specified by the FRD; 30 days
// is a conventional default for an offline-capable POS where forcing a
// full re-login too often would be disruptive.
const refreshTokenTTL = 30 * 24 * time.Hour

// generateRefreshToken returns a high-entropy raw token to hand to the
// client, and the sha256 hex hash of it to store server-side. Refresh
// tokens are looked up by exact hash match (like a GitHub PAT), not
// verified with bcrypt — bcrypt is for low-entropy secrets (passwords,
// PINs) where a slow hash defends against guessing; a 256-bit random token
// has no guessing surface to slow down, so a fast hash is the right choice
// and avoids needless bcrypt cost on every refresh call.
func generateRefreshToken() (raw string, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("authn: generate refresh token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(raw))
	hash = hex.EncodeToString(sum[:])
	return raw, hash, nil
}

func hashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// issueRefreshToken inserts a new refresh_tokens row inside the caller's
// existing tenant-scoped transaction and returns the raw token to send to
// the client — the row only ever stores the hash (see generateRefreshToken).
func issueRefreshToken(ctx context.Context, tx pgx.Tx, tenantID, userID string, posTerminalID, deviceFingerprint *string) (string, error) {
	raw, hash, err := generateRefreshToken()
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO refresh_tokens (id, merchant_id, user_id, token_hash, device_fingerprint, pos_terminal_id, expires_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, now() + $6::interval)`,
		tenantID, userID, hash, deviceFingerprint, posTerminalID, refreshTokenTTL.String())
	if err != nil {
		return "", fmt.Errorf("authn: store refresh token: %w", err)
	}
	return raw, nil
}

type refreshTokenRecord struct {
	ID        string
	UserID    string
	TenantID  string
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// lookupRefreshToken is a pool-level (not tenant-scoped) read: refresh_tokens
// carries no RLS policy, by the same reasoning as the dev password
// endpoints' merchant lookup — the token hash itself, not RLS, is the proof
// of identity here, and we don't yet know which tenant to scope into until
// this lookup tells us (see migrations/003_hardening.sql).
func lookupRefreshToken(ctx context.Context, database *db.DB, rawToken string) (*refreshTokenRecord, error) {
	hash := hashRefreshToken(rawToken)
	var rec refreshTokenRecord
	err := database.Pool.QueryRow(ctx, `
		SELECT id, user_id, merchant_id, expires_at, revoked_at
		FROM refresh_tokens WHERE token_hash = $1`, hash,
	).Scan(&rec.ID, &rec.UserID, &rec.TenantID, &rec.ExpiresAt, &rec.RevokedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return &rec, nil
}

func revokeRefreshToken(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	return err
}
