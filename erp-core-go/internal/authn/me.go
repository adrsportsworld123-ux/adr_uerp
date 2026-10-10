package authn

import (
	"context"
	"net/http"
	"sort"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

// MeHandler: GET /me/permissions — the caller's effective permission
// codes, resolved fresh the same way RequirePermission resolves them
// (FetchPermissions), so the UI can disable an action with a reason
// before the user tries it (Phase 11, phased_roadmap.md). This is a hint
// for the interface only: every gate stays enforced server-side, and a
// stale copy in a client can never grant anything.
type MeHandler struct {
	DB *db.DB
}

func (h *MeHandler) Permissions(w http.ResponseWriter, r *http.Request) {
	claims, ok := FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var codes []string
	type branchRef struct {
		BranchID string `json:"branch_id"`
		Name     string `json:"name"`
	}
	scope, _ := db.ScopeFrom(r.Context())
	branches := []branchRef{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if codes, err = FetchPermissions(ctx, tx, claims.UserID); err != nil {
			return err
		}
		if scope.All {
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT id::text, name FROM branches WHERE id::text = ANY($1) ORDER BY name`, scope.BranchIDs)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b branchRef
			if err := rows.Scan(&b.BranchID, &b.Name); err != nil {
				return err
			}
			branches = append(branches, b)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load permissions")
		return
	}
	if codes == nil {
		codes = []string{}
	}
	sort.Strings(codes)
	roles := claims.Roles
	if roles == nil {
		roles = []string{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"user_id":     claims.UserID,
		"branch_id":   claims.BranchID,
		"roles":       roles,
		"permissions": codes,
		// Phase 15: which branches' data this user sees. all = every
		// branch; otherwise exactly `branches`. A hint for the UI (branch
		// pickers, empty states); the database enforces it.
		"data_scope": map[string]any{"all": scope.All, "branches": branches},
	})
}
