package catalog

import (
	"context"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type ListHandler struct {
	DB *db.DB
}

type variantSummary struct {
	VariantID    string   `json:"variant_id"`
	SKU          string   `json:"sku"`
	CostPrice    string   `json:"cost_price"`
	MRP          string   `json:"mrp"`
	SellingPrice string   `json:"selling_price"`
	MarginPct    *float64 `json:"margin_pct"`
	MarkupPct    *float64 `json:"markup_pct"`
}

type productSummary struct {
	ProductID string           `json:"product_id"`
	Name      string           `json:"name"`
	HSNCode   string           `json:"hsn_code"`
	Variants  []variantSummary `json:"variants"`
}

// ListProducts: GET /products?category_id=&brand_id=&q=&min_price=&max_price=&page=&limit=
// The catalog browse endpoint phase0_1_design.md §3.2 documented from the
// start but never built (Phase 1 only ever needed the barcode-scan path).
// Pricing Management needs it now — you can't bulk-reprice or browse
// margins on a catalog you can't list. Returns each product with its
// variants' pricing and computed margin/markup, since that's the pricing
// screen's core view.
func (h *ListHandler) ListProducts(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	categoryID := q.Get("category_id")
	brandID := q.Get("brand_id")
	search := q.Get("q")
	minPrice := q.Get("min_price")
	maxPrice := q.Get("max_price")

	limit := 50
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 && l <= 200 {
		limit = l
	}
	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}
	offset := (page - 1) * limit

	products := []productSummary{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT p.id, p.name, COALESCE(p.hsn_code,'')
			FROM products p
			WHERE ($1 = '' OR p.category_id::text = $1)
			  AND ($2 = '' OR p.brand_id::text = $2)
			  AND ($3 = '' OR p.name ILIKE '%' || $3 || '%')
			  AND p.status = 'active'
			ORDER BY p.name
			LIMIT $4 OFFSET $5`, categoryID, brandID, search, limit, offset)
		if err != nil {
			return err
		}
		type row struct{ id, name, hsn string }
		var productRows []row
		for rows.Next() {
			var rrow row
			if err := rows.Scan(&rrow.id, &rrow.name, &rrow.hsn); err != nil {
				rows.Close()
				return err
			}
			productRows = append(productRows, rrow)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, pr := range productRows {
			variantRows, err := tx.Query(ctx, `
				SELECT id, sku, cost_price::text, mrp::text, selling_price::text, cost_price, selling_price
				FROM product_variants
				WHERE product_id = $1
				  AND ($2 = '' OR selling_price >= $2::numeric)
				  AND ($3 = '' OR selling_price <= $3::numeric)
				ORDER BY sku`, pr.id, minPrice, maxPrice)
			if err != nil {
				return err
			}
			var variants []variantSummary
			for variantRows.Next() {
				var v variantSummary
				var costPrice, sellingPrice float64
				if err := variantRows.Scan(&v.VariantID, &v.SKU, &v.CostPrice, &v.MRP, &v.SellingPrice, &costPrice, &sellingPrice); err != nil {
					variantRows.Close()
					return err
				}
				if sellingPrice > 0 {
					margin := round2((sellingPrice - costPrice) / sellingPrice * 100)
					v.MarginPct = &margin
				}
				if costPrice > 0 {
					markup := round2((sellingPrice - costPrice) / costPrice * 100)
					v.MarkupPct = &markup
				}
				variants = append(variants, v)
			}
			variantRows.Close()
			if err := variantRows.Err(); err != nil {
				return err
			}
			if minPrice != "" || maxPrice != "" {
				if len(variants) == 0 {
					continue // no variant in this product matched the price filter
				}
			}
			products = append(products, productSummary{ProductID: pr.id, Name: pr.name, HSNCode: pr.hsn, Variants: variants})
		}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list products")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"products": products, "page": page, "limit": limit})
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
