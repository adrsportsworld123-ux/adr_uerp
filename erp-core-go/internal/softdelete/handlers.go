package softdelete

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

// binEntity is one kind of record the recycle bin shows and can restore.
// Lines (cart, quotation and price-list lines) are kept for the record
// but are not restorable on their own: bringing one back would silently
// change a document's totals.
type binEntity struct {
	Table    string
	Label    string    // SQL over alias t: what the bin shows for a row
	Children [2]string // table, foreign key: rows deleted along with it (DeleteChildren)
	// RestoreCheck, when set, runs before a restore; a *Refusal answers 409.
	RestoreCheck func(ctx context.Context, tx pgx.Tx, id string) error
}

var binEntities = map[string]binEntity{
	"category":         {Table: "categories", Label: "t.name"},
	"brand":            {Table: "brands", Label: "t.name"},
	"tax_slab":         {Table: "tax_slabs", Label: "t.name"},
	"attribute_value":  {Table: "attribute_values", Label: "COALESCE((SELECT a.name FROM attributes a WHERE a.id = t.attribute_id), '') || ': ' || t.value"},
	"price_list":       {Table: "price_lists", Label: "t.name", Children: [2]string{"price_list_items", "price_list_id"}},
	"price_rule":       {Table: "price_rules", Label: "t.name"},
	"markup_rule":      {Table: "markup_rules", Label: "t.name"},
	"scheduled_change": {Table: "scheduled_price_changes", Label: "'Price change for ' || to_char(t.effective_at, 'DD Mon YYYY HH24:MI')"},
	"promotion":        {Table: "promotions", Label: "t.name"},
	"coupon":           {Table: "coupons", Label: "t.code"},
	"role":             {Table: "roles", Label: "t.name"},
	"report_template":  {Table: "report_templates", Label: "t.name"},
	"quotation":        {Table: "quotations", Label: "t.quote_number"},
	"journal_voucher":  {Table: "journal_vouchers", Label: "t.voucher_number"},
	"customer":         {Table: "customers", Label: "COALESCE(t.name, '') || COALESCE(' · ' || t.phone, '')"},
	"supplier":         {Table: "suppliers", Label: "t.name"},
	"branch":           {Table: "branches", Label: "t.name || ' (' || t.code || ')'"},
	"employee":         {Table: "users", Label: "t.name || COALESCE(' · ' || t.employee_code, '')"},
	"account":          {Table: "chart_of_accounts", Label: "t.code || ' ' || t.name"},
	"shift":            {Table: "shifts", Label: "t.name"},
	"attribute":        {Table: "attributes", Label: "t.name"},
	// Purchase drafts deleted by mistake (migrations/058).
	"purchase_order": {Table: "purchase_orders", Label: "t.po_number", Children: [2]string{"purchase_order_lines", "po_id"}},
	"grn": {Table: "goods_receipt_notes", Label: "t.grn_number", Children: [2]string{"goods_receipt_lines", "grn_id"},
		RestoreCheck: grnPOLive},
}

// grnPOLive: a GRN received against a purchase order comes back only
// while that PO is live; restore the PO first.
func grnPOLive(ctx context.Context, tx pgx.Tx, id string) error {
	var poDeleted bool
	err := db.IncludeDeleted(ctx, tx, func() error {
		return tx.QueryRow(ctx, `
			SELECT COALESCE(po.deleted_at IS NOT NULL, false)
			FROM goods_receipt_notes g LEFT JOIN purchase_orders po ON po.id = g.purchase_order_id
			WHERE g.id::text = $1`, id).Scan(&poDeleted)
	})
	if err != nil {
		return err
	}
	if poDeleted {
		return &Refusal{Code: "RESTORE_CONFLICT", Message: "this GRN's purchase order is deleted; restore the purchase order first"}
	}
	return nil
}

// binOrder fixes the listing order of the "all entities" view.
var binOrder = []string{"category", "brand", "tax_slab", "attribute_value", "price_list", "price_rule",
	"scheduled_change", "promotion", "coupon", "role", "report_template", "quotation", "journal_voucher",
	"customer", "supplier", "branch", "employee", "account", "shift", "attribute", "purchase_order", "grn"}

type Handler struct {
	DB *db.DB
}

type binItem struct {
	Entity        string  `json:"entity"`
	ID            string  `json:"id"`
	Label         string  `json:"label"`
	DeletedAt     string  `json:"deleted_at"`
	DeletedBy     *string `json:"deleted_by"`
	DeletedByName *string `json:"deleted_by_name"`
}

// List: GET /recycle-bin?entity=&limit= (recycle_bin.manage) — the most
// recently deleted records first, across every restorable entity unless
// one is named.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	only := r.URL.Query().Get("entity")
	if _, known := binEntities[only]; only != "" && !known {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "unknown entity: "+only)
		return
	}
	limit := 100
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 500 {
		limit = l
	}

	var parts []string
	for _, key := range binOrder {
		if only != "" && key != only {
			continue
		}
		e := binEntities[key]
		parts = append(parts, `SELECT '`+key+`' AS entity, t.id::text, (`+e.Label+`)::text, t.deleted_at, t.deleted_by
			FROM `+pgx.Identifier{e.Table}.Sanitize()+` t WHERE t.deleted_at IS NOT NULL`)
	}
	items := []binItem{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return db.IncludeDeleted(ctx, tx, func() error {
			rows, err := tx.Query(ctx, `
				SELECT b.entity, b.id, COALESCE(b.label, ''), b.deleted_at::text, b.deleted_by::text, u.name
				FROM (`+strings.Join(parts, " UNION ALL ")+`) b (entity, id, label, deleted_at, deleted_by)
				LEFT JOIN users u ON u.id = b.deleted_by
				ORDER BY b.deleted_at DESC
				LIMIT $1`, limit)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var it binItem
				if err := rows.Scan(&it.Entity, &it.ID, &it.Label, &it.DeletedAt, &it.DeletedBy, &it.DeletedByName); err != nil {
					return err
				}
				items = append(items, it)
			}
			return rows.Err()
		})
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the recycle bin")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items})
}

// RestoreHandler: POST /recycle-bin/{entity}/{id}/restore (recycle_bin.manage).
// Idempotent in effect: restoring an already-live record answers 404, and
// changes nothing.
func (h *Handler) RestoreHandler(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	e, known := binEntities[chi.URLParam(r, "entity")]
	if !known {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "this kind of record can't be restored")
		return
	}
	id := chi.URLParam(r, "id")
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if e.RestoreCheck != nil {
			if err := e.RestoreCheck(ctx, tx, id); err != nil {
				return err
			}
		}
		if e.Children[0] != "" {
			if err := restoreChildren(ctx, tx, e.Table, id, e.Children[0], e.Children[1]); err != nil {
				return err
			}
		}
		return Restore(ctx, tx, e.Table, id, claims.UserID)
	})
	var refusal *Refusal
	switch {
	case errors.As(err, &refusal):
		httpx.Error(w, http.StatusConflict, refusal.Code, refusal.Message)
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no deleted record with this id")
	case errors.Is(err, ErrConflict):
		httpx.Error(w, http.StatusConflict, "RESTORE_CONFLICT", "a live record already uses this name or code — rename it first")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not restore the record")
	default:
		if e.Table == "roles" || e.Table == "users" || e.Table == "branches" {
			authn.InvalidateScopes()
		}
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "restored"})
	}
}
