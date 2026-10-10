package subscriptions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

// countSQL: what each limit counts, across the whole merchant (Unrestricted:
// a Branch Manager's branch scope must not hide the other branches' usage).
// $1 is an id to leave out (the row a PATCH is reactivating), or ”.
//   - branches and products: active ones; deactivating frees a slot.
//   - pos_terminals: active tills only; the per-branch back-office and online
//     storefront terminals are virtual and never count.
//   - transactions_per_month: finalized sales this calendar month (IST),
//     however they were made.
var countSQL = map[string]string{
	LimitBranches: `SELECT count(*) FROM branches WHERE status = 'active' AND id::text <> $1`,
	LimitPOSTerminals: `SELECT count(*) FROM pos_terminals WHERE merchant_id = current_setting('app.tenant_id')::uuid
		AND channel = 'pos' AND status = 'active' AND id::text <> $1`,
	LimitProducts: `SELECT count(*) FROM products WHERE status = 'active' AND id::text <> $1`,
	LimitTransactionsPM: `SELECT count(*) FROM sales_orders WHERE status = 'finalized' AND $1 = $1
		AND created_at >= date_trunc('month', now() AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata'`,
}

// Count returns current usage of one limit kind inside the tenant's tx.
func Count(ctx context.Context, tx pgx.Tx, kind, excludeID string) (int64, error) {
	q, ok := countSQL[kind]
	if !ok {
		return 0, fmt.Errorf("subscriptions: unknown limit %q", kind)
	}
	var n int64
	err := db.Unrestricted(ctx, tx, func() error {
		return tx.QueryRow(ctx, q, excludeID).Scan(&n)
	})
	return n, err
}

// UsageItem is one limit's usage for the subscription page.
type UsageItem struct {
	Used  int64  `json:"used"`
	Limit *int64 `json:"limit"`
}

// Usage counts every limit kind for the plan.
func Usage(ctx context.Context, tx pgx.Tx, plan Plan) (map[string]UsageItem, error) {
	out := map[string]UsageItem{}
	for _, k := range AllLimits {
		n, err := Count(ctx, tx, k, "")
		if err != nil {
			return nil, err
		}
		out[k] = UsageItem{Used: n, Limit: plan.Limit(k)}
	}
	return out, nil
}

// LimitError: creating one more would go over the plan.
type LimitError struct {
	Kind  string
	Used  int64
	Limit int64
	Plan  Plan
	Next  *Plan
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("plan limit reached: %s %d of %d", e.Kind, e.Used, e.Limit)
}

var limitNoun = map[string]string{
	LimitBranches:       "active branches",
	LimitPOSTerminals:   "active POS terminals",
	LimitProducts:       "active products",
	LimitTransactionsPM: "sales this month",
}

func (e *LimitError) Write(w http.ResponseWriter) {
	msg := fmt.Sprintf("the %s plan allows %d %s and %d are in use", e.Plan.Name, e.Limit, limitNoun[e.Kind], e.Used)
	if e.Next != nil {
		msg += "; " + e.Next.Name + " allows more"
	}
	if e.Kind != LimitTransactionsPM {
		msg += ". Deactivating one frees a place"
	}
	details := map[string]any{"limit_kind": e.Kind, "used": e.Used, "limit": e.Limit, "plan": e.Plan.Code}
	if e.Next != nil {
		details["upgrade_to"] = e.Next.Code
	}
	httpx.ErrorWithDetails(w, http.StatusForbidden, "PLAN_LIMIT_REACHED", msg, details)
}

// Check refuses one more of kind when the plan's limit is already used.
func Check(ctx context.Context, tx pgx.Tx, e Entitlement, kind, excludeID string) error {
	l := e.Plan.Limit(kind)
	if l == nil {
		return nil
	}
	n, err := Count(ctx, tx, kind, excludeID)
	if err != nil {
		return err
	}
	if n >= *l {
		return &LimitError{Kind: kind, Used: n, Limit: *l, Plan: e.Plan}
	}
	return nil
}

// Limit guards a create (POST) or a reactivation (PATCH whose body sets
// "status": "active" on the row named by the {id} URL parameter) against
// the plan's limit for kind. Downgrading never deactivates anything: a
// merchant over a limit keeps what they have and can't add more.
//
// The count runs just before the handler's own transaction, so two
// simultaneous creates can both pass at the last free place; one over is
// an accepted bound for a plan limit (unlike stock, nothing is oversold).
func Limit(database *db.DB, kind string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			e, ok := FromContext(r.Context())
			if !ok || e.Plan.Limit(kind) == nil {
				next.ServeHTTP(w, r)
				return
			}
			exclude := ""
			if r.Method == http.MethodPatch {
				body, _ := io.ReadAll(r.Body)
				r.Body = io.NopCloser(bytes.NewReader(body))
				var b struct {
					Status *string `json:"status"`
				}
				if json.Unmarshal(body, &b) != nil || b.Status == nil || *b.Status != "active" {
					next.ServeHTTP(w, r)
					return
				}
				exclude = chi.URLParam(r, "id")
			}
			claims, _ := authn.FromContext(r.Context())
			var limitErr *LimitError
			err := database.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
				if err := Check(ctx, tx, e, kind, exclude); err != nil {
					if le, ok := err.(*LimitError); ok {
						limitErr = le
						return nil
					}
					return err
				}
				return nil
			})
			if err != nil {
				httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not check the plan limit")
				return
			}
			if limitErr != nil {
				limitErr.Next = cheapestAbove(r.Context(), database, kind, limitErr.Used)
				limitErr.Write(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
