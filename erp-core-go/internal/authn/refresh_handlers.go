package authn

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type RefreshHandler struct {
	DB     *db.DB
	Issuer *TokenIssuer
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// ServeHTTP: POST /auth/refresh — exchanges a still-valid refresh token for
// a new access token. Rotates the refresh token on every use (old one is
// revoked, a new one issued) rather than reusing it: if a refresh token is
// ever stolen, rotation means the legitimate client's next refresh attempt
// fails with the token already revoked, which is a detectable signal a
// non-rotating scheme doesn't give you.
func (h *RefreshHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.RefreshToken == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "refresh_token is required")
		return
	}

	ctx := r.Context()
	rec, err := lookupRefreshToken(ctx, h.DB, req.RefreshToken)
	if err == db.ErrNotFound {
		httpx.Error(w, http.StatusUnauthorized, "INVALID_TOKEN", "refresh token is invalid")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not validate refresh token")
		return
	}
	if rec.RevokedAt != nil || time.Now().After(rec.ExpiresAt) {
		httpx.Error(w, http.StatusUnauthorized, "INVALID_TOKEN", "refresh token is invalid or expired")
		return
	}

	var resp loginResponse
	err = h.DB.WithTenant(ctx, rec.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var lockedUntil *time.Time
		var roles []string
		if scanErr := tx.QueryRow(ctx, `SELECT status, locked_until FROM users WHERE id = $1`, rec.UserID).
			Scan(&status, &lockedUntil); scanErr != nil {
			return scanErr
		}
		if status != "active" {
			return errAccountNotUsable
		}
		if lockedUntil != nil && lockedUntil.After(time.Now()) {
			return errAccountNotUsable
		}

		rows, queryErr := tx.Query(ctx, `
			SELECT r.name FROM roles r
			JOIN user_roles ur ON ur.role_id = r.id
			WHERE ur.user_id = $1`, rec.UserID)
		if queryErr != nil {
			return queryErr
		}
		for rows.Next() {
			var name string
			if scanErr := rows.Scan(&name); scanErr != nil {
				rows.Close()
				return scanErr
			}
			roles = append(roles, name)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		if err := revokeRefreshToken(ctx, tx, rec.ID); err != nil {
			return err
		}
		newRefresh, err := issueRefreshToken(ctx, tx, rec.TenantID, rec.UserID, nil, nil)
		if err != nil {
			return err
		}

		token, expiresAt, err := h.Issuer.IssueAccessToken(rec.TenantID, rec.UserID, "", roles, sessionTTLForRoles(roles))
		if err != nil {
			return err
		}
		resp = loginResponse{
			AccessToken:  token,
			RefreshToken: newRefresh,
			ExpiresAt:    expiresAt,
			ExpiresIn:    int(time.Until(expiresAt).Seconds()),
			UserID:       rec.UserID,
			Roles:        roles,
		}
		return nil
	})

	switch {
	case err == errAccountNotUsable:
		httpx.Error(w, http.StatusForbidden, "ACCOUNT_INACTIVE", "this account can no longer refresh sessions")
	case err == pgx.ErrNoRows:
		httpx.Error(w, http.StatusUnauthorized, "INVALID_TOKEN", "refresh token is invalid")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not refresh session")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

type LogoutHandler struct {
	DB *db.DB
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// ServeHTTP: POST /auth/logout — revokes the presented refresh token.
// Registered behind RequireAuth (see router.go) so a valid access token is
// also required, and the revoked token's owning user must match the
// access token's claims — defense in depth against a stray leaked refresh
// token alone being enough to tamper with someone else's session.
func (h *LogoutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims, ok := FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}

	var req logoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.RefreshToken == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "refresh_token is required")
		return
	}

	ctx := r.Context()
	rec, err := lookupRefreshToken(ctx, h.DB, req.RefreshToken)
	if err == db.ErrNotFound {
		// Already gone/never existed — logout is idempotent either way.
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not revoke session")
		return
	}
	if rec.UserID != claims.UserID {
		httpx.Error(w, http.StatusForbidden, "INVALID_TOKEN", "refresh token does not belong to this session")
		return
	}

	err = h.DB.WithTenant(ctx, claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return revokeRefreshToken(ctx, tx, rec.ID)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not revoke session")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
