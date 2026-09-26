package rbac

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type assignRoleRequest struct {
	RoleID string `json:"role_id"`
}

// AssignUserRole: POST /rbac/users/{id}/roles — adds a role to a user
// WITHOUT touching any role they already hold, unlike
// PATCH /hr/employees/{id}'s role_id field (a single-role convenience
// for the common employee-onboarding case). This is the real multi-role
// primitive the schema's user_roles many-to-many table has always
// supported — a user can hold "POS User" and a custom "Sales Manager"
// role at once. Idempotent (ON CONFLICT DO NOTHING): assigning a role a
// user already has is a no-op, not an error.
func (h *Handler) AssignUserRole(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	userID := chi.URLParam(r, "id")
	var req assignRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.RoleID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "role_id is required")
		return
	}

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, userID, req.RoleID)
		return err
	})

	switch {
	case isForeignKeyViolation(err):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "unknown user_id or role_id")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not assign role")
	default:
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "assigned"})
	}
}

// RevokeUserRole: DELETE /rbac/users/{id}/roles/{role_id} — refuses
// (409 LAST_RBAC_HOLDER) if this is the user's only remaining source of
// rbac.manage AND no other user in the merchant would still hold it
// afterward — the same lockout guard SetRolePermissions enforces, from
// the other direction (removing a user's grant rather than a role's).
func (h *Handler) RevokeUserRole(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	userID := chi.URLParam(r, "id")
	roleID := chi.URLParam(r, "role_id")

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var roleGrantsRBAC bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM role_permissions rp JOIN permissions p ON p.id = rp.permission_id
			              WHERE rp.role_id = $1 AND p.code = 'rbac.manage')`, roleID).Scan(&roleGrantsRBAC); err != nil {
			return err
		}
		if roleGrantsRBAC {
			var userHasOtherSource bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS(
					SELECT 1 FROM user_roles ur
					JOIN role_permissions rp ON rp.role_id = ur.role_id
					JOIN permissions p ON p.id = rp.permission_id
					WHERE ur.user_id = $1 AND ur.role_id != $2 AND p.code = 'rbac.manage')`,
				userID, roleID).Scan(&userHasOtherSource); err != nil {
				return err
			}
			if !userHasOtherSource {
				var totalHolders int
				if err := tx.QueryRow(ctx, `
					SELECT COUNT(DISTINCT ur.user_id) FROM user_roles ur
					JOIN role_permissions rp ON rp.role_id = ur.role_id
					JOIN permissions p ON p.id = rp.permission_id
					WHERE p.code = 'rbac.manage'`).Scan(&totalHolders); err != nil {
					return err
				}
				if totalHolders <= 1 {
					return errLastRBACHolder
				}
			}
		}
		_, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1 AND role_id = $2`, userID, roleID)
		return err
	})

	switch {
	case errors.Is(err, errLastRBACHolder):
		httpx.Error(w, http.StatusConflict, "LAST_RBAC_HOLDER", "this would leave no user able to manage roles and permissions")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not revoke role")
	default:
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "revoked"})
	}
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
