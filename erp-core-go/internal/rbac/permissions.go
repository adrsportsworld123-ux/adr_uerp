package rbac

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type permissionResponse struct {
	ID          string `json:"id"`
	Code        string `json:"code"`
	Description string `json:"description"`
}

// ListPermissions: GET /rbac/permissions — the full catalog of every
// permission code that actually exists in this codebase, for the
// role-editing UI's checklist. `permissions` has no merchant_id (it's a
// global catalog of what the application code itself understands, not
// per-tenant data), so unlike every other list endpoint in this package
// this one isn't scoped by tenant — every merchant sees the same set of
// possible permissions, same as `permissions.code` values are hardcoded
// into `authn.RequirePermission(...)` calls throughout the Go source.
func (h *Handler) ListPermissions(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	permissions := []permissionResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, code, COALESCE(description,'') FROM permissions ORDER BY code`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p permissionResponse
			if err := rows.Scan(&p.ID, &p.Code, &p.Description); err != nil {
				return err
			}
			permissions = append(permissions, p)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list permissions")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"permissions": permissions})
}

type setRolePermissionsRequest struct {
	PermissionIDs []string `json:"permission_ids"`
}

// SetRolePermissions: PATCH /rbac/roles/{id}/permissions — replaces the
// role's ENTIRE permission set with permission_ids (an empty array is
// valid: a role with no permissions at all). A full replace rather than
// incremental add/remove endpoints, matching this codebase's existing
// pattern for "the whole set is edited together" data (e.g. pt_slabs,
// commission tiers) — the UI sends a checklist's current state, not a
// diff.
//
// Refuses (409 LAST_RBAC_HOLDER) a change that would leave no user in
// this merchant able to reach rbac.manage at all — the one lockout this
// package actively guards against, since losing every other permission
// is recoverable by someone with rbac.manage fixing it, but losing
// rbac.manage itself everywhere is not recoverable through this API.
func (h *Handler) SetRolePermissions(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var req setRolePermissionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.PermissionIDs == nil {
		req.PermissionIDs = []string{}
	}

	var resp roleResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var hadRBAC bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM role_permissions rp JOIN permissions p ON p.id = rp.permission_id
			              WHERE rp.role_id = $1 AND p.code = 'rbac.manage')`, id).Scan(&hadRBAC); err != nil {
			return err
		}
		newHasRBAC := false
		if hadRBAC && len(req.PermissionIDs) > 0 {
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM permissions WHERE id = ANY($1::uuid[]) AND code = 'rbac.manage')`, req.PermissionIDs).
				Scan(&newHasRBAC); err != nil {
				return err
			}
		}
		if hadRBAC && !newHasRBAC {
			var otherHolders int
			if err := tx.QueryRow(ctx, `
				SELECT COUNT(DISTINCT ur.user_id) FROM user_roles ur
				JOIN role_permissions rp ON rp.role_id = ur.role_id AND rp.role_id != $1
				JOIN permissions p ON p.id = rp.permission_id
				WHERE p.code = 'rbac.manage'`, id).Scan(&otherHolders); err != nil {
				return err
			}
			if otherHolders == 0 {
				return errLastRBACHolder
			}
		}

		if _, err := tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id = $1`, id); err != nil {
			return err
		}
		if len(req.PermissionIDs) > 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO role_permissions (role_id, permission_id)
				SELECT $1, unnest($2::uuid[])`, id, req.PermissionIDs); err != nil {
				return err
			}
		}
		var err error
		resp, err = loadRole(ctx, tx, id)
		return err
	})

	switch {
	case errors.Is(err, errLastRBACHolder):
		httpx.Error(w, http.StatusConflict, "LAST_RBAC_HOLDER", "this would leave no user able to manage roles and permissions")
	case errors.Is(err, errRoleNotFound):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no role with this id")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update role permissions")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}
