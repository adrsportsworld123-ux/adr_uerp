package catalog

import (
	"context"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/taxcalc"
)

type ListHandler struct {
	DB *db.DB
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
}

type productSummary struct {
	ProductID string `json:"product_id"`
	Name      string `json:"name"`
	HSNCode   string `json:"hsn_code"`
	// migrations/033 — whether every variant's selling_price includes tax,
	// and the product's total tax rate (0 when it has no tax slab).
	PriceIncludesTax bool             `json:"price_includes_tax"`
	TaxRatePct       float64          `json:"tax_rate_pct"`
	Variants         []variantSummary `json:"variants"`
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
	collectionID := q.Get("collection_id")
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
			SELECT p.id, p.name, COALESCE(p.hsn_code,''), p.price_includes_tax,
			       COALESCE(ts.cgst_rate,0) + COALESCE(ts.sgst_rate,0) + COALESCE(ts.igst_rate,0) + COALESCE(ts.cess_rate,0)
			FROM products p
			LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
			WHERE ($1 = '' OR p.category_id::text = $1)
			  AND ($2 = '' OR p.brand_id::text = $2)
			  AND ($3 = '' OR p.name ILIKE '%' || $3 || '%')
			  AND ($6 = '' OR p.collection_id::text = $6)
			  AND p.status = 'active'
			ORDER BY p.name
			LIMIT $4 OFFSET $5`, categoryID, brandID, search, limit, offset, collectionID)
		if err != nil {
			return err
		}
		type row struct {
			id, name, hsn    string
			priceIncludesTax bool
			taxRatePct       float64
		}
		var productRows []row
		for rows.Next() {
			var rrow row
			if err := rows.Scan(&rrow.id, &rrow.name, &rrow.hsn, &rrow.priceIncludesTax, &rrow.taxRatePct); err != nil {
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
				       pv.track_batch
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
				if err := variantRows.Scan(&v.VariantID, &v.SKU, &v.CostPrice, &v.MRP, &v.SellingPrice, &costPrice, &sellingPrice, &v.OriginalBarcode, &v.GeneratedBarcode, &v.TrackBatch); err != nil {
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
				ProductID: pr.id, Name: pr.name, HSNCode: pr.hsn,
				PriceIncludesTax: pr.priceIncludesTax, TaxRatePct: pr.taxRatePct, Variants: variants,
			})
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
