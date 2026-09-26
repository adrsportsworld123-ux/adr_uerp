// A real forgot-password flow — Phase 1's own roadmap named this item
// ("Password-reuse history, a real forgot-password flow, superseding the
// dev-only /dev/set-password tool") since the original gap analysis and
// it stayed open through every subsequent phase; closed here in a
// 2026-09-22 "review previous phases for anything missing" audit. See
// migrations/020_password_reset.sql's header comment for the schema.
//
// Same Notifier structural-interface pattern internal/sales/notify_receipt.go
// already established, to avoid internal/authn importing internal/notifications
// directly (main.go wires the concrete *notifications.Handler in either
// place — no import cycle either way, but this keeps authn's own
// dependency graph as narrow as it already is).
package authn

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

// resetTokenTTL is deliberately much shorter than refreshTokenTTL — a
// password reset token is a stronger credential (it can mint a whole new
// session) than a refresh token (it can only extend an existing one), so
// it gets a tighter window.
const resetTokenTTL = 1 * time.Hour

// passwordHistoryDepth is how many of a user's past passwords (including
// their current one) POST /auth/reset-password refuses to let them reuse.
const passwordHistoryDepth = 5

type PasswordResetNotifier interface {
	DispatchEmail(ctx context.Context, tx pgx.Tx, category, recipient, subject, body, referenceType, referenceID string)
}

type PasswordResetHandler struct {
	DB     *db.DB
	Notify PasswordResetNotifier // optional — nil just means no email goes out, same "best-effort" stance notify_receipt.go documents
}

func generateResetToken() (raw string, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("authn: generate reset token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(raw))
	hash = hex.EncodeToString(sum[:])
	return raw, hash, nil
}

func hashResetToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------
// POST /auth/forgot-password — unauthenticated, deliberately vague in
// every response regardless of whether merchant_code/email actually
// resolved to anything, so this endpoint can't be used to enumerate
// which accounts exist (the same reasoning /dev/set-password's own doc
// comment already gives for its own 404 shape, applied here to an
// endpoint that's public in every environment, not just dev ones).
// ---------------------------------------------------------------------

type forgotPasswordRequest struct {
	MerchantCode string `json:"merchant_code"`
	Email        string `json:"email"`
}

func (h *PasswordResetHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.MerchantCode == "" || req.Email == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "merchant_code and email are required")
		return
	}

	ctx := r.Context()
	var tenantID string
	err := h.DB.Pool.QueryRow(ctx, `SELECT id FROM merchants WHERE code = $1 AND status = 'active'`, req.MerchantCode).Scan(&tenantID)
	if err == nil {
		_ = h.DB.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
			var userID, name string
			if err := tx.QueryRow(ctx, `SELECT id, name FROM users WHERE email = $1 AND status = 'active'`, req.Email).
				Scan(&userID, &name); err != nil {
				return nil // no matching user — silently no-op, same vagueness as the response below
			}

			raw, hash, err := generateResetToken()
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO password_reset_tokens (id, merchant_id, user_id, token_hash, expires_at)
				VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, now() + $3::interval)`,
				userID, hash, resetTokenTTL.String()); err != nil {
				return err
			}

			if h.Notify != nil {
				body := fmt.Sprintf(
					"Hi %s,\n\nUse this code to reset your password (expires in 1 hour): %s\n\nIf you didn't request this, you can ignore this email.",
					name, raw,
				)
				h.Notify.DispatchEmail(ctx, tx, "password_reset", req.Email, "Reset your password", body, "user", userID)
			}
			return nil
		})
	}
	// Always 200, always the same message — whether or not anything above
	// actually matched a real account.
	httpx.JSON(w, http.StatusOK, map[string]any{
		"message": "If an account matches that email, a password reset code has been sent to it.",
	})
}

// ---------------------------------------------------------------------
// POST /auth/reset-password — unauthenticated (that's the whole point:
// proving you received the emailed code is the authentication). Looked
// up by exact token hash first, pool-level, the same shape
// lookupRefreshToken already uses, since which tenant to scope into
// isn't known until the token itself resolves one.
// ---------------------------------------------------------------------

type resetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

var errPasswordReused = errors.New("authn: new password matches a recent one")

func (h *PasswordResetHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Token == "" || req.NewPassword == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "token and new_password are required")
		return
	}
	if len(req.NewPassword) > 72 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "new_password must be 72 bytes or fewer (bcrypt's limit)")
		return
	}

	ctx := r.Context()
	hash := hashResetToken(req.Token)

	var tokenID, tenantID, userID string
	var expiresAt time.Time
	var usedAt *time.Time
	err := h.DB.Pool.QueryRow(ctx, `
		SELECT id, merchant_id, user_id, expires_at, used_at
		FROM password_reset_tokens WHERE token_hash = $1`, hash,
	).Scan(&tokenID, &tenantID, &userID, &expiresAt, &usedAt)
	if err == pgx.ErrNoRows || (err == nil && (usedAt != nil || time.Now().After(expiresAt))) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "reset token is invalid, expired, or already used")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not look up reset token")
		return
	}

	err = h.DB.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var currentHash *string
		if err := tx.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&currentHash); err != nil {
			return err
		}

		// Reuse check: the current password (not yet archived into
		// password_history until this same transaction, below) plus the
		// last passwordHistoryDepth-1 archived ones.
		if currentHash != nil && bcrypt.CompareHashAndPassword([]byte(*currentHash), []byte(req.NewPassword)) == nil {
			return errPasswordReused
		}
		rows, err := tx.Query(ctx, `
			SELECT password_hash FROM password_history
			WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`, userID, passwordHistoryDepth-1)
		if err != nil {
			return err
		}
		var pastHashes []string
		for rows.Next() {
			var ph string
			if err := rows.Scan(&ph); err != nil {
				rows.Close()
				return err
			}
			pastHashes = append(pastHashes, ph)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, ph := range pastHashes {
			if bcrypt.CompareHashAndPassword([]byte(ph), []byte(req.NewPassword)) == nil {
				return errPasswordReused
			}
		}

		newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			return err
		}

		if currentHash != nil {
			if _, err := tx.Exec(ctx, `
				INSERT INTO password_history (id, merchant_id, user_id, password_hash)
				VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2)`,
				userID, *currentHash); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE users SET password_hash = $1, password_changed_at = now(), failed_login_attempts = 0, locked_until = NULL
			WHERE id = $2`, string(newHash), userID); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `UPDATE password_reset_tokens SET used_at = now() WHERE id = $1`, tokenID); err != nil {
			return err
		}

		// A password reset is exactly the moment every other session for
		// this account should stop being trusted — revoke every refresh
		// token still active, forcing a real re-login everywhere.
		if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
			return err
		}
		return nil
	})

	switch {
	case errors.Is(err, errPasswordReused):
		httpx.Error(w, http.StatusConflict, "PASSWORD_REUSED", "new password matches one of your recent passwords")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not reset password")
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"status": "ok"})
	}
}
