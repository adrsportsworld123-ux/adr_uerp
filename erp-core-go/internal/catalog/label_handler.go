package catalog

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/printing"
)

type LabelHandler struct {
	DB *db.DB
}

// PrintLabel: GET /products/variants/{id}/label?template=standard|compact
// — renders the variant's primary barcode as an ESC/POS print job (see
// internal/printing.BuildLabel). Requires a barcode already assigned via
// POST /products/variants/{id}/barcodes; there's deliberately no
// "generate one on the fly" fallback here — a label always reflects a
// real, already-committed barcode, never one invented just to print.
func (h *LabelHandler) PrintLabel(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	variantID := chi.URLParam(r, "id")
	template := r.URL.Query().Get("template")

	var data printing.LabelData
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT p.name, pv.sku, pv.mrp::text, pv.selling_price::text, COALESCE(b.code, ''), COALESCE(b.symbology, '')
			FROM product_variants pv
			JOIN products p ON p.id = pv.product_id
			LEFT JOIN barcodes b ON b.variant_id = pv.id AND b.is_primary = true
			WHERE pv.id = $1`, variantID).
			Scan(&data.ProductName, &data.SKU, &data.MRP, &data.SellingPrice, &data.BarcodeCode, &data.Symbology)
	})

	switch {
	case err == pgx.ErrNoRows:
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "variant not found")
		return
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load variant")
		return
	}
	if data.BarcodeCode == "" {
		httpx.Error(w, http.StatusConflict, "NO_BARCODE", "this variant has no barcode assigned yet — POST /products/variants/{id}/barcodes first")
		return
	}

	httpx.Binary(w, http.StatusOK, "application/vnd.escpos-raw", printing.BuildLabel(template, data))
}
