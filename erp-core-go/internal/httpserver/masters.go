package httpserver

import (
	"context"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/dependencies"
	"erp-core-go/internal/softdelete"
)

// Delete for the masters that had none (migrations/054), every one through
// softdelete.MasterDelete so they answer alike: dependency-checked, soft,
// restorable. Categories, brands, tax slabs, promotions and coupons keep
// their own (same-shaped) handlers.
func registerMasterDeletes(r chi.Router, database *db.DB) {
	gate := func(code string) func(chi.Router) chi.Router {
		return func(cr chi.Router) chi.Router { return cr.With(authn.RequirePermission(database, code)) }
	}

	gate("master_data.delete")(r).Delete("/customers/{id}", softdelete.MasterDelete(database, softdelete.Master{
		Table: "customers", What: "customer",
	}))
	gate("master_data.delete")(r).Delete("/suppliers/{id}", softdelete.MasterDelete(database, softdelete.Master{
		Table: "suppliers", What: "supplier",
	}))
	gate("master_data.delete")(r).Delete("/branches/{id}", softdelete.MasterDelete(database, softdelete.Master{
		Table: "branches", What: "branch",
		// Bookkeeping every branch gets on creation (zero stock rows for
		// every variant, its virtual terminals, letterhead, geofence,
		// access grants) isn't a use of it; stock actually held is (Check).
		Skip:  dependencies.SkipFor["branch"],
		Check: checkBranchDeletable,
		After: func(ctx context.Context, tx pgx.Tx, id string, _ *authn.Claims) error {
			// Its terminals can't take sales for a branch that's gone.
			if _, err := tx.Exec(ctx, `UPDATE pos_terminals SET status = 'inactive', updated_at = now() WHERE branch_id::text = $1`, id); err != nil {
				return err
			}
			authn.InvalidateScopes()
			return nil
		},
	}))
	gate("hr.manage")(r).Delete("/hr/employees/{id}", softdelete.MasterDelete(database, softdelete.Master{
		Table: "users", What: "employee",
		// Sign-in artifacts every account has; anything the employee did
		// (a sale, a receipt, a punch) still counts.
		Skip:  dependencies.SkipFor["employee"],
		Check: checkEmployeeDeletable,
		After: func(context.Context, pgx.Tx, string, *authn.Claims) error {
			authn.InvalidateScopes()
			return nil
		},
	}))
	gate("hr.manage")(r).Delete("/hr/shifts/{id}", softdelete.MasterDelete(database, softdelete.Master{
		Table: "shifts", What: "shift",
	}))
	gate("accounting.manage")(r).Delete("/accounting/accounts/{id}", softdelete.MasterDelete(database, softdelete.Master{
		Table: "chart_of_accounts", What: "account",
		Check: func(ctx context.Context, tx pgx.Tx, id string, _ *authn.Claims) error {
			var system bool
			if err := tx.QueryRow(ctx, `SELECT is_system FROM chart_of_accounts WHERE id::text = $1`, id).Scan(&system); err != nil {
				return err
			}
			if system {
				return &softdelete.Refusal{Code: "SYSTEM_ACCOUNT", Message: "this is a system account the books post to automatically — it can't be deleted"}
			}
			return nil
		},
	}))
	gate("catalog.manage")(r).Delete("/attributes/{id}", softdelete.MasterDelete(database, softdelete.Master{
		Table: "attributes", What: "attribute",
		// Its values and category assignments belong to it.
		Skip: dependencies.SkipFor["attribute"],
		Check: func(ctx context.Context, tx pgx.Tx, id string, _ *authn.Claims) error {
			// Variants name their attributes inside attribute_combo (no
			// foreign key), so "in use" is checked there.
			var used int
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM product_variants pv, attributes a
				WHERE a.id::text = $1 AND (pv.attribute_combo ? a.name OR pv.attribute_combo ? lower(a.name))`, id).Scan(&used); err != nil {
				return err
			}
			if used > 0 {
				return &softdelete.Refusal{Code: "IN_USE", Message: "product variants use this attribute — deactivate it instead"}
			}
			return nil
		},
	}))
}

func checkBranchDeletable(ctx context.Context, tx pgx.Tx, id string, _ *authn.Claims) error {
	var others int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM branches WHERE id::text <> $1`, id).Scan(&others); err != nil {
		return err
	}
	if others == 0 {
		return &softdelete.Refusal{Code: "LAST_BRANCH", Message: "the merchant's only branch can't be deleted"}
	}
	var holding bool
	err := db.Unrestricted(ctx, tx, func() error {
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM stock_levels WHERE branch_id::text = $1 AND (on_hand <> 0 OR reserved <> 0))`, id).Scan(&holding)
	})
	if err != nil {
		return err
	}
	if holding {
		return &softdelete.Refusal{Code: "IN_USE", Message: "this branch still holds stock — transfer or adjust it to zero, or deactivate the branch instead"}
	}
	return nil
}

func checkEmployeeDeletable(ctx context.Context, tx pgx.Tx, id string, claims *authn.Claims) error {
	if id == claims.UserID {
		return &softdelete.Refusal{Code: "SELF_DELETE", Message: "you can't delete your own account"}
	}
	// Never leave the merchant with no one able to manage roles.
	var holds bool
	var others int
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM user_roles ur JOIN role_permissions rp ON rp.role_id = ur.role_id
		               JOIN permissions p ON p.id = rp.permission_id WHERE ur.user_id::text = $1 AND p.code = 'rbac.manage'),
		       (SELECT count(DISTINCT ur.user_id) FROM user_roles ur JOIN users u ON u.id = ur.user_id AND u.status = 'active'
		        JOIN role_permissions rp ON rp.role_id = ur.role_id JOIN permissions p ON p.id = rp.permission_id
		        WHERE p.code = 'rbac.manage' AND ur.user_id::text <> $1)`, id).Scan(&holds, &others); err != nil {
		return err
	}
	if holds && others == 0 {
		return &softdelete.Refusal{Code: "LAST_RBAC_HOLDER", Message: "this would leave no one able to manage roles and permissions"}
	}
	return nil
}
