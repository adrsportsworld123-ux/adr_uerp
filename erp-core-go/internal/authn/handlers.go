package authn

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

const (
	maxFailedAttempts = 5
	lockoutDuration   = 15 * time.Minute
)

type LoginHandler struct {
	DB     *db.DB
	Issuer *TokenIssuer
}

type loginRequest struct {
	MerchantCode string `json:"merchant_code"`
	Email        string `json:"email"`
	Password     string `json:"password"`
}

type loginResponse struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	UserID      string    `json:"user_id"`
	Roles       []string  `json:"roles"`
}

// ServeHTTP implements the two-step tenant resolution the schema requires:
//  1. Resolve the merchant by its public `code` — merchants is the tenant
//     root and carries no RLS policy of its own, so this lookup is safe
//     outside any tenant-scoped transaction (see phase0_1_database_schema.sql).
//  2. Everything after that runs inside db.WithTenant, so the user lookup
//     itself is RLS-enforced rather than trusting a WHERE merchant_id = ...
//     clause that a future refactor could accidentally drop.
func (h *LoginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.MerchantCode == "" || req.Email == "" || req.Password == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "merchant_code, email and password are required")
		return
	}

	ctx := r.Context()

	var tenantID string
	err := h.DB.Pool.QueryRow(ctx, `SELECT id FROM merchants WHERE code = $1 AND status = 'active'`, req.MerchantCode).Scan(&tenantID)
	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid merchant code, email or password")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not resolve merchant")
		return
	}

	type userRow struct {
		id                  string
		name                string
		passwordHash        string
		status              string
		failedLoginAttempts int
		lockedUntil         *time.Time
	}
	var u userRow
	var roles []string

	err = h.DB.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, name, password_hash, status, failed_login_attempts, locked_until
			FROM users WHERE email = $1`, req.Email)
		if scanErr := row.Scan(&u.id, &u.name, &u.passwordHash, &u.status, &u.failedLoginAttempts, &u.lockedUntil); scanErr != nil {
			return scanErr
		}

		rows, queryErr := tx.Query(ctx, `
			SELECT r.name FROM roles r
			JOIN user_roles ur ON ur.role_id = r.id
			WHERE ur.user_id = $1`, u.id)
		if queryErr != nil {
			return queryErr
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if scanErr := rows.Scan(&name); scanErr != nil {
				return scanErr
			}
			roles = append(roles, name)
		}
		return rows.Err()
	})

	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid merchant code, email or password")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "login failed")
		return
	}

	if u.status != "active" {
		httpx.Error(w, http.StatusForbidden, "ACCOUNT_INACTIVE", "this account is not active")
		return
	}
	if u.lockedUntil != nil && u.lockedUntil.After(time.Now()) {
		httpx.Error(w, http.StatusForbidden, "ACCOUNT_LOCKED", "too many failed attempts — try again later")
		return
	}

	if bcrypt.CompareHashAndPassword([]byte(u.passwordHash), []byte(req.Password)) != nil {
		// NOTE: incrementing failed_login_attempts / setting locked_until on
		// mismatch is a straightforward follow-up UPDATE inside the same
		// WithTenant pattern above — omitted here to keep this first vertical
		// slice small; do not ship Phase 1 without it (FRD §18 requires the
		// 5-attempt/15-minute lockout enforced above to actually trigger).
		httpx.Error(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid merchant code, email or password")
		return
	}

	token, expiresAt, err := h.Issuer.IssueAccessToken(tenantID, u.id, "", roles)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not issue token")
		return
	}

	httpx.JSON(w, http.StatusOK, loginResponse{
		AccessToken: token,
		ExpiresAt:   expiresAt,
		UserID:      u.id,
		Roles:       roles,
	})
}
