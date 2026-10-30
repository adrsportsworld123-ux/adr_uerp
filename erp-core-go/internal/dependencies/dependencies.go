// Package dependencies answers "what uses this record?" for master data
// (Phase 11, phased_roadmap.md): the counts behind the admin's
// DependencyDialog, and the check every master delete runs before it
// tries.
//
// The foreign keys are read from Postgres's own catalog (pg_constraint)
// rather than a hand-kept list, so a table added by a future migration is
// covered automatically and the answer can never drift from the schema
// that actually enforces it. Table and column names come from the catalog,
// never from the request, and are quoted with pgx.Identifier.
package dependencies

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

// CountCap bounds each count: the UI shows "1,000+" past it, and a hot
// table (sales lines) never gets a full scan just to fill a dialog.
const CountCap = 1000

type Dependency struct {
	Table string `json:"table"`
	Label string `json:"label"`
	Count int    `json:"count"`
	// Capped is true when Count stopped at CountCap.
	Capped bool `json:"capped"`
	// Blocking is true when this reference would stop a delete: the
	// foreign key isn't ON DELETE CASCADE / SET NULL.
	Blocking bool `json:"blocking"`
}

type fkRef struct {
	table, column string
	blocking      bool
}

func referencingKeys(ctx context.Context, tx pgx.Tx, table string) ([]fkRef, error) {
	rows, err := tx.Query(ctx, `
		SELECT cl.relname, a.attname, c.confdeltype NOT IN ('c', 'n')
		FROM pg_constraint c
		JOIN pg_class cl ON cl.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = cl.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		WHERE c.contype = 'f'
		  AND c.confrelid = to_regclass('public.' || $1)
		  AND array_length(c.conkey, 1) = 1
		  AND n.nspname = 'public'
		ORDER BY cl.relname, a.attname`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []fkRef
	for rows.Next() {
		var r fkRef
		if err := rows.Scan(&r.table, &r.column, &r.blocking); err != nil {
			return nil, err
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}

// Count returns every non-empty reference to the given rows of table, in
// the caller's tenant transaction. skip names referencing tables to leave
// out (e.g. a product's own variants, which are part of it, not a use of it).
func Count(ctx context.Context, tx pgx.Tx, table string, ids []string, skip ...string) ([]Dependency, error) {
	refs, err := referencingKeys(ctx, tx, table)
	if err != nil {
		return nil, err
	}
	out := []Dependency{}
	for _, ref := range refs {
		if contains(skip, ref.table) {
			continue
		}
		var n int
		q := `SELECT count(*) FROM (SELECT 1 FROM ` + pgx.Identifier{ref.table}.Sanitize() +
			` WHERE ` + pgx.Identifier{ref.column}.Sanitize() + `::text = ANY($1) LIMIT ` + strconv.Itoa(CountCap+1) + `) s`
		if err := tx.QueryRow(ctx, q, ids).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			continue
		}
		d := Dependency{Table: ref.table, Label: label(ref.table, table, ref.column), Count: n, Blocking: ref.blocking}
		if n > CountCap {
			d.Count, d.Capped = CountCap, true
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out, nil
}

// AnyBlocking reports whether a delete would be refused.
func AnyBlocking(deps []Dependency) bool {
	for _, d := range deps {
		if d.Blocking {
			return true
		}
	}
	return false
}

// entities maps the public entity name to its table. Only masters the
// admin can delete or deactivate are listed — this is not a generic
// "count anything" endpoint.
var entities = map[string]string{
	"category":  "categories",
	"brand":     "brands",
	"tax_slab":  "tax_slabs",
	"supplier":  "suppliers",
	"customer":  "customers",
	"branch":    "branches",
	"product":   "products",
	"promotion": "promotions",
	"coupon":    "coupons",
	"employee":  "users",
	"account":   "chart_of_accounts",
	"shift":     "shifts",
	"attribute": "attributes",
}

// SkipFor: referencing tables that belong to the record rather than use
// it, per entity — the same lists the masters' DELETE handlers pass
// (internal/httpserver/masters.go), so the dialog and the delete agree.
var SkipFor = map[string][]string{
	"branch":    {"stock_levels", "pos_terminals", "branch_print_settings", "branch_geofences", "user_branches"},
	"employee":  {"refresh_tokens", "password_history", "password_reset_tokens", "user_roles", "user_branches"},
	"attribute": {"attribute_values", "category_attributes"},
}

type Handler struct {
	DB *db.DB
}

// Get: GET /dependencies/{entity}/{id}
// 200: { "entity", "id", "dependencies": [...], "deletable": bool }
// A product's count includes everything that uses any of its variants
// (sales lines, GRN lines, stock), since that's what a user means by
// "is this product used anywhere".
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	entity := chi.URLParam(r, "entity")
	id := chi.URLParam(r, "id")
	table, ok := entities[entity]
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "unknown entity "+entity)
		return
	}

	var deps []Dependency
	found := true
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		// RLS makes another tenant's row invisible here, so this is also
		// the tenant check.
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+pgx.Identifier{table}.Sanitize()+` WHERE id::text = $1)`, id).Scan(&found); err != nil || !found {
			return err
		}
		var err error
		if entity == "product" {
			deps, err = productDependencies(ctx, tx, id)
		} else {
			deps, err = Count(ctx, tx, table, []string{id}, SkipFor[entity]...)
		}
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not count dependencies")
		return
	}
	if !found {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", entity+" not found")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"entity": entity, "id": id, "dependencies": deps, "deletable": !AnyBlocking(deps),
	})
}

func productDependencies(ctx context.Context, tx pgx.Tx, productID string) ([]Dependency, error) {
	own, err := Count(ctx, tx, "products", []string{productID}, "product_variants")
	if err != nil {
		return nil, err
	}
	var variantIDs []string
	rows, err := tx.Query(ctx, `SELECT id::text FROM product_variants WHERE product_id = $1`, productID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		variantIDs = append(variantIDs, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(variantIDs) == 0 {
		return own, nil
	}
	viaVariants, err := Count(ctx, tx, "product_variants", variantIDs)
	if err != nil {
		return nil, err
	}
	all := append(own, viaVariants...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Count > all[j].Count })
	return all, nil
}

// Readable names for the tables users will actually see in a dependency
// list. Anything not listed falls back to its table name, humanized.
var labels = map[string]string{
	"sales_order_lines":              "Sales lines",
	"sales_orders":                   "Sales",
	"products":                       "Products",
	"product_variants":               "Product variants",
	"categories":                     "Sub-categories",
	"category_attributes":            "Category attributes",
	"grn_lines":                      "Goods receipt lines",
	"goods_receipts":                 "Goods receipts",
	"purchase_bills":                 "Purchase bills",
	"purchase_returns":               "Purchase returns",
	"purchase_return_lines":          "Purchase return lines",
	"stock_levels":                   "Stock records",
	"stock_batches":                  "Stock batches",
	"stock_batch_movements":          "Batch movements",
	"stock_reservations":             "Stock reservations",
	"inventory_adjustments":          "Stock adjustments",
	"barcodes":                       "Barcodes",
	"price_history":                  "Price history entries",
	"price_list_items":               "Price list entries",
	"price_rules":                    "Price rules",
	"markup_rules":                   "Markup rules",
	"price_suggestion_rules":         "Price suggestion rules",
	"scheduled_price_changes":        "Scheduled price changes",
	"promotions":                     "Promotions",
	"promotion_products":             "Promotion products",
	"coupons":                        "Coupons",
	"quotation_lines":                "Quotation lines",
	"quotations":                     "Quotations",
	"branch_transfer_lines":          "Branch transfer lines",
	"branch_transfers":               "Branch transfers",
	"pos_terminals":                  "POS terminals",
	"users":                          "Users",
	"loyalty_ledger":                 "Loyalty entries",
	"credit_payments":                "Credit payments",
	"customer_payments":              "Customer payments",
	"cash_reconciliations":           "Cash reconciliations",
	"inventory_reconciliations":      "Stock counts",
	"inventory_reconciliation_lines": "Stock count lines",
	"prescriptions":                  "Prescriptions",
	"einvoices":                      "E-invoices",
	"sales_order_discounts":          "Sales it discounted",
	"coupon_redemptions":             "Redemptions",
}

func label(refTable, target, column string) string {
	if refTable == target && strings.HasPrefix(column, "parent") {
		if target == "categories" {
			return "Sub-categories"
		}
	}
	if l, ok := labels[refTable]; ok {
		return l
	}
	s := strings.ReplaceAll(refTable, "_", " ")
	return strings.ToUpper(s[:1]) + s[1:]
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
