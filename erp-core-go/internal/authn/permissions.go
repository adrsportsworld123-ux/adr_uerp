package authn

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

// FetchPermissions resolves every permission code granted to userID via its
// roles — the schema's permissions/role_permissions model
// (migrations/005_permissions.sql), looked up fresh per request rather than
// embedded in the JWT so a permission grant change takes effect on the next
// request, not the next login.
func FetchPermissions(ctx context.Context, tx pgx.Tx, userID string) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT p.code
		FROM permissions p
		JOIN role_permissions rp ON rp.permission_id = p.id
		JOIN user_roles ur ON ur.role_id = rp.role_id
		WHERE ur.user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var codes []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, rows.Err()
}

func HasPermission(codes []string, want string) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

// RequirePermission is a router-level gate, same shape as RequireAuth:
// resolves the caller's permissions inside a WithTenant lookup and rejects
// with 403 FORBIDDEN before the handler ever runs. Use this for any new
// mutating endpoint that needs a permission check — it's the general
// mechanism AdjustStock's doc comment called for instead of each handler
// re-implementing its own role-name check.
func RequirePermission(database *db.DB, code string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := FromContext(r.Context())
			if !ok {
				httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
				return
			}
			var allowed bool
			err := database.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
				codes, err := FetchPermissions(ctx, tx, claims.UserID)
				if err != nil {
					return err
				}
				allowed = HasPermission(codes, code)
				return nil
			})
			if err != nil {
				httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not check permissions")
				return
			}
			if !allowed {
				httpx.Error(w, http.StatusForbidden, "FORBIDDEN", "you don't have the "+code+" permission")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
