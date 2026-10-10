package rbac

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// Phase 15: who sees which branches (migrations/044_branch_data_scope.sql).
// GET /rbac/data-access lists every active user with their roles, home
// branch, extra branches and the scope that comes out of them;
// PUT /rbac/users/{id}/branches sets a user's extra branches. A role's
// scope itself is PATCH /rbac/roles/{id} {data_scope}. rbac.manage.

type branchRef struct {
	BranchID string `json:"branch_id"`
	Name     string `json:"name"`
}

type userAccess struct {
	UserID        string      `json:"user_id"`
	Name          string      `json:"name"`
	EmployeeCode  *string     `json:"employee_code"`
	Email         *string     `json:"email"`
	Roles         []string    `json:"roles"`
	HomeBranch    *branchRef  `json:"home_branch"`
	ExtraBranches []branchRef `json:"extra_branches"`
	AllBranches   bool        `json:"all_branches"` // the effective scope
}

func (h *Handler) DataAccess(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	users := []userAccess{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT u.id, u.name, u.employee_code, u.email, u.branch_id::text, hb.name,
			       COALESCE((SELECT array_agg(r.name ORDER BY r.name) FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE ur.user_id = u.id), '{}'),
			       COALESCE((SELECT bool_or(r.data_scope = 'all') FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE ur.user_id = u.id), false)
			FROM users u LEFT JOIN branches hb ON hb.id = u.branch_id
			WHERE u.status = 'active'
			ORDER BY u.name`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var a userAccess
			var homeID, homeName *string
			if err := rows.Scan(&a.UserID, &a.Name, &a.EmployeeCode, &a.Email, &homeID, &homeName, &a.Roles, &a.AllBranches); err != nil {
				rows.Close()
				return err
			}
			if homeID != nil {
				a.HomeBranch = &branchRef{BranchID: *homeID, Name: *homeName}
			}
			a.ExtraBranches = []branchRef{}
			users = append(users, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		idx := map[string]int{}
		for i, u := range users {
			idx[u.UserID] = i
		}
		rows, err = tx.Query(ctx, `
			SELECT ub.user_id::text, b.id::text, b.name FROM user_branches ub JOIN branches b ON b.id = ub.branch_id ORDER BY b.name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var uid string
			var b branchRef
			if err := rows.Scan(&uid, &b.BranchID, &b.Name); err != nil {
				return err
			}
			if i, ok := idx[uid]; ok {
				users[i].ExtraBranches = append(users[i].ExtraBranches, b)
			}
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load data access")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"users": users})
}

var errBadBranch = errors.New("not a branch of this merchant")

// SetUserBranches: PUT /rbac/users/{id}/branches {branch_ids} — replaces the
// user's extra branches (their home branch is part of their employee
// record). Takes effect on the user's next request.
func (h *Handler) SetUserBranches(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	userID := chi.URLParam(r, "id")
	var req struct {
		BranchIDs []string `json:"branch_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.BranchIDs == nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_ids (a list, possibly empty) is required")
		return
	}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id::text = $1)`, userID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return pgx.ErrNoRows
		}
		var before []string
		if err := tx.QueryRow(ctx, `SELECT COALESCE(array_agg(branch_id::text ORDER BY branch_id), '{}') FROM user_branches WHERE user_id::text = $1`, userID).Scan(&before); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM user_branches WHERE user_id::text = $1`, userID); err != nil {
			return err
		}
		for _, b := range req.BranchIDs {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM branches WHERE id::text = $1)`, b).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return errBadBranch
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO user_branches (user_id, branch_id, merchant_id) VALUES ($1::uuid, $2::uuid, current_setting('app.tenant_id')::uuid)
				ON CONFLICT DO NOTHING`, userID, b); err != nil {
				return err
			}
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "user_branches", EntityID: userID, Action: "update", PerformedBy: claims.UserID,
			Before: map[string]any{"branch_ids": before}, After: map[string]any{"branch_ids": req.BranchIDs}})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "user not found")
	case errors.Is(err, errBadBranch):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REFERENCE", "every branch_id must be a branch of this merchant")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the user's branches")
	default:
		authn.InvalidateScopes()
		httpx.JSON(w, http.StatusOK, map[string]any{"user_id": userID, "branch_ids": req.BranchIDs})
	}
}
