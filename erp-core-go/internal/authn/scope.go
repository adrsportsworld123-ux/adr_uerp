package authn

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

// Phase 15: branch data scope (migrations/044_branch_data_scope.sql).
// BranchScope runs after RequireAuth on every protected route. It works
// out which branches the caller may see (a role with data_scope 'all'
// means every branch; otherwise the user's home branch, user_branches, and
// a PIN session's terminal branch) and attaches it to the request, so every
// WithTenant transaction sets app.branch_scope and the database's
// restrictive policies do the filtering.
//
// It also refuses, up front and with a clear 403, a request that names a
// branch outside the scope (branch_id / from_branch_id / to_branch_id in
// the query or a JSON body). Without this a write into another branch
// would still be stopped by the database, but as an opaque error.

const scopeTTL = 30 * time.Second

type cachedScope struct {
	scope   db.BranchScope
	expires time.Time
}

var scopeCache sync.Map // tenant|user|claimBranch -> cachedScope

// InvalidateScopes drops every cached scope: call after changing a role's
// data scope or a user's branches, so the change applies at once rather
// than within scopeTTL.
func InvalidateScopes() {
	scopeCache.Range(func(k, _ any) bool {
		scopeCache.Delete(k)
		return true
	})
}

// ResolveScope computes a user's branch scope.
func ResolveScope(ctx context.Context, tx pgx.Tx, userID, sessionBranchID string) (db.BranchScope, error) {
	var all bool
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(bool_or(r.data_scope = 'all'), false)
		FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE ur.user_id::text = $1`, userID).Scan(&all); err != nil {
		return db.BranchScope{}, err
	}
	if all {
		return db.BranchScope{All: true}, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT branch_id::text FROM users WHERE id::text = $1 AND branch_id IS NOT NULL
		UNION SELECT branch_id::text FROM user_branches WHERE user_id::text = $1
		UNION SELECT $2 WHERE $2 <> ''
		ORDER BY 1`, userID, sessionBranchID)
	if err != nil {
		return db.BranchScope{}, err
	}
	defer rows.Close()
	scope := db.BranchScope{BranchIDs: []string{}}
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return scope, err
		}
		scope.BranchIDs = append(scope.BranchIDs, b)
	}
	return scope, rows.Err()
}

func BranchScopeMiddleware(database *db.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := FromContext(r.Context())
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			key := claims.TenantID + "|" + claims.UserID + "|" + claims.BranchID
			var scope db.BranchScope
			if c, hit := scopeCache.Load(key); hit && time.Now().Before(c.(cachedScope).expires) {
				scope = c.(cachedScope).scope
			} else {
				err := database.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
					var err error
					scope, err = ResolveScope(ctx, tx, claims.UserID, claims.BranchID)
					return err
				})
				if err != nil {
					httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not resolve branch access")
					return
				}
				scopeCache.Store(key, cachedScope{scope: scope, expires: time.Now().Add(scopeTTL)})
			}

			if !scope.All {
				if bad := outOfScope(r, scope); bad != "" {
					httpx.ErrorWithDetails(w, http.StatusForbidden, "BRANCH_FORBIDDEN",
						"you can only see and act on your own branch's data", map[string]any{"branch_id": bad})
					return
				}
			}
			next.ServeHTTP(w, r.WithContext(db.WithBranchScope(r.Context(), scope)))
		})
	}
}

// outOfScope returns the first branch id the request names that the scope
// doesn't allow, or "". A transfer names two branches: it's in scope if
// either end is (the same rule as branch_transfers' policy).
func outOfScope(r *http.Request, scope db.BranchScope) string {
	ids := map[string]string{}
	q := r.URL.Query()
	for _, k := range []string{"branch_id", "from_branch_id", "to_branch_id"} {
		if v := strings.TrimSpace(q.Get(k)); v != "" {
			ids[k] = v
		}
	}
	if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch) &&
		strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(raw))
		if err == nil {
			var body map[string]any
			if json.Unmarshal(raw, &body) == nil {
				for _, k := range []string{"branch_id", "from_branch_id", "to_branch_id"} {
					if v, ok := body[k].(string); ok && strings.TrimSpace(v) != "" {
						ids[k] = strings.TrimSpace(v)
					}
				}
			}
		}
	}
	from, to := ids["from_branch_id"], ids["to_branch_id"]
	if from != "" && to != "" {
		if !scope.Allows(from) && !scope.Allows(to) {
			return from
		}
	} else if v := from + to; v != "" && !scope.Allows(v) {
		return v
	}
	if v := ids["branch_id"]; v != "" && !scope.Allows(v) {
		return v
	}
	return ""
}
