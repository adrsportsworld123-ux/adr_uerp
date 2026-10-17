package softdelete

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/dependencies"
	"erp-core-go/internal/httpx"
)

// Refusal is a master-specific reason a delete can't happen (a system
// account, deleting yourself), answered as 409 with its own code.
type Refusal struct {
	Code    string
	Message string
}

func (r *Refusal) Error() string { return r.Message }

// Master describes one master for MasterDelete (migrations/054): the
// table, the word used in messages, referencing tables that don't count
// as a use (rows that belong to the record itself), and optional extra
// rules checked before and work done after the delete.
type Master struct {
	Table string
	What  string // "customer", "chart of accounts entry"
	Skip  []string
	// Check runs with the row locked; a *Refusal answers 409.
	Check func(ctx context.Context, tx pgx.Tx, id string, claims *authn.Claims) error
	// After runs once the row is deleted, in the same transaction.
	After func(ctx context.Context, tx pgx.Tx, id string, claims *authn.Claims) error
}

// MasterDelete: DELETE /{master}/{id} for every master the same way —
// 204 when moved to the Recycle Bin; 404 when there is no such live
// record; 409 IN_USE with the dependency list (the same one
// GET /dependencies/{entity}/{id} shows) when something still uses it,
// so the screen offers "deactivate instead"; 409 with the master's own
// code when one of its rules refuses.
func MasterDelete(database *db.DB, m Master) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := authn.FromContext(r.Context())
		if !ok {
			httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
			return
		}
		id := chi.URLParam(r, "id")
		var blockers []dependencies.Dependency
		err := database.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
			var locked string
			if err := tx.QueryRow(ctx, `SELECT id::text FROM `+pgx.Identifier{m.Table}.Sanitize()+` WHERE id::text = $1 FOR UPDATE`, id).Scan(&locked); err != nil {
				return err
			}
			if m.Check != nil {
				if err := m.Check(ctx, tx, locked, claims); err != nil {
					return err
				}
			}
			deps, err := dependencies.Count(ctx, tx, m.Table, []string{locked}, m.Skip...)
			if err != nil {
				return err
			}
			if dependencies.AnyBlocking(deps) {
				blockers = deps
				return nil
			}
			if err := Delete(ctx, tx, m.Table, locked, claims.UserID, ""); err != nil {
				return err
			}
			if m.After != nil {
				return m.After(ctx, tx, locked, claims)
			}
			return nil
		})
		var refusal *Refusal
		switch {
		case errors.As(err, &refusal):
			httpx.Error(w, http.StatusConflict, refusal.Code, refusal.Message)
		case errors.Is(err, pgx.ErrNoRows):
			httpx.Error(w, http.StatusNotFound, "NOT_FOUND", m.What+" not found")
		case err != nil:
			httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not delete the "+m.What)
		case blockers != nil:
			httpx.ErrorWithDetails(w, http.StatusConflict, "IN_USE",
				"this "+m.What+" is still in use — deactivate it instead", map[string]any{"dependencies": blockers})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}
}
