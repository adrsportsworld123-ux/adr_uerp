package catalog

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type BarcodeHandler struct {
	DB *db.DB
}

type barcodeResponse struct {
	ProductID    string  `json:"product_id"`
	ProductName  string  `json:"product_name"`
	HSNCode      string  `json:"hsn_code"`
	VariantID    string  `json:"variant_id"`
	SKU          string  `json:"sku"`
	SellingPrice string  `json:"selling_price"`
	MRP          string  `json:"mrp"`
	CGSTRate     float64 `json:"cgst_rate"`
	SGSTRate     float64 `json:"sgst_rate"`
	IGSTRate     float64 `json:"igst_rate"`
	CessRate     float64 `json:"cess_rate"`
	// Phase 8 (Grocery/FMCG): set only when `code` decoded as a
	// weighing-scale barcode — SellingPrice above is this variant's
	// PER-KILOGRAM catalog price; the POS client adds this exact weight
	// as the cart line's quantity, not 1, so the line prices correctly
	// for what was actually weighed.
	ComputedWeightKg *string `json:"computed_weight_kg,omitempty"`
}

// ServeHTTP is the ≤6-second POS flow's first hop: scan a barcode, get back
// everything needed to add a line to the cart in one round trip. Query
// verified directly against a seeded Postgres instance during design — see
// phase0_1_design.md and the verification notes accompanying this handler.
func (h *BarcodeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}

	code := chi.URLParam(r, "code")
	if code == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "barcode is required")
		return
	}

	// Weighing-scale barcode (weighing_scale.go) takes priority: a real
	// registered barcode is never expected to also satisfy this format's
	// check-digit convention by coincidence, but trying the more specific
	// interpretation first is the safer order regardless.
	pluCode, weightKg, isWeighed := decodeWeighingScaleBarcode(code)

	var resp barcodeResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if isWeighed {
			row := tx.QueryRow(ctx, `
				SELECT p.id, p.name, COALESCE(p.hsn_code, ''),
				       pv.id, pv.sku, pv.selling_price::text, pv.mrp::text,
				       COALESCE(ts.cgst_rate,0), COALESCE(ts.sgst_rate,0),
				       COALESCE(ts.igst_rate,0), COALESCE(ts.cess_rate,0)
				FROM product_variants pv
				JOIN products p ON p.id = pv.product_id
				LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
				WHERE pv.plu_code = $1`, pluCode)
			if err := row.Scan(
				&resp.ProductID, &resp.ProductName, &resp.HSNCode,
				&resp.VariantID, &resp.SKU, &resp.SellingPrice, &resp.MRP,
				&resp.CGSTRate, &resp.SGSTRate, &resp.IGSTRate, &resp.CessRate,
			); err != nil {
				return err
			}
			weightStr := strconv.FormatFloat(weightKg, 'f', 3, 64)
			resp.ComputedWeightKg = &weightStr
			return nil
		}

		row := tx.QueryRow(ctx, `
			SELECT p.id, p.name, COALESCE(p.hsn_code, ''),
			       pv.id, pv.sku, pv.selling_price::text, pv.mrp::text,
			       COALESCE(ts.cgst_rate,0), COALESCE(ts.sgst_rate,0),
			       COALESCE(ts.igst_rate,0), COALESCE(ts.cess_rate,0)
			FROM barcodes b
			JOIN product_variants pv ON pv.id = b.variant_id
			JOIN products p ON p.id = pv.product_id
			LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
			WHERE b.code = $1`, code)
		return row.Scan(
			&resp.ProductID, &resp.ProductName, &resp.HSNCode,
			&resp.VariantID, &resp.SKU, &resp.SellingPrice, &resp.MRP,
			&resp.CGSTRate, &resp.SGSTRate, &resp.IGSTRate, &resp.CessRate,
		)
	})

	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusNotFound, "PRODUCT_NOT_FOUND", "no product matches this barcode")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "barcode lookup failed")
		return
	}

	httpx.JSON(w, http.StatusOK, resp)
}
