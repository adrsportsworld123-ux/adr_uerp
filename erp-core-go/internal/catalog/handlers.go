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
	// PriceIncludesTax: SellingPrice already contains tax (migrations/033)
	// — the POS shows it as-is either way; the cart computes the split.
	PriceIncludesTax bool `json:"price_includes_tax"`
	// Phase 8 (Grocery/FMCG): set only when `code` decoded as a
	// weighing-scale barcode — SellingPrice above is this variant's
	// PER-KILOGRAM catalog price; the POS client adds this exact weight
	// as the cart line's quantity, not 1, so the line prices correctly
	// for what was actually weighed.
	ComputedWeightKg *string `json:"computed_weight_kg,omitempty"`
	// Phase 13: non-nil for an open-price variant — the cashier enters
	// the price; SellingPrice is only the suggestion.
	OpenPrice *OpenPrice `json:"open_price"`
	// Units (migrations/076): what the product is counted in, and what one
	// scan of this barcode adds — PackQty base units ("1.000" for an
	// ordinary barcode; 30 for a tray of 30; the weight for a scale code).
	// SellingPrice stays the price of one base unit.
	Unit      *UnitRef `json:"unit"`
	PackQty   string   `json:"pack_qty"`
	PackLabel string   `json:"pack_label,omitempty"`
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
	var u unitCols
	var sellable bool
	var opEnabled bool
	var opMin, opMax, opAbove *string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if isWeighed {
			row := tx.QueryRow(ctx, `
				SELECT p.id, p.name, COALESCE(p.hsn_code, ''),
				       pv.id, pv.sku, pv.selling_price::text, pv.mrp::text,
				       COALESCE(ts.cgst_rate,0), COALESCE(ts.sgst_rate,0),
				       COALESCE(ts.igst_rate,0), COALESCE(ts.cess_rate,0), p.price_includes_tax,
				       `+SellablePredicate+`, `+OpenPriceColumns+`, `+unitColumns+`
				FROM product_variants pv
				JOIN products p ON p.id = pv.product_id
				LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
				LEFT JOIN units u ON u.id = p.unit_id
				WHERE pv.plu_code = $1`, pluCode)
			if err := row.Scan(append([]any{
				&resp.ProductID, &resp.ProductName, &resp.HSNCode,
				&resp.VariantID, &resp.SKU, &resp.SellingPrice, &resp.MRP,
				&resp.CGSTRate, &resp.SGSTRate, &resp.IGSTRate, &resp.CessRate, &resp.PriceIncludesTax, &sellable,
				&opEnabled, &opMin, &opMax, &opAbove,
			}, u.dest()...)...); err != nil {
				return err
			}
			resp.OpenPrice = NewOpenPrice(opEnabled, opMin, opMax, opAbove)
			weightStr := strconv.FormatFloat(weightKg, 'f', 3, 64)
			resp.ComputedWeightKg = &weightStr
			resp.Unit, resp.PackQty = u.ref(), weightStr
			return nil
		}

		row := tx.QueryRow(ctx, `
			SELECT p.id, p.name, COALESCE(p.hsn_code, ''),
			       pv.id, pv.sku, pv.selling_price::text, pv.mrp::text,
			       COALESCE(ts.cgst_rate,0), COALESCE(ts.sgst_rate,0),
			       COALESCE(ts.igst_rate,0), COALESCE(ts.cess_rate,0), p.price_includes_tax,
			       `+SellablePredicate+`, `+OpenPriceColumns+`, `+unitColumns+`,
			       b.pack_qty::text, `+packLabelSQL+`
			FROM barcodes b
			JOIN product_variants pv ON pv.id = b.variant_id
			JOIN products p ON p.id = pv.product_id
			LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
			LEFT JOIN units u ON u.id = p.unit_id
			LEFT JOIN units bu ON bu.id = b.pack_unit_id
			WHERE b.code = $1`, code)
		var label *string
		if err := row.Scan(append(append([]any{
			&resp.ProductID, &resp.ProductName, &resp.HSNCode,
			&resp.VariantID, &resp.SKU, &resp.SellingPrice, &resp.MRP,
			&resp.CGSTRate, &resp.SGSTRate, &resp.IGSTRate, &resp.CessRate, &resp.PriceIncludesTax, &sellable,
			&opEnabled, &opMin, &opMax, &opAbove,
		}, u.dest()...), &resp.PackQty, &label)...); err != nil {
			return err
		}
		resp.OpenPrice = NewOpenPrice(opEnabled, opMin, opMax, opAbove)
		resp.Unit = u.ref()
		if label != nil {
			resp.PackLabel = *label
		}
		return nil
	})

	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusNotFound, "PRODUCT_NOT_FOUND", "no product matches this barcode")
		return
	}
	// Found, but inactive/discontinued: say so, rather than a confusing
	// "not found" — the cashier is holding the item.
	if err == nil && !sellable {
		httpx.Error(w, http.StatusConflict, "PRODUCT_INACTIVE", resp.ProductName+" ("+resp.SKU+") is inactive or discontinued and can't be sold")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "barcode lookup failed")
		return
	}

	httpx.JSON(w, http.StatusOK, resp)
}
