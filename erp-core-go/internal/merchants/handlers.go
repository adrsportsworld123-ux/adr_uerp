package merchants

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB *db.DB
}

var errInvalid = errors.New("invalid")

// GetProfile: GET /merchant/profile — any signed-in user of the merchant
// (the letterhead and screens show it).
func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var p Profile
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		p, err = Load(ctx, tx, claims.TenantID)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the company profile")
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

// UpdateProfile: PUT /merchant/profile — merchant.profile. Partial: a
// field left out keeps its value. Audited in the merchant's own log.
func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var u Update
	if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	var p Profile
	var bad string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := Load(ctx, tx, claims.TenantID)
		if err != nil {
			return err
		}
		p = before
		if bad = u.Apply(&p); bad != "" {
			return errInvalid
		}
		if err := Save(ctx, tx, p); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "merchant", EntityID: claims.TenantID, Action: "update", Reason: "company profile",
			PerformedBy: claims.UserID, Before: before, After: p})
	})
	switch {
	case errors.Is(err, errInvalid):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", bad)
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the company profile")
	default:
		httpx.JSON(w, http.StatusOK, p)
	}
}

// ---------------------------------------------------------------------
// The SaaS console (platform staff; router.go's platform group). Every
// change is written to platform_audit_log with the staff member and a
// reason, and the profile edit to the merchant's own audit log too.
// ---------------------------------------------------------------------

type PlatformHandler struct {
	DB *db.DB
}

func (h *PlatformHandler) logPlatform(ctx context.Context, staffID, action, merchantID string, before, after any, reason string) error {
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	_, err := h.DB.Pool.Exec(ctx, `INSERT INTO platform_audit_log (staff_id, action, target_type, target_id, before_value, after_value, reason)
		VALUES ($1, $2, 'merchant', $3, $4, $5, NULLIF($6, ''))`, staffID, action, merchantID, b, a, reason)
	return err
}

type adminUser struct {
	UserID      string  `json:"user_id"`
	Name        string  `json:"name"`
	Email       string  `json:"email"`
	Status      string  `json:"status"`
	LockedUntil *string `json:"locked_until"`
}

type overview struct {
	Profile   Profile     `json:"profile"`
	Branches  int         `json:"branches"`
	Users     int         `json:"users"`
	Terminals int         `json:"terminals"`
	Admins    []adminUser `json:"admins"`
}

func (h *PlatformHandler) overview(ctx context.Context, merchantID string) (overview, error) {
	var o overview
	o.Admins = []adminUser{}
	err := h.DB.WithTenant(ctx, merchantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if o.Profile, err = Load(ctx, tx, merchantID); err != nil {
			return err
		}
		if err := db.Unrestricted(ctx, tx, func() error {
			return tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM branches WHERE status = 'active'),
			                                (SELECT count(*) FROM users WHERE status = 'active'),
			                                (SELECT count(*) FROM pos_terminals WHERE status = 'active')`).
				Scan(&o.Branches, &o.Users, &o.Terminals)
		}); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT u.id::text, u.name, COALESCE(u.email, ''), u.status, u.locked_until::text
			FROM users u JOIN user_roles ur ON ur.user_id = u.id JOIN roles r ON r.id = ur.role_id
			WHERE r.name = 'Merchant Admin' ORDER BY u.created_at`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a adminUser
			if err := rows.Scan(&a.UserID, &a.Name, &a.Email, &a.Status, &a.LockedUntil); err != nil {
				return err
			}
			o.Admins = append(o.Admins, a)
		}
		return rows.Err()
	})
	return o, err
}

// GetMerchant: GET /platform/merchants/{id}/profile — the profile, active
// branch/user/terminal counts and the merchant's admins.
func (h *PlatformHandler) GetMerchant(w http.ResponseWriter, r *http.Request) {
	o, err := h.overview(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "merchant not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the merchant")
		return
	}
	httpx.JSON(w, http.StatusOK, o)
}

// UpdateMerchant: PATCH /platform/merchants/{id}/profile {...Update,
// reason?} — the same edit and validation as the merchant's own.
func (h *PlatformHandler) UpdateMerchant(w http.ResponseWriter, r *http.Request) {
	staff, _ := authn.FromContext(r.Context())
	id := chi.URLParam(r, "id")
	var req struct {
		Update
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	var before, p Profile
	var bad string
	err := h.DB.WithTenant(r.Context(), id, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if before, err = Load(ctx, tx, id); err != nil {
			return err
		}
		p = before
		if bad = req.Update.Apply(&p); bad != "" {
			return errInvalid
		}
		if err := Save(ctx, tx, p); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "merchant", EntityID: id, Action: "update",
			Before: before, After: p, Reason: strings.TrimSpace("platform console: " + req.Reason)})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "merchant not found")
		return
	case errors.Is(err, errInvalid):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", bad)
		return
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the merchant")
		return
	}
	_ = h.logPlatform(r.Context(), staff.UserID, "merchant.update_profile", id, before, p, strings.TrimSpace(req.Reason))
	o, err := h.overview(r.Context(), id)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "merchant saved but could not be reloaded")
		return
	}
	httpx.JSON(w, http.StatusOK, o)
}

// SetStatus: POST /platform/merchants/{id}/status {status, reason} —
// active, suspended or cancelled. A merchant that isn't active can't sign
// in (password, PIN or refresh); its data is untouched, and reactivating
// restores access. Reason required.
func (h *PlatformHandler) SetStatus(w http.ResponseWriter, r *http.Request) {
	staff, _ := authn.FromContext(r.Context())
	id := chi.URLParam(r, "id")
	var req struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Status != "active" && req.Status != "suspended" && req.Status != "cancelled" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be active, suspended or cancelled")
		return
	}
	if req.Reason == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a reason is required")
		return
	}
	var before string
	if err := h.DB.Pool.QueryRow(r.Context(), `SELECT status FROM merchants WHERE id::text = $1`, id).Scan(&before); errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "merchant not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the merchant")
		return
	}
	if _, err := h.DB.Pool.Exec(r.Context(), `
		UPDATE merchants SET status = $2, status_reason = $3, status_changed_at = now(), updated_at = now() WHERE id::text = $1`,
		id, req.Status, req.Reason); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not change the status")
		return
	}
	_ = h.logPlatform(r.Context(), staff.UserID, "merchant.status", id, map[string]string{"status": before}, map[string]string{"status": req.Status}, req.Reason)
	o, err := h.overview(r.Context(), id)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "status changed but the merchant could not be reloaded")
		return
	}
	httpx.JSON(w, http.StatusOK, o)
}

// ResetAdminPassword: POST /platform/merchants/{id}/admins/{user_id}/password
// {new_password, reason} — for a merchant locked out of its own admin
// account: sets the password, clears a lockout, and ends the user's other
// sessions. Only a Merchant Admin of that merchant; reason required.
func (h *PlatformHandler) ResetAdminPassword(w http.ResponseWriter, r *http.Request) {
	staff, _ := authn.FromContext(r.Context())
	id, userID := chi.URLParam(r, "id"), chi.URLParam(r, "user_id")
	var req struct {
		NewPassword string `json:"new_password"`
		Reason      string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if len(req.NewPassword) < 8 || len(req.NewPassword) > 72 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "new_password must be 8 to 72 characters")
		return
	}
	if req.Reason == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a reason is required")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not hash the password")
		return
	}
	var name string
	err = h.DB.WithTenant(r.Context(), id, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			UPDATE users u SET password_hash = $2, password_changed_at = now(), failed_login_attempts = 0, locked_until = NULL,
			       status = CASE WHEN u.status = 'locked' THEN 'active' ELSE u.status END
			WHERE u.id::text = $1 AND EXISTS (SELECT 1 FROM user_roles ur JOIN roles r ON r.id = ur.role_id
			                                   WHERE ur.user_id = u.id AND r.name = 'Merchant Admin')
			RETURNING u.name`, userID, string(hash)).Scan(&name); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE user_id::text = $1 AND revoked_at IS NULL`, userID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "user", EntityID: userID, Action: "update", Reason: "platform console password reset: " + req.Reason})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no Merchant Admin with this id at this merchant")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not reset the password")
		return
	}
	_ = h.logPlatform(r.Context(), staff.UserID, "merchant.admin_password_reset", id, nil, map[string]string{"user_id": userID, "name": name}, req.Reason)
	w.WriteHeader(http.StatusNoContent)
}
