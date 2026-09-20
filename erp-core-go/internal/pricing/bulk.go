package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

var errInvalidMethod = errors.New("method must be one of percent, fixed, set")

// FRD §11 "Bulk Updates" names a Preview → Approval → Apply flow; this
// implements Preview → Apply, skipping a separate formal approval step —
// the same "skip the formal workflow for now" simplification already
// applied to Purchase's PO stage and Accounting's manual journal entries.
// Preview and Apply share the exact same matching/computation logic
// (computeBulkChanges) so what you previewed is guaranteed to be what
// Apply does — no drift between the two code paths.

type bulkFilter struct {
	CategoryID string  `json:"category_id"`
	BrandID    string  `json:"brand_id"`
	MinPrice   float64 `json:"min_price"`
	MaxPrice   float64 `json:"max_price"`
}

type bulkUpdateRequest struct {
	Filter   bulkFilter `json:"filter"`
	Method   string     `json:"method"` // "percent" | "fixed" | "set"
	Value    float64    `json:"value"`
	RoundTo  float64    `json:"round_to"` // 0 = no rounding; else nearest ₹1/5/10/50/100 etc.
	Reason   string     `json:"reason"`
	Override bool       `json:"override"` // allow variants whose new price would be below cost
}

type bulkItemResult struct {
	VariantID   string  `json:"variant_id"`
	SKU         string  `json:"sku"`
	ProductName string  `json:"product_name"`
	OldPrice    float64 `json:"old_price"`
	NewPrice    float64 `json:"new_price"`
	Status      string  `json:"status"` // "would_update" | "updated" | "skipped_negative_margin"
}

func validateBulkRequest(req bulkUpdateRequest) error {
	switch req.Method {
	case "percent", "fixed", "set":
	default:
		return errInvalidMethod
	}
	return nil
}

func computeNewPrice(method string, value, roundTo, oldPrice float64) float64 {
	var newPrice float64
	switch method {
	case "percent":
		newPrice = oldPrice * (1 + value/100)
	case "fixed":
		newPrice = oldPrice + value
	case "set":
		newPrice = value
	}
	if roundTo > 0 {
		newPrice = float64(int64(newPrice/roundTo+0.5)) * roundTo
	}
	return round2(newPrice)
}

// computeBulkChanges finds every variant matching the filter and computes
// what its new selling_price would be. When apply is true, it also writes
// the change (product_variants + price_history) for every non-skipped
// item, inside the caller's transaction.
func computeBulkChanges(ctx context.Context, tx pgx.Tx, req bulkUpdateRequest, apply bool, changedBy string) ([]bulkItemResult, error) {
	rows, err := tx.Query(ctx, `
		SELECT pv.id, pv.sku, p.name, pv.selling_price, pv.cost_price
		FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE ($1 = '' OR p.category_id::text = $1)
		  AND ($2 = '' OR p.brand_id::text = $2)
		  AND ($3 = 0 OR pv.selling_price >= $3)
		  AND ($4 = 0 OR pv.selling_price <= $4)
		ORDER BY p.name, pv.sku`,
		req.Filter.CategoryID, req.Filter.BrandID, req.Filter.MinPrice, req.Filter.MaxPrice)
	if err != nil {
		return nil, err
	}
	type row struct {
		id, sku, name      string
		sellingPrice, cost float64
	}
	var matched []row
	for rows.Next() {
		var rrow row
		if err := rows.Scan(&rrow.id, &rrow.sku, &rrow.name, &rrow.sellingPrice, &rrow.cost); err != nil {
			rows.Close()
			return nil, err
		}
		matched = append(matched, rrow)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	results := make([]bulkItemResult, 0, len(matched))
	for _, m := range matched {
		newPrice := computeNewPrice(req.Method, req.Value, req.RoundTo, m.sellingPrice)
		item := bulkItemResult{VariantID: m.id, SKU: m.sku, ProductName: m.name, OldPrice: m.sellingPrice, NewPrice: newPrice}

		if newPrice < m.cost && !req.Override {
			item.Status = "skipped_negative_margin"
			results = append(results, item)
			continue
		}

		if apply {
			if _, err := tx.Exec(ctx, `UPDATE product_variants SET selling_price = $1, updated_at = now() WHERE id = $2`, newPrice, m.id); err != nil {
				return nil, err
			}
			if err := recordPriceChange(ctx, tx, m.id, "selling_price", m.sellingPrice, newPrice, req.Reason, changedBy); err != nil {
				return nil, err
			}
			item.Status = "updated"
		} else {
			item.Status = "would_update"
		}
		results = append(results, item)
	}
	return results, nil
}

func (h *Handler) PreviewBulkUpdate(w http.ResponseWriter, r *http.Request) {
	h.bulkUpdate(w, r, false)
}

func (h *Handler) ApplyBulkUpdate(w http.ResponseWriter, r *http.Request) {
	h.bulkUpdate(w, r, true)
}

func (h *Handler) bulkUpdate(w http.ResponseWriter, r *http.Request, apply bool) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req bulkUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if err := validateBulkRequest(req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	var results []bulkItemResult
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		results, err = computeBulkChanges(ctx, tx, req, apply, claims.UserID)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not compute bulk price update")
		return
	}

	skipped := 0
	for _, item := range results {
		if item.Status == "skipped_negative_margin" {
			skipped++
		}
		if apply && item.Status == "updated" {
			h.reindex(r.Context(), claims.TenantID, item.VariantID)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"items": results, "total_matched": len(results), "skipped_negative_margin": skipped,
	})
}
