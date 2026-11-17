package catalog

import (
	"context"
	"errors"
	"math"
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

var errScalePriceUnknown = errors.New("price label for a product with no selling price")

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
	// EmbeddedPrice: a price-embedded scale label (migrations/084): the
	// price printed on it. ComputedWeightKg is then that price over the
	// unit price, to 3 decimals, so the line comes to the label's price
	// (within the rounding of the third decimal).
	EmbeddedPrice *string `json:"embedded_price,omitempty"`
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
	// migrations/083: a code shared by several colours of one product, or
	// a variant with MRP lots. The fields above are the option to use;
	// ChoiceRequired = the cashier picks from Options (every colour; or
	// the MRP lots in stock when two or more are). Options is left out
	// for an ordinary code with one variant.
	ChoiceRequired bool         `json:"choice_required"`
	Options        []ScanOption `json:"options,omitempty"`
}

// ServeHTTP is the ≤6-second POS flow's first hop: scan a barcode, get back
// everything needed to add a line to the cart in one round trip.
// ?branch_id= (the till's) lets an MRP lot in stock be chosen for it;
// ?purpose=receive (a goods receipt) offers the variants, never their lots. Query
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
	// interpretation first is the safer order regardless. The merchant's
	// own formats (migrations/084) are read inside the transaction.
	var resp barcodeResponse
	var u unitCols
	var sellable bool
	var opEnabled bool
	var opMin, opMax, opAbove *string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		formats, _, err := loadScaleFormats(ctx, tx)
		if err != nil {
			return err
		}
		pluCode, scaleValue, format, isWeighed := decodeScale(code, formats)
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
			weightKg := scaleValue
			if format.ValueKind == "price" {
				unit, _ := strconv.ParseFloat(resp.SellingPrice, 64)
				if unit <= 0 {
					return errScalePriceUnknown
				}
				price := strconv.FormatFloat(scaleValue, 'f', 2, 64)
				resp.EmbeddedPrice = &price
				weightKg = math.Round(scaleValue/unit*1000) / 1000
			}
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
			WHERE b.code = $1
			ORDER BY b.is_primary DESC, b.created_at, b.id
			LIMIT 1`, code)
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
		ch, err := scanOptions(ctx, tx, code, r.URL.Query().Get("branch_id"), r.URL.Query().Get("purpose") == "receive")
		if err != nil || len(ch.Options) == 0 {
			return err // nothing sellable: the variant's own row answers (inactive)
		}
		if ch.Chosen.VariantID != resp.VariantID {
			// Same product, so tax, unit and pack hold; the variant's own
			// SKU and prices (an MRP lot keeps its parent's open-price range).
			resp.VariantID, resp.SKU, resp.MRP, resp.SellingPrice = ch.Chosen.VariantID, ch.Chosen.SKU, ch.Chosen.MRP, ch.Chosen.SellingPrice
			sellable = true
		}
		if len(ch.Options) > 1 {
			resp.Options, resp.ChoiceRequired = ch.Options, ch.ChoiceRequired
		}
		return nil
	})

	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusNotFound, "PRODUCT_NOT_FOUND", "no product matches this barcode")
		return
	}
	if err == errScalePriceUnknown {
		httpx.Error(w, http.StatusConflict, "PRICE_REQUIRED", "this price label's product has no selling price to work out the quantity from")
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
