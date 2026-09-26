package hr

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type roleResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListRoles: GET /hr/roles — there was no GET /roles endpoint anywhere in
// this codebase before Phase 5; it's added here, gated the same as the
// rest of employee management, specifically to populate the "assign a
// role" picker POST/PATCH /hr/employees needs. If a use for it emerges
// outside HR, it can move to its own top-level route without a schema
// change — the query is already generic.
//
// `roles` does have the standard tenant_isolation RLS policy (applied via
// 001_schema.sql's dynamic per-table loop — easy to miss with a plain
// grep for "CREATE POLICY" since it's built with format() inside a DO
// block, not a literal top-level statement), so this query is safe
// without an explicit merchant_id filter. The `OR merchant_id IS NULL`
// below is for the schema's documented-but-currently-unused "system
// default role" template concept (a NULL-merchant_id row) — under
// today's RLS policy such a row would never actually match
// `merchant_id = current_setting(...)::uuid` and so could never be
// visible here anyway; this clause is future-proofing for if that
// policy is ever loosened to support shared templates, not a fix for a
// real gap today.
func (h *Handler) ListRoles(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	roles := []roleResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, name FROM roles
			WHERE merchant_id = current_setting('app.tenant_id')::uuid OR merchant_id IS NULL
			ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var rr roleResponse
			if err := rows.Scan(&rr.ID, &rr.Name); err != nil {
				return err
			}
			roles = append(roles, rr)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list roles")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"roles": roles})
}
