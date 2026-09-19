package authn

import (
	"context"
	"encoding/json"
	"errors"
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

// errAccountNotUsable covers both ACCOUNT_INACTIVE and ACCOUNT_LOCKED as a
// single sentinel where the caller (refresh_handlers.go) doesn't need to
// distinguish the two in its response.
var errAccountNotUsable = errors.New("authn: account not usable")

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
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	ExpiresIn    int       `json:"expires_in"` // seconds — matches phase0_1_design.md §3.1's documented field
	UserID       string    `json:"user_id"`
	Roles        []string  `json:"roles"`
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

		var rolesErr error
		roles, rolesErr = fetchRoles(ctx, tx, u.id)
		return rolesErr
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
		// Lockout enforcement: increment failed_login_attempts, and once it
		// crosses maxFailedAttempts, set locked_until and reset the counter
		// so the next window starts fresh. Runs inside the same WithTenant
		// pattern as everything else — closes the gap flagged in the
		// original NOTE here (ACCOUNT_LOCKED was checked above but never
		// triggered by anything).
		lockErr := h.DB.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return recordFailedLoginAttempt(ctx, tx, u.id, u.failedLoginAttempts)
		})
		if lockErr != nil {
			httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "login failed")
			return
		}
		httpx.Error(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid merchant code, email or password")
		return
	}

	ttl := sessionTTLForRoles(roles)
	var resp loginResponse
	err = h.DB.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		// Successful login clears any accumulated failed-attempt count —
		// otherwise a user who fails a few times then succeeds stays one
		// mistake away from lockout indefinitely.
		if err := clearFailedLoginState(ctx, tx, u.id, u.failedLoginAttempts != 0 || u.lockedUntil != nil); err != nil {
			return err
		}

		refreshToken, err := issueRefreshToken(ctx, tx, tenantID, u.id, nil, nil)
		if err != nil {
			return err
		}

		token, expiresAt, err := h.Issuer.IssueAccessToken(tenantID, u.id, "", roles, ttl)
		if err != nil {
			return err
		}
		resp = loginResponse{
			AccessToken:  token,
			RefreshToken: refreshToken,
			ExpiresAt:    expiresAt,
			ExpiresIn:    int(time.Until(expiresAt).Seconds()),
			UserID:       u.id,
			Roles:        roles,
		}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not issue token")
		return
	}

	httpx.JSON(w, http.StatusOK, resp)
}
