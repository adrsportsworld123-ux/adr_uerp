package catalog

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/search"
	"erp-core-go/internal/taxcalc"
)

type ListHandler struct {
	DB *db.DB
	// Search is optional (nil or disabled is fine): product edits
	// reindex their variants so search doesn't show a stale name/status.
	Search *search.Client
}

type variantSummary struct {
	VariantID    string `json:"variant_id"`
	SKU          string `json:"sku"`
	CostPrice    string `json:"cost_price"`
	MRP          string `json:"mrp"`
	SellingPrice string `json:"selling_price"`
	// NetSellingPrice is SellingPrice with tax removed — equal to it for a
	// tax-exclusive product. Margin/markup are computed on this, never on
	// a tax-inclusive price (cost_price is always tax-exclusive).
	NetSellingPrice string `json:"net_selling_price"`
	// GrossSellingPrice is what the customer pays for one unit, tax
	// included — equal to SellingPrice for a tax-inclusive product.
	GrossSellingPrice string   `json:"gross_selling_price"`
	MarginPct         *float64 `json:"margin_pct"`
	MarkupPct         *float64 `json:"markup_pct"`
	// OriginalBarcode/GeneratedBarcode are this variant's two possible
	// barcodes (internal/catalog/barcode_assign.go,
	// migrations/032_barcode_source.sql): the real one already on the
	// product (manually entered) and this system's own auto-generated
	// one, which coexist rather than one replacing the other. Either or
	// both can be nil — lets the product list (erp-web-admin's Pricing
	// page) show and assign both without a second per-variant round trip.
	OriginalBarcode  *string `json:"original_barcode"`
	GeneratedBarcode *string `json:"generated_barcode"`
	// TrackBatch (Phase 8) lets stock screens show batch/expiry fields
	// only for the variants whose adjustments, counts and returns need
	// them (internal/inventory/batches.go's ErrBatchRequired).
	TrackBatch bool `json:"track_batch"`
	// Status of the variant itself (a single size can be discontinued
	// while the product stays on sale).
	Status string `json:"status"`
}

type productSummary struct {
	ProductID string `json:"product_id"`
	Name      string `json:"name"`
	HSNCode   string `json:"hsn_code"`
	Status    string `json:"status"` // active | inactive | discontinued
	// migrations/033 — whether every variant's selling_price includes tax,
	// and the product's total tax rate (0 when it has no tax slab).
	PriceIncludesTax bool             `json:"price_includes_tax"`
	TaxRatePct       float64          `json:"tax_rate_pct"`
	Variants         []variantSummary `json:"variants"`
	// Phase 11 list columns (additive): names for display, ids for links.
	CategoryID   *string `json:"category_id"`
	CategoryName *string `json:"category_name"`
	BrandID      *string `json:"brand_id"`
	BrandName    *string `json:"brand_name"`
}

// ListProducts: GET /products?category_id=&brand_id=&q=&min_price=&max_price=&page=&limit=&status=&sort=&dir=
// q matches the product name, any variant's SKU, or any of its barcodes.
// sort: name (default) | status | created | updated. The response carries
// `total` (Phase 11 list convention, httpx.ListParams) so tables can page
// on the server; it counts the same filtered set the page is cut from.
// status defaults to "active" — every picker (POS, quotations, pricing)
// only offers sellable products — and takes "all" or a specific status
// for the product admin screen, which must be able to find an inactive
// product to reactivate it.
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
	collectionID := q.Get("collection_id")
	search := q.Get("q")
	minPrice := q.Get("min_price")
	maxPrice := q.Get("max_price")
	status := q.Get("status")
	switch status {
	case "":
		status = "active"
	case "all", "active", "inactive", "discontinued":
	default:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be all, active, inactive, or discontinued")
		return
	}

	limit := 50
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 && l <= 200 {
		limit = l
	}
	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}
	offset := (page - 1) * limit
	orderBy := map[string]string{"name": "p.name", "status": "p.status", "created": "p.created_at", "updated": "p.updated_at"}[q.Get("sort")]
	if orderBy == "" {
		orderBy = "p.name"
	}
	if strings.EqualFold(q.Get("dir"), "desc") {
		orderBy += " DESC"
	}

	products := []productSummary{}
	total := 0
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		// One WHERE for both the count and the page, so "of N" matches what
		// paging walks through. The price filter lives here too (a product
		// matches when any variant is in range); filtering after LIMIT, as
		// this did before Phase 11, made pages come back short.
		where := `
			FROM products p
			LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
			LEFT JOIN categories c ON c.id = p.category_id
			LEFT JOIN brands b ON b.id = p.brand_id
			WHERE ($1 = '' OR p.category_id IN (` + db.CategorySubtreeSQL + `))
			  AND ($2 = '' OR p.brand_id::text = $2)
			  AND ($3 = '' OR p.name ILIKE '%' || $3 || '%'
			       OR EXISTS (SELECT 1 FROM product_variants sv WHERE sv.product_id = p.id AND sv.sku ILIKE '%' || $3 || '%')
			       OR EXISTS (SELECT 1 FROM barcodes bc JOIN product_variants bv ON bv.id = bc.variant_id
			                  WHERE bv.product_id = p.id AND bc.code = $3))
			  AND ($4 = '' OR p.collection_id::text = $4)
			  AND ($5 = 'all' OR p.status = $5)
			  AND ($6 = '' AND $7 = '' OR EXISTS (
			        SELECT 1 FROM product_variants pv WHERE pv.product_id = p.id
			          AND ($6 = '' OR pv.selling_price >= $6::numeric)
			          AND ($7 = '' OR pv.selling_price <= $7::numeric)))`
		args := []any{categoryID, brandID, search, collectionID, status, minPrice, maxPrice}
		if err := tx.QueryRow(ctx, `SELECT count(*)`+where, args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT p.id, p.name, COALESCE(p.hsn_code,''), p.status, p.price_includes_tax,
			       COALESCE(ts.cgst_rate,0) + COALESCE(ts.sgst_rate,0) + COALESCE(ts.igst_rate,0) + COALESCE(ts.cess_rate,0),
			       p.category_id::text, c.name, p.brand_id::text, b.name`+where+`
			ORDER BY `+orderBy+`, p.id
			LIMIT $8 OFFSET $9`, append(args, limit, offset)...)
		if err != nil {
			return err
		}
		type row struct {
			id, name, hsn    string
			status           string
			priceIncludesTax bool
			taxRatePct       float64
			categoryID       *string
			categoryName     *string
			brandID          *string
			brandName        *string
		}
		var productRows []row
		for rows.Next() {
			var rrow row
			if err := rows.Scan(&rrow.id, &rrow.name, &rrow.hsn, &rrow.status, &rrow.priceIncludesTax, &rrow.taxRatePct,
				&rrow.categoryID, &rrow.categoryName, &rrow.brandID, &rrow.brandName); err != nil {
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
				SELECT pv.id, pv.sku, pv.cost_price::text, pv.mrp::text, pv.selling_price::text, pv.cost_price, pv.selling_price,
				       (SELECT code FROM barcodes WHERE variant_id = pv.id AND source = 'original' ORDER BY created_at LIMIT 1),
				       (SELECT code FROM barcodes WHERE variant_id = pv.id AND source = 'generated' ORDER BY created_at LIMIT 1),
				       pv.track_batch, pv.status
				FROM product_variants pv
				WHERE pv.product_id = $1
				  AND ($2 = '' OR pv.selling_price >= $2::numeric)
				  AND ($3 = '' OR pv.selling_price <= $3::numeric)
				ORDER BY pv.sku`, pr.id, minPrice, maxPrice)
			if err != nil {
				return err
			}
			var variants []variantSummary
			for variantRows.Next() {
				var v variantSummary
				var costPrice, sellingPrice float64
				if err := variantRows.Scan(&v.VariantID, &v.SKU, &v.CostPrice, &v.MRP, &v.SellingPrice, &costPrice, &sellingPrice, &v.OriginalBarcode, &v.GeneratedBarcode, &v.TrackBatch, &v.Status); err != nil {
					variantRows.Close()
					return err
				}
				netPrice := taxcalc.NetPrice(sellingPrice, pr.taxRatePct, pr.priceIncludesTax)
				v.NetSellingPrice = strconv.FormatFloat(netPrice, 'f', 2, 64)
				v.GrossSellingPrice = strconv.FormatFloat(taxcalc.GrossPrice(sellingPrice, pr.taxRatePct, pr.priceIncludesTax), 'f', 2, 64)
				if netPrice > 0 {
					margin := round2((netPrice - costPrice) / netPrice * 100)
					v.MarginPct = &margin
				}
				if costPrice > 0 {
					markup := round2((netPrice - costPrice) / costPrice * 100)
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
			products = append(products, productSummary{
				ProductID: pr.id, Name: pr.name, HSNCode: pr.hsn, Status: pr.status,
				PriceIncludesTax: pr.priceIncludesTax, TaxRatePct: pr.taxRatePct, Variants: variants,
				CategoryID: pr.categoryID, CategoryName: pr.categoryName, BrandID: pr.brandID, BrandName: pr.brandName,
			})
		}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list products")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"products": products, "page": page, "limit": limit, "total": total})
}

// round2 rounds half away from zero. (int64(v*100+0.5) truncated
// negatives toward zero: -10.00 became -9.99.)
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
