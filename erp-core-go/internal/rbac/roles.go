// Package rbac implements UACL — user/role/permission administration
// over a mechanism that has existed since migrations/001_schema.sql
// (`roles`, `permissions`, `role_permissions`, `user_roles`) but never
// had a management API: every role/permission grant in this codebase
// before this package was seeded directly by a migration file, so a
// merchant could not create a custom role (e.g. the FRD's own named
// "Accountant"/"HR Manager"/"Sales Manager") or change what an existing
// one could do without a developer editing SQL. Gated end to end by the
// new `rbac.manage` permission, Merchant Admin only — see
// migrations/025_rbac.sql's header comment for why this is the one
// surface in the system that stays top-tier-only rather than also open
// to Branch Manager.
package rbac

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB *db.DB
}

var (
	errRoleInUse      = errors.New("role is currently assigned to one or more users")
	errLastRBACHolder = errors.New("this would leave no user able to manage roles and permissions")
	errRoleNotFound   = pgx.ErrNoRows
)

type roleResponse struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	IsSystemDefault bool     `json:"is_system_default"`
	PermissionCodes []string `json:"permission_codes"`
	UserCount       int      `json:"user_count"`
}

// loadRole assembles one role's full detail — its permission codes and
// how many users currently hold it — in two follow-up queries rather
// than a fan-out join, so the counts can't be thrown off by the
// multiplicative row blow-up a single join across both child tables
// would cause.
func loadRole(ctx context.Context, tx pgx.Tx, id string) (roleResponse, error) {
	var role roleResponse
	role.PermissionCodes = []string{}
	if err := tx.QueryRow(ctx, `SELECT id, name, is_system_default FROM roles WHERE id = $1`, id).
		Scan(&role.ID, &role.Name, &role.IsSystemDefault); err != nil {
		return role, err
	}
	rows, err := tx.Query(ctx, `
		SELECT p.code FROM role_permissions rp JOIN permissions p ON p.id = rp.permission_id
		WHERE rp.role_id = $1 ORDER BY p.code`, id)
	if err != nil {
		return role, err
	}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			rows.Close()
			return role, err
		}
		role.PermissionCodes = append(role.PermissionCodes, code)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return role, err
	}
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM user_roles WHERE role_id = $1`, id).Scan(&role.UserCount); err != nil {
		return role, err
	}
	return role, nil
}

type createRoleRequest struct {
	Name string `json:"name"`
}

// CreateRole: POST /rbac/roles — a brand-new role with no permissions
// yet; assign them via PATCH /rbac/roles/{id}/permissions.
func (h *Handler) CreateRole(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name is required")
		return
	}

	var resp roleResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO roles (id, merchant_id, name, is_system_default)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, false)
			RETURNING id`, req.Name,
		).Scan(&id); err != nil {
			return err
		}
		var err error
		resp, err = loadRole(ctx, tx, id)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create role")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// ListRoles: GET /rbac/roles
func (h *Handler) ListRoles(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	roles := []roleResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id FROM roles
			WHERE merchant_id = current_setting('app.tenant_id')::uuid OR merchant_id IS NULL
			ORDER BY name`)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range ids {
			role, err := loadRole(ctx, tx, id)
			if err != nil {
				return err
			}
			roles = append(roles, role)
		}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list roles")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"roles": roles})
}

// GetRole: GET /rbac/roles/{id}
func (h *Handler) GetRole(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")

	var resp roleResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		resp, err = loadRole(ctx, tx, id)
		return err
	})
	if errors.Is(err, errRoleNotFound) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no role with this id")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load role")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

type updateRoleRequest struct {
	Name string `json:"name"`
}

// UpdateRole: PATCH /rbac/roles/{id} — rename only; a role's permission
// set is managed separately via PATCH /rbac/roles/{id}/permissions.
func (h *Handler) UpdateRole(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var req updateRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name is required")
		return
	}

	var resp roleResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE roles SET name = $2 WHERE id = $1`, id, req.Name); err != nil {
			return err
		}
		var err error
		resp, err = loadRole(ctx, tx, id)
		return err
	})
	if errors.Is(err, errRoleNotFound) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no role with this id")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update role")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// DeleteRole: DELETE /rbac/roles/{id} — refuses if any user currently
// holds this role (409 ROLE_IN_USE) rather than silently cascading the
// delete into user_roles (the schema's own FK is ON DELETE CASCADE,
// which would otherwise revoke this role from every holder with no
// warning) — an admin must reassign or remove those users' hold on it
// first, a deliberate speed bump for a hard-to-reverse action.
func (h *Handler) DeleteRole(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var userCount int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM user_roles WHERE role_id = $1`, id).Scan(&userCount); err != nil {
			return err
		}
		if userCount > 0 {
			return errRoleInUse
		}
		ct, err := tx.Exec(ctx, `DELETE FROM roles WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return errRoleNotFound
		}
		return nil
	})

	switch {
	case errors.Is(err, errRoleInUse):
		httpx.Error(w, http.StatusConflict, "ROLE_IN_USE", "this role is currently assigned to one or more users — reassign them first")
	case errors.Is(err, errRoleNotFound):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no role with this id")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not delete role")
	default:
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}
