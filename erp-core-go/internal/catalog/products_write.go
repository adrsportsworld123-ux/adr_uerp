// Phase 1's Product & Catalog Management — the product-creation gap
// itself. See migrations/019_catalog_management.sql's header comment for
// the full story of why this was missing and what it unblocks. Lives in
// this same package/Handler as ListProducts (products.go) — same
// resource, same conventions, just the write half nothing had built yet.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type createVariantInput struct {
	SKU            string         `json:"sku"`
	AttributeCombo map[string]any `json:"attribute_combo"`
	CostPrice      float64        `json:"cost_price"`
	MRP            float64        `json:"mrp"`
	SellingPrice   float64        `json:"selling_price"`
	TrackSerial    bool           `json:"track_serial"`
	TrackBatch     bool           `json:"track_batch"` // Phase 8 (Grocery/FMCG) — opts this variant into batch/lot + expiry tracking
	PLUCode        string         `json:"plu_code"`    // Phase 8 (Grocery/FMCG) — weighing-scale barcode short code, optional
	// OriginalBarcode/GenerateBarcode let a caller provision either or
	// both of this variant's barcodes right at creation time, instead of
	// a separate POST .../barcodes call afterward — see barcode_assign.go's
	// assignBarcodeInTx. Both can be set together: the real barcode
	// already on the product (OriginalBarcode) and this system's own
	// internal one (GenerateBarcode) coexist rather than one replacing
	// the other.
	OriginalBarcode string `json:"original_barcode"`
	GenerateBarcode bool   `json:"generate_barcode"`
}

type createProductRequest struct {
	Name             string `json:"name"`
	ShortDescription string `json:"short_description"`
	HSNCode          string `json:"hsn_code"`
	CategoryID       string `json:"category_id"`
	BrandID          string `json:"brand_id"`
	TaxSlabID        string `json:"tax_slab_id"`
	CollectionID     string `json:"collection_id"` // Phase 8 (Apparel) — optional, "" = no collection
	ProductType      string `json:"product_type"`  // "simple" | "variant" | "composite"; defaults to "simple"
	// PriceIncludesTax: whether the variants' selling_price/mrp already
	// include tax (migrations/033). nil = use the merchant's
	// pricing_settings.prices_include_tax_default (itself false when the
	// merchant never set one).
	PriceIncludesTax *bool                `json:"price_includes_tax"`
	Variants         []createVariantInput `json:"variants"`
}

type variantResponse struct {
	VariantID        string  `json:"variant_id"`
	SKU              string  `json:"sku"`
	CostPrice        string  `json:"cost_price"`
	MRP              string  `json:"mrp"`
	SellingPrice     string  `json:"selling_price"`
	OriginalBarcode  *string `json:"original_barcode"`
	GeneratedBarcode *string `json:"generated_barcode"`
}

type productResponse struct {
	ProductID        string            `json:"product_id"`
	Name             string            `json:"name"`
	ShortDescription string            `json:"short_description"`
	HSNCode          string            `json:"hsn_code"`
	CategoryID       *string           `json:"category_id"`
	BrandID          *string           `json:"brand_id"`
	TaxSlabID        *string           `json:"tax_slab_id"`
	CollectionID     *string           `json:"collection_id"`
	ProductType      string            `json:"product_type"`
	Status           string            `json:"status"`
	PriceIncludesTax bool              `json:"price_includes_tax"`
	Variants         []variantResponse `json:"variants"`
}

var errNoVariants = errors.New("at least one variant is required")

// CreateProduct: POST /products — gated by catalog.manage. Creates the
// product row and every listed variant in the same transaction; a
// product with zero variants can't be sold (no SKU, no price, nothing
// AddLine or GetStock could ever reference), so this refuses rather than
// creating a half-usable product a caller would just have to fix up with
// a second call anyway.
func (h *ListHandler) CreateProduct(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createProductRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name is required")
		return
	}
	if len(req.Variants) == 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "at least one variant is required")
		return
	}
	if req.ProductType == "" {
		req.ProductType = "simple"
	}
	for _, v := range req.Variants {
		if v.SKU == "" || v.MRP <= 0 || v.SellingPrice <= 0 {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "each variant needs a sku, a positive mrp, and a positive selling_price")
			return
		}
	}

	var resp productResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		resp, err = createProductTx(ctx, tx, req)
		return err
	})

	switch {
	case uniqueViolationConstraint(err) == "barcodes_merchant_id_code_key":
		httpx.Error(w, http.StatusConflict, "BARCODE_EXISTS", "this barcode is already assigned to another variant")
	case isUniqueViolation(err):
		httpx.Error(w, http.StatusConflict, "SKU_EXISTS", "one of these SKUs is already in use")
	case errors.Is(err, errAttributeRequired):
		httpx.Error(w, http.StatusBadRequest, "ATTRIBUTE_REQUIRED", err.Error())
	case errors.Is(err, errAttributeInvalidValue):
		httpx.Error(w, http.StatusBadRequest, "ATTRIBUTE_INVALID_VALUE", err.Error())
	case errors.Is(err, errInvalidBarcode):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
	case errors.Is(err, errInvalidReference):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REFERENCE", err.Error())
	case errors.Is(err, errInactiveReference):
		httpx.Error(w, http.StatusConflict, "INACTIVE_REFERENCE", err.Error()+" — reactivate it or pick another")
	case uniqueViolationConstraint(err) == "idx_product_variants_plu_code":
		httpx.Error(w, http.StatusConflict, "PLU_CODE_EXISTS", "this PLU code is already used by another variant")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create product")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}

// createProductTx is CreateProduct's transaction: the links checked
// against this tenant, the product row and every variant (insertVariant).
// Shared with CreateSimpleProduct, so a product the purchase importer
// creates goes through exactly the same checks.
func createProductTx(ctx context.Context, tx pgx.Tx, req createProductRequest) (productResponse, error) {
	categoryID := nullableUUID(req.CategoryID)
	brandID := nullableUUID(req.BrandID)
	taxSlabID := nullableUUID(req.TaxSlabID)
	collectionID := nullableUUID(req.CollectionID)

	// FK checks bypass RLS, so a link must be checked against this
	// tenant's own rows (products_edit.go's refExists).
	for _, l := range []struct {
		id    *string
		table string
	}{{categoryID, "categories"}, {brandID, "brands"}, {taxSlabID, "tax_slabs"}, {collectionID, "collections"}} {
		if l.id != nil {
			if err := refUsable(ctx, tx, l.table, *l.id); err != nil {
				return productResponse{}, err
			}
		}
	}

	var priceIncludesTax bool
	if req.PriceIncludesTax != nil {
		priceIncludesTax = *req.PriceIncludesTax
	} else if err := tx.QueryRow(ctx, `
		SELECT COALESCE((SELECT prices_include_tax_default FROM pricing_settings), false)`,
	).Scan(&priceIncludesTax); err != nil {
		return productResponse{}, err
	}

	var productID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO products (id, merchant_id, category_id, brand_id, tax_slab_id, collection_id, name, short_description, hsn_code, product_type, price_includes_tax)
		VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id`,
		categoryID, brandID, taxSlabID, collectionID, req.Name, req.ShortDescription, req.HSNCode, req.ProductType, priceIncludesTax,
	).Scan(&productID); err != nil {
		return productResponse{}, err
	}

	variants := make([]variantResponse, 0, len(req.Variants))
	for _, v := range req.Variants {
		vr, err := insertVariant(ctx, tx, productID, categoryID, v)
		if err != nil {
			return productResponse{}, err
		}
		variants = append(variants, vr)
	}

	resp := productResponse{
		ProductID: productID, Name: req.Name, ShortDescription: req.ShortDescription, HSNCode: req.HSNCode,
		CategoryID: categoryID, BrandID: brandID, TaxSlabID: taxSlabID, CollectionID: collectionID,
		ProductType: req.ProductType, Status: "active", PriceIncludesTax: priceIncludesTax, Variants: variants,
	}
	return resp, nil
}

type updateProductRequest struct {
	Name             *string `json:"name"`
	ShortDescription *string `json:"short_description"`
	HSNCode          *string `json:"hsn_code"`
	CategoryID       *string `json:"category_id"`
	BrandID          *string `json:"brand_id"`
	TaxSlabID        *string `json:"tax_slab_id"`
	CollectionID     *string `json:"collection_id"`
	Status           *string `json:"status"` // "active" | "inactive" | "discontinued"
	// Flipping this reinterprets every variant's existing selling_price
	// (₹1180 exclusive → customer pays ₹1392.40; ₹1180 inclusive →
	// customer pays ₹1180). Sales already made keep their own snapshot
	// (sales_order_lines.price_includes_tax); open carts' lines too.
	PriceIncludesTax *bool `json:"price_includes_tax"`
	// POSColor: the POS tile colour, "#RRGGBB"; "" clears it (migrations/063).
	POSColor *string `json:"pos_color"`
}

// posColor is a tile colour: #RRGGBB.
var posColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func nullableUUID(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func formatMoney(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// insertVariant creates one variant of productID — attribute validation
// against the product's category, the variant row, a zero stock_levels row
// at every branch, and any requested barcodes. Shared by CreateProduct and
// POST /products/{id}/variants (products_edit.go), so a variant added
// later goes through exactly the same checks as one created with the
// product.
func insertVariant(ctx context.Context, tx pgx.Tx, productID string, categoryID *string, v createVariantInput) (variantResponse, error) {
	combo := v.AttributeCombo
	if combo == nil {
		combo = map[string]any{}
	}
	// Phase 8: validates attribute_combo against whatever attribute
	// set this product's category declares (attributes.go) — a
	// no-op for a category with no declared set, or no category at
	// all, so this is purely additive over Phase 1's behavior.
	if err := validateAttributeCombo(ctx, tx, categoryID, combo); err != nil {
		return variantResponse{}, err
	}
	comboJSON, err := json.Marshal(combo)
	if err != nil {
		return variantResponse{}, err
	}
	var variantID string
	if err := tx.QueryRow(ctx, `
			INSERT INTO product_variants (id, merchant_id, product_id, sku, attribute_combo, cost_price, mrp, selling_price, track_serial, track_batch, plu_code)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9,''))
			RETURNING id`,
		productID, v.SKU, comboJSON, v.CostPrice, v.MRP, v.SellingPrice, v.TrackSerial, v.TrackBatch, v.PLUCode,
	).Scan(&variantID); err != nil {
		return variantResponse{}, err
	}
	// Every existing branch gets a zero-stock stock_levels row for
	// this variant immediately, not lazily on first GRN/adjustment —
	// found live: without this, reserveStock's read
	// (internal/sales/reservation.go) hits a plain pgx.ErrNoRows for
	// a variant that has never been stocked anywhere, which
	// AddLine's error mapping surfaces as a confusing
	// "order or product not found" instead of the correct
	// "no stock available" — reproduced by actually creating a
	// product and adding it to a cart, not just reasoning about it.
	// Seeding at 0 makes a brand-new variant behave exactly like any
	// other unstocked one: visible in stock reports at zero, and a
	// real STOCK_UNAVAILABLE (not a NOT_FOUND) if sold before being
	// received.
	//
	// Phase 15: every branch, whatever the creator's branch scope. A new
	// variant's zero rows are bookkeeping its creation implies everywhere,
	// not the creator acting on another branch's stock.
	if err := db.Unrestricted(ctx, tx, func() error {
		_, err := tx.Exec(ctx, `
			INSERT INTO stock_levels (id, merchant_id, branch_id, variant_id, on_hand, reserved, version)
			SELECT gen_random_uuid(), current_setting('app.tenant_id')::uuid, b.id, $1, 0, 0, 0
			FROM branches b WHERE b.merchant_id = current_setting('app.tenant_id')::uuid
			ON CONFLICT (branch_id, variant_id) DO NOTHING`, variantID)
		return err
	}); err != nil {
		return variantResponse{}, err
	}

	vr := variantResponse{
		VariantID: variantID, SKU: v.SKU,
		CostPrice: formatMoney(v.CostPrice), MRP: formatMoney(v.MRP), SellingPrice: formatMoney(v.SellingPrice),
	}
	// Original first, so it naturally becomes this variant's primary
	// barcode (assignBarcodeInTx's is_primary logic is "whichever
	// gets inserted first") whenever both are given — the real
	// barcode already on the item is what a scanner at the
	// register should match, not an internal one nothing
	// physically carries yet.
	if v.OriginalBarcode != "" {
		code, err := assignBarcodeInTx(ctx, tx, variantID, v.OriginalBarcode)
		if err != nil {
			return variantResponse{}, err
		}
		vr.OriginalBarcode = &code
	}
	if v.GenerateBarcode {
		code, err := assignBarcodeInTx(ctx, tx, variantID, "")
		if err != nil {
			return variantResponse{}, err
		}
		vr.GeneratedBarcode = &code
	}

	return vr, nil
}
