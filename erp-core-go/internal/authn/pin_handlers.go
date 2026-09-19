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

type PinLoginHandler struct {
	DB     *db.DB
	Issuer *TokenIssuer
}

type pinLoginRequest struct {
	POSTerminalID     string `json:"pos_terminal_id"`
	EmployeeCode      string `json:"employee_code"`
	PIN               string `json:"pin"`
	DeviceFingerprint string `json:"device_fingerprint"` // optional; see device-binding note below
}

// ServeHTTP: POST /auth/pin-login — FRD §18's cashier quick-login.
//
// Device binding, as actually implemented (the design doc's contract
// didn't specify how the presented device is identified): if the terminal
// already has a device_fingerprint on file, the request's must match or
// this fails with DEVICE_MISMATCH. If the terminal has never been bound,
// the first successful PIN login binds it — deliberately only on success,
// so a wrong-PIN attempt can never claim a terminal for an attacker's
// device.
func (h *PinLoginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req pinLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.POSTerminalID == "" || req.EmployeeCode == "" || req.PIN == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "pos_terminal_id, employee_code and pin are required")
		return
	}

	ctx := r.Context()

	var tenantID, branchID, status string
	var boundFingerprint *string
	err := h.DB.Pool.QueryRow(ctx, `
		SELECT merchant_id, branch_id, device_fingerprint, status
		FROM pos_terminals WHERE id = $1`, req.POSTerminalID,
	).Scan(&tenantID, &branchID, &boundFingerprint, &status)
	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid terminal, employee code or pin")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not resolve terminal")
		return
	}
	if status != "active" {
		httpx.Error(w, http.StatusForbidden, "TERMINAL_UNAVAILABLE", "this terminal is not active")
		return
	}
	if boundFingerprint != nil && *boundFingerprint != "" && *boundFingerprint != req.DeviceFingerprint {
		httpx.Error(w, http.StatusForbidden, "DEVICE_MISMATCH", "this terminal is bound to a different device")
		return
	}

	type userRow struct {
		id                  string
		pinHash             *string
		status              string
		failedLoginAttempts int
		lockedUntil         *time.Time
	}
	var u userRow
	var roles []string

	err = h.DB.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT id, pin_hash, status, failed_login_attempts, locked_until
			FROM users WHERE employee_code = $1`, req.EmployeeCode)
		if scanErr := row.Scan(&u.id, &u.pinHash, &u.status, &u.failedLoginAttempts, &u.lockedUntil); scanErr != nil {
			return scanErr
		}
		var rolesErr error
		roles, rolesErr = fetchRoles(ctx, tx, u.id)
		return rolesErr
	})
	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid terminal, employee code or pin")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "pin login failed")
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

	pinOK := u.pinHash != nil && bcrypt.CompareHashAndPassword([]byte(*u.pinHash), []byte(req.PIN)) == nil
	if !pinOK {
		lockErr := h.DB.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return recordFailedLoginAttempt(ctx, tx, u.id, u.failedLoginAttempts)
		})
		if lockErr != nil {
			httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "pin login failed")
			return
		}
		httpx.Error(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid terminal, employee code or pin")
		return
	}

	ttl := sessionTTLForRoles(roles)
	var resp loginResponse
	err = h.DB.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := clearFailedLoginState(ctx, tx, u.id, u.failedLoginAttempts != 0 || u.lockedUntil != nil); err != nil {
			return err
		}

		// Bind-on-first-use: only reached after a correct PIN, per the
		// package comment above.
		if boundFingerprint == nil || *boundFingerprint == "" {
			if _, err := tx.Exec(ctx, `UPDATE pos_terminals SET device_fingerprint = $1 WHERE id = $2`,
				req.DeviceFingerprint, req.POSTerminalID); err != nil {
				return err
			}
		}

		fingerprint := req.DeviceFingerprint
		termID := req.POSTerminalID
		refreshToken, err := issueRefreshToken(ctx, tx, tenantID, u.id, &termID, &fingerprint)
		if err != nil {
			return err
		}

		token, expiresAt, err := h.Issuer.IssueAccessToken(tenantID, u.id, branchID, roles, ttl)
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
