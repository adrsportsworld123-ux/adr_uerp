// Package users is User Management (migrations/062, users.manage): one
// place for a merchant admin to see every login and manage what the other
// screens don't — passwords and PINs, unlocking a locked account,
// deactivating a leaver, and the home branch. Roles and extra branches
// stay on their own endpoints (/rbac/users/{id}/roles, /rbac/users/{id}/
// branches), which this screen also drives. Users themselves change their
// own password and PIN with /me/password and /me/pin.
//
// Logins are still created with an employee (POST /hr/employees); nothing
// here creates or deletes a user — a leaver is deactivated, so their sales
// and audit trail keep a name.
package users

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
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

type branchRef struct {
	BranchID string `json:"branch_id"`
	Name     string `json:"name"`
}

type roleRef struct {
	RoleID string `json:"role_id"`
	Name   string `json:"name"`
}

type user struct {
	UserID            string      `json:"user_id"`
	Name              string      `json:"name"`
	Email             *string     `json:"email"`
	Phone             *string     `json:"phone"`
	EmployeeCode      *string     `json:"employee_code"`
	Status            string      `json:"status"` // active | inactive | locked
	Locked            bool        `json:"locked"` // locked now (status or a running lockout)
	FailedAttempts    int         `json:"failed_login_attempts"`
	HasPassword       bool        `json:"has_password"`
	HasPIN            bool        `json:"has_pin"`
	PasswordChangedAt *string     `json:"password_changed_at"`
	CreatedAt         string      `json:"created_at"`
	HomeBranch        *branchRef  `json:"home_branch"` // null = no home branch (all branches)
	ExtraBranches     []branchRef `json:"extra_branches"`
	Roles             []roleRef   `json:"roles"`
}

const userSelect = `
	SELECT u.id::text, u.name, u.email, u.phone, u.employee_code, u.status,
	       (u.status = 'locked' OR (u.locked_until IS NOT NULL AND u.locked_until > now())),
	       u.failed_login_attempts, u.password_hash IS NOT NULL, u.pin_hash IS NOT NULL,
	       u.password_changed_at::text, u.created_at::text, u.branch_id::text, hb.name,
	       COALESCE((SELECT json_agg(json_build_object('branch_id', ub.branch_id, 'name', b.name) ORDER BY b.name)
	                 FROM user_branches ub JOIN branches b ON b.id = ub.branch_id WHERE ub.user_id = u.id), '[]'),
	       COALESCE((SELECT json_agg(json_build_object('role_id', r.id, 'name', r.name) ORDER BY r.name)
	                 FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE ur.user_id = u.id), '[]')
	FROM users u LEFT JOIN branches hb ON hb.id = u.branch_id`

func scanUser(row pgx.Row) (user, error) {
	var u user
	var homeID, homeName *string
	var extra, roles []byte
	if err := row.Scan(&u.UserID, &u.Name, &u.Email, &u.Phone, &u.EmployeeCode, &u.Status, &u.Locked, &u.FailedAttempts,
		&u.HasPassword, &u.HasPIN, &u.PasswordChangedAt, &u.CreatedAt, &homeID, &homeName, &extra, &roles); err != nil {
		return u, err
	}
	if homeID != nil {
		u.HomeBranch = &branchRef{BranchID: *homeID, Name: deref(homeName)}
	}
	u.ExtraBranches, u.Roles = []branchRef{}, []roleRef{}
	if err := json.Unmarshal(extra, &u.ExtraBranches); err != nil {
		return u, err
	}
	return u, json.Unmarshal(roles, &u.Roles)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func loadUser(ctx context.Context, tx pgx.Tx, id string) (user, error) {
	return scanUser(tx.QueryRow(ctx, userSelect+` WHERE u.id::text = $1`, id))
}

// ListUsers: GET /users?q=&status= — users.manage. q matches name, email,
// phone or employee code; status active | inactive | locked.
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	status := r.URL.Query().Get("status")
	list := []user{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, userSelect+`
			WHERE ($1 = '' OR u.name ILIKE '%' || $1 || '%' OR u.email ILIKE '%' || $1 || '%'
			       OR u.phone ILIKE '%' || $1 || '%' OR u.employee_code ILIKE '%' || $1 || '%')
			  AND ($2 = '' OR ($2 = 'locked' AND (u.status = 'locked' OR u.locked_until > now()))
			       OR ($2 <> 'locked' AND u.status = $2))
			ORDER BY u.status <> 'active', u.name`, q, status)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			u, err := scanUser(rows)
			if err != nil {
				return err
			}
			list = append(list, u)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list users")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"users": list})
}

// GetUser: GET /users/{id} — users.manage.
func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var u user
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		u, err = loadUser(ctx, tx, chi.URLParam(r, "id"))
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "user not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load user")
		return
	}
	httpx.JSON(w, http.StatusOK, u)
}

var (
	errSelf          = errors.New("you can't deactivate your own account")
	errLastAdmin     = errors.New("this is the last active user who can manage users; give another user that access first")
	errNoBranch      = errors.New("branch not found")
	errNoCode        = errors.New("the user needs an employee code before a PIN can be set — PIN sign-in is code + PIN")
	errWeakPassword  = errors.New("the password must be 8 to 72 characters")
	errBadPIN        = errors.New("the PIN must be 4 to 6 digits, not all the same digit")
	errWrongPassword = errors.New("your current password is not correct")
)

func writeErr(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "user not found")
	case errors.Is(err, errSelf):
		httpx.Error(w, http.StatusConflict, "CANNOT_DEACTIVATE_SELF", err.Error())
	case errors.Is(err, errLastAdmin):
		httpx.Error(w, http.StatusConflict, "LAST_USER_ADMIN", err.Error())
	case errors.Is(err, errNoBranch):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
	case errors.Is(err, errNoCode):
		httpx.Error(w, http.StatusConflict, "EMPLOYEE_CODE_REQUIRED", err.Error())
	case errors.Is(err, errWeakPassword), errors.Is(err, errBadPIN):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
	case errors.Is(err, errWrongPassword):
		httpx.Error(w, http.StatusForbidden, "INVALID_CREDENTIALS", err.Error())
	case errors.Is(err, authn.ErrPasswordReused):
		httpx.Error(w, http.StatusConflict, "PASSWORD_REUSED", "the new password matches one of the recent passwords")
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not "+what)
	}
}

// canManageUsers: the user holds users.manage through a role.
const canManageUsers = `EXISTS (SELECT 1 FROM user_roles ur JOIN role_permissions rp ON rp.role_id = ur.role_id
	JOIN permissions p ON p.id = rp.permission_id WHERE ur.user_id = u.id AND p.code = 'users.manage')`

// UpdateUser: PATCH /users/{id} — users.manage.
//
//	{"status": "active" | "inactive"}   deactivate a leaver (signs them out
//	                                    everywhere) or bring them back
//	{"unlock": true}                    clear a lockout after failed sign-ins
//	{"home_branch_id": "<id>" | null}   the branch the user belongs to;
//	                                    null = none (sees every branch their
//	                                    role allows)
func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	var status string
	if v, ok := raw["status"]; ok {
		if json.Unmarshal(v, &status) != nil || (status != "active" && status != "inactive") {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be active or inactive")
			return
		}
	}
	var unlock bool
	if v, ok := raw["unlock"]; ok {
		_ = json.Unmarshal(v, &unlock)
	}
	homeSet := false
	var home *string
	if v, ok := raw["home_branch_id"]; ok {
		homeSet = true
		if err := json.Unmarshal(v, &home); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "home_branch_id must be a branch id or null")
			return
		}
		if home != nil && strings.TrimSpace(*home) == "" {
			home = nil
		}
	}
	var resp user
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := loadUser(ctx, tx, id)
		if err != nil {
			return err
		}
		if status == "inactive" && before.Status != "inactive" {
			if id == claims.UserID {
				return errSelf
			}
			var manager, others bool
			if err := tx.QueryRow(ctx, `
				SELECT `+canManageUsers+`,
				       EXISTS (SELECT 1 FROM users u2 WHERE u2.id::text <> $1 AND u2.status = 'active'
				               AND EXISTS (SELECT 1 FROM user_roles ur JOIN role_permissions rp ON rp.role_id = ur.role_id
				                           JOIN permissions p ON p.id = rp.permission_id WHERE ur.user_id = u2.id AND p.code = 'users.manage'))
				FROM users u WHERE u.id::text = $1`, id).Scan(&manager, &others); err != nil {
				return err
			}
			if manager && !others {
				return errLastAdmin
			}
			if _, err := tx.Exec(ctx, `UPDATE users SET status = 'inactive' WHERE id::text = $1`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE user_id::text = $1 AND revoked_at IS NULL`, id); err != nil {
				return err
			}
		} else if status == "active" && before.Status != "active" {
			if _, err := tx.Exec(ctx, `UPDATE users SET status = 'active', failed_login_attempts = 0, locked_until = NULL WHERE id::text = $1`, id); err != nil {
				return err
			}
		}
		if unlock {
			if _, err := tx.Exec(ctx, `
				UPDATE users SET failed_login_attempts = 0, locked_until = NULL,
				       status = CASE WHEN status = 'locked' THEN 'active' ELSE status END
				WHERE id::text = $1`, id); err != nil {
				return err
			}
		}
		if homeSet {
			if home != nil {
				var exists bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM branches WHERE id::text = $1)`, *home).Scan(&exists); err != nil {
					return err
				}
				if !exists {
					return errNoBranch
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE users SET branch_id = $2::uuid WHERE id::text = $1`, id, home); err != nil {
				return err
			}
		}
		if resp, err = loadUser(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "user", EntityID: id, Action: "update", PerformedBy: claims.UserID,
			Before: before, After: resp})
	})
	if err != nil {
		writeErr(w, err, "update the user")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func checkPassword(p string) error {
	if n := len([]rune(p)); n < 8 || len(p) > 72 {
		return errWeakPassword
	}
	return nil
}

var pinPattern = regexp.MustCompile(`^[0-9]{4,6}$`)

func checkPIN(p string) error {
	if !pinPattern.MatchString(p) || strings.Count(p, p[:1]) == len(p) {
		return errBadPIN
	}
	return nil
}

// SetPassword: PUT /users/{id}/password {"new_password"} — users.manage.
// The user's other sessions are signed out; recent passwords can't be
// reused. The password itself is never logged.
func (h *Handler) SetPassword(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var req struct {
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if err := checkPassword(req.NewPassword); err != nil {
		writeErr(w, err, "")
		return
	}
	var resp user
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := loadUser(ctx, tx, id); err != nil {
			return err
		}
		if err := authn.SetPasswordTx(ctx, tx, id, req.NewPassword); err != nil {
			return err
		}
		var err error
		if resp, err = loadUser(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "user", EntityID: id, Action: "update", PerformedBy: claims.UserID,
			After: map[string]any{"password": "reset by an administrator"}, Reason: "password reset"})
	})
	if err != nil {
		writeErr(w, err, "set the password")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func setPIN(ctx context.Context, tx pgx.Tx, id, pin string) error {
	var code *string
	if err := tx.QueryRow(ctx, `SELECT employee_code FROM users WHERE id::text = $1`, id).Scan(&code); err != nil {
		return err
	}
	if code == nil || strings.TrimSpace(*code) == "" {
		return errNoCode
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE users SET pin_hash = $2 WHERE id::text = $1`, id, string(hash))
	return err
}

// SetPIN: PUT /users/{id}/pin {"pin"} — users.manage. The PIN for POS
// sign-in and manager approvals (employee code + PIN).
func (h *Handler) SetPIN(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var req struct {
		PIN string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if err := checkPIN(req.PIN); err != nil {
		writeErr(w, err, "")
		return
	}
	var resp user
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := setPIN(ctx, tx, id, req.PIN); err != nil {
			return err
		}
		var err error
		if resp, err = loadUser(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "user", EntityID: id, Action: "update", PerformedBy: claims.UserID,
			After: map[string]any{"pin": "set by an administrator"}, Reason: "PIN set"})
	})
	if err != nil {
		writeErr(w, err, "set the PIN")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// RemovePIN: DELETE /users/{id}/pin — users.manage. The user can no
// longer sign in or approve with a PIN until a new one is set.
func (h *Handler) RemovePIN(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var resp user
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE users SET pin_hash = NULL WHERE id::text = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		if resp, err = loadUser(ctx, tx, id); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "user", EntityID: id, Action: "update", PerformedBy: claims.UserID,
			After: map[string]any{"pin": "removed"}, Reason: "PIN removed"})
	})
	if err != nil {
		writeErr(w, err, "remove the PIN")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// verifyCurrent: the signed-in user's own current password.
func verifyCurrent(ctx context.Context, tx pgx.Tx, id, current string) error {
	var hash *string
	if err := tx.QueryRow(ctx, `SELECT password_hash FROM users WHERE id::text = $1`, id).Scan(&hash); err != nil {
		return err
	}
	if hash == nil || bcrypt.CompareHashAndPassword([]byte(*hash), []byte(current)) != nil {
		return errWrongPassword
	}
	return nil
}

// ChangeOwnPassword: PUT /me/password {"current_password", "new_password"}
// — any signed-in user. Other sessions are signed out.
func (h *Handler) ChangeOwnPassword(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if err := checkPassword(req.NewPassword); err != nil {
		writeErr(w, err, "")
		return
	}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := verifyCurrent(ctx, tx, claims.UserID, req.CurrentPassword); err != nil {
			return err
		}
		if err := authn.SetPasswordTx(ctx, tx, claims.UserID, req.NewPassword); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "user", EntityID: claims.UserID, Action: "update", PerformedBy: claims.UserID,
			After: map[string]any{"password": "changed by the user"}, Reason: "password changed"})
	})
	if err != nil {
		writeErr(w, err, "change the password")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// ChangeOwnPIN: PUT /me/pin {"current_password", "pin"} — any signed-in user.
func (h *Handler) ChangeOwnPIN(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		PIN             string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if err := checkPIN(req.PIN); err != nil {
		writeErr(w, err, "")
		return
	}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := verifyCurrent(ctx, tx, claims.UserID, req.CurrentPassword); err != nil {
			return err
		}
		if err := setPIN(ctx, tx, claims.UserID, req.PIN); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "user", EntityID: claims.UserID, Action: "update", PerformedBy: claims.UserID,
			After: map[string]any{"pin": "changed by the user"}, Reason: "PIN changed"})
	})
	if err != nil {
		writeErr(w, err, "change the PIN")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
