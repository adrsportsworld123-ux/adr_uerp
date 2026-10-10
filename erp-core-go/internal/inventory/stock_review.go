package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// Phase 13: the sync review queue (migrations/041_stock_review.sql). A sale
// made offline is never rejected at sync (§3.5), so stock can go negative;
// each such line opens a review item here, listed for the back office and
// the till that made the sale, and resolved with a note of what was done.

type reviewItem struct {
	ItemID       string  `json:"item_id"`
	Status       string  `json:"status"`
	Reason       string  `json:"reason"`
	BranchID     string  `json:"branch_id"`
	BranchName   string  `json:"branch_name"`
	VariantID    string  `json:"variant_id"`
	SKU          string  `json:"sku"`
	ProductName  string  `json:"product_name"`
	OrderID      string  `json:"sales_order_id"`
	OrderNumber  string  `json:"order_number"`
	TerminalName *string `json:"terminal_name"`
	Quantity     string  `json:"quantity"`
	OnHandAfter  string  `json:"on_hand_after"`
	OnHandNow    string  `json:"on_hand_now"`
	CreatedAt    string  `json:"created_at"`
	Resolution   *string `json:"resolution"`
	ResolvedBy   *string `json:"resolved_by"`
	ResolvedAt   *string `json:"resolved_at"`
}

var errAlreadyResolved = errors.New("already resolved")

const reviewSelect = `
	SELECT r.id, r.status, r.reason, r.branch_id, b.name, r.variant_id, pv.sku, p.name, r.sales_order_id, so.order_number,
	       t.name, r.quantity::text, r.on_hand_after::text,
	       COALESCE((SELECT on_hand FROM stock_levels sl WHERE sl.branch_id = r.branch_id AND sl.variant_id = r.variant_id), 0)::text,
	       r.created_at, r.resolution, ru.name, r.resolved_at
	FROM stock_review_items r
	JOIN branches b ON b.id = r.branch_id
	JOIN product_variants pv ON pv.id = r.variant_id
	JOIN products p ON p.id = pv.product_id
	JOIN sales_orders so ON so.id = r.sales_order_id
	LEFT JOIN pos_terminals t ON t.id = r.pos_terminal_id
	LEFT JOIN users ru ON ru.id = r.resolved_by`

func scanReview(row pgx.Row) (reviewItem, error) {
	var it reviewItem
	var created time.Time
	var resolved *time.Time
	if err := row.Scan(&it.ItemID, &it.Status, &it.Reason, &it.BranchID, &it.BranchName, &it.VariantID, &it.SKU, &it.ProductName,
		&it.OrderID, &it.OrderNumber, &it.TerminalName, &it.Quantity, &it.OnHandAfter, &it.OnHandNow, &created,
		&it.Resolution, &it.ResolvedBy, &resolved); err != nil {
		return it, err
	}
	it.CreatedAt = created.UTC().Format(time.RFC3339)
	if resolved != nil {
		s := resolved.UTC().Format(time.RFC3339)
		it.ResolvedAt = &s
	}
	return it, nil
}

// ListStockReview: GET /inventory/review?status=open|resolved|all&q=&branch_id=&terminal_id=&page=&limit=
// newest first, paged (default 25); `open_count` is the open total for the
// same branch/terminal filter whatever `status` is, for a badge.
func (h *Handler) ListStockReview(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	status := q.Get("status")
	if status == "all" { // the admin list kit's "everything"
		status = ""
	}
	if status != "" && status != "open" && status != "resolved" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be open or resolved")
		return
	}
	p := httpx.ParseListParams(r, map[string]string{"created": "r.created_at"}, "created")
	if q.Get("dir") == "" {
		p.OrderBy = "r.created_at DESC"
	}
	items := []reviewItem{}
	var total, open int
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		// q: product name, SKU or order number.
		where := ` WHERE ($1 = '' OR r.branch_id::text = $1) AND ($2 = '' OR r.pos_terminal_id::text = $2)
			AND ($3 = '' OR p.name ILIKE '%' || $3 || '%' OR pv.sku ILIKE '%' || $3 || '%' OR so.order_number ILIKE '%' || $3 || '%')`
		joins := ` JOIN product_variants pv ON pv.id = r.variant_id JOIN products p ON p.id = pv.product_id JOIN sales_orders so ON so.id = r.sales_order_id`
		args := []any{q.Get("branch_id"), q.Get("terminal_id"), strings.TrimSpace(q.Get("q"))}
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE r.status = 'open'), count(*) FILTER (WHERE $4 = '' OR r.status = $4)
			FROM stock_review_items r`+joins+where, append(args, status)...).Scan(&open, &total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, reviewSelect+where+` AND ($4 = '' OR r.status = $4)
			ORDER BY `+p.OrderBy+`, r.id LIMIT $5 OFFSET $6`, append(args, status, p.Limit, p.Offset)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			it, err := scanReview(rows)
			if err != nil {
				return err
			}
			items = append(items, it)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list review items")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "open_count": open, "page": p.Page, "limit": p.Limit})
}

// ResolveStockReview: POST /inventory/review/{id}/resolve {resolution} —
// inventory.adjust. The note says what was done (counted and adjusted,
// a mis-scan, stock found in the back room ...); the item stays as a
// record. Fixing the stock itself is a normal adjustment or count.
func (h *Handler) ResolveStockReview(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		Resolution string `json:"resolution"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Resolution) == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "say what was done (resolution)")
		return
	}
	id := chi.URLParam(r, "id")
	var it reviewItem
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM stock_review_items WHERE id::text = $1 FOR UPDATE`, id).Scan(&status); err != nil {
			return err
		}
		if status != "open" {
			return errAlreadyResolved
		}
		if _, err := tx.Exec(ctx, `
			UPDATE stock_review_items SET status = 'resolved', resolution = $1, resolved_by = $2, resolved_at = now()
			WHERE id::text = $3`, strings.TrimSpace(req.Resolution), claims.UserID, id); err != nil {
			return err
		}
		var err error
		if it, err = scanReview(tx.QueryRow(ctx, reviewSelect+` WHERE r.id::text = $1`, id)); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{
			EntityType: "stock_review_items", EntityID: it.ItemID, Action: "update", PerformedBy: claims.UserID,
			Before: map[string]any{"status": "open"}, After: map[string]any{"status": "resolved", "resolution": it.Resolution},
			Reason: "NEGATIVE_STOCK review resolved",
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "review item not found")
	case errors.Is(err, errAlreadyResolved):
		httpx.Error(w, http.StatusConflict, "ALREADY_RESOLVED", "this item was already resolved")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not resolve the review item")
	default:
		httpx.JSON(w, http.StatusOK, it)
	}
}
