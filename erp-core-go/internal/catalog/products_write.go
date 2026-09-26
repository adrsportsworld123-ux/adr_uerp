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
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
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
}

type createProductRequest struct {
	Name             string               `json:"name"`
	ShortDescription string               `json:"short_description"`
	HSNCode          string               `json:"hsn_code"`
	CategoryID       string               `json:"category_id"`
	BrandID          string               `json:"brand_id"`
	TaxSlabID        string               `json:"tax_slab_id"`
	CollectionID     string               `json:"collection_id"` // Phase 8 (Apparel) — optional, "" = no collection
	ProductType      string               `json:"product_type"`  // "simple" | "variant" | "composite"; defaults to "simple"
	Variants         []createVariantInput `json:"variants"`
}

type variantResponse struct {
	VariantID    string `json:"variant_id"`
	SKU          string `json:"sku"`
	CostPrice    string `json:"cost_price"`
	MRP          string `json:"mrp"`
	SellingPrice string `json:"selling_price"`
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

	categoryID := nullableUUID(req.CategoryID)
	brandID := nullableUUID(req.BrandID)
	taxSlabID := nullableUUID(req.TaxSlabID)
	collectionID := nullableUUID(req.CollectionID)

	var resp productResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var productID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO products (id, merchant_id, category_id, brand_id, tax_slab_id, collection_id, name, short_description, hsn_code, product_type)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id`,
			categoryID, brandID, taxSlabID, collectionID, req.Name, req.ShortDescription, req.HSNCode, req.ProductType,
		).Scan(&productID); err != nil {
			return err
		}

		variants := make([]variantResponse, 0, len(req.Variants))
		for _, v := range req.Variants {
			combo := v.AttributeCombo
			if combo == nil {
				combo = map[string]any{}
			}
			// Phase 8: validates attribute_combo against whatever attribute
			// set this product's category declares (attributes.go) — a
			// no-op for a category with no declared set, or no category at
			// all, so this is purely additive over Phase 1's behavior.
			if err := validateAttributeCombo(ctx, tx, categoryID, combo); err != nil {
				return err
			}
			comboJSON, err := json.Marshal(combo)
			if err != nil {
				return err
			}
			var variantID string
			if err := tx.QueryRow(ctx, `
				INSERT INTO product_variants (id, merchant_id, product_id, sku, attribute_combo, cost_price, mrp, selling_price, track_serial, track_batch, plu_code)
				VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9,''))
				RETURNING id`,
				productID, v.SKU, comboJSON, v.CostPrice, v.MRP, v.SellingPrice, v.TrackSerial, v.TrackBatch, v.PLUCode,
			).Scan(&variantID); err != nil {
				return err
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
			if _, err := tx.Exec(ctx, `
				INSERT INTO stock_levels (id, merchant_id, branch_id, variant_id, on_hand, reserved, version)
				SELECT gen_random_uuid(), current_setting('app.tenant_id')::uuid, b.id, $1, 0, 0, 0
				FROM branches b WHERE b.merchant_id = current_setting('app.tenant_id')::uuid
				ON CONFLICT (branch_id, variant_id) DO NOTHING`, variantID); err != nil {
				return err
			}

			variants = append(variants, variantResponse{
				VariantID: variantID, SKU: v.SKU,
				CostPrice: formatMoney(v.CostPrice), MRP: formatMoney(v.MRP), SellingPrice: formatMoney(v.SellingPrice),
			})
		}

		resp = productResponse{
			ProductID: productID, Name: req.Name, ShortDescription: req.ShortDescription, HSNCode: req.HSNCode,
			CategoryID: categoryID, BrandID: brandID, TaxSlabID: taxSlabID, CollectionID: collectionID,
			ProductType: req.ProductType, Status: "active", Variants: variants,
		}
		return nil
	})

	switch {
	case isUniqueViolation(err):
		httpx.Error(w, http.StatusConflict, "SKU_EXISTS", "one of these SKUs is already in use")
	case errors.Is(err, errAttributeRequired):
		httpx.Error(w, http.StatusBadRequest, "ATTRIBUTE_REQUIRED", err.Error())
	case errors.Is(err, errAttributeInvalidValue):
		httpx.Error(w, http.StatusBadRequest, "ATTRIBUTE_INVALID_VALUE", err.Error())
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create product")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
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
}

// UpdateProduct: PATCH /products/{id} — gated by catalog.manage. Product-
// level metadata only (name/description/HSN/category/brand/tax
// slab/status) — variant pricing is already owned by
// PATCH /pricing/variants/{id} (Phase 2's Pricing sub-area), so this
// deliberately doesn't duplicate that write path.
func (h *ListHandler) UpdateProduct(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	productID := chi.URLParam(r, "id")

	var req updateProductRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Status != nil && *req.Status != "active" && *req.Status != "inactive" && *req.Status != "discontinued" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be active, inactive, or discontinued")
		return
	}

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE products SET
				name = COALESCE($1, name),
				short_description = COALESCE($2, short_description),
				hsn_code = COALESCE($3, hsn_code),
				category_id = COALESCE($4::uuid, category_id),
				brand_id = COALESCE($5::uuid, brand_id),
				tax_slab_id = COALESCE($6::uuid, tax_slab_id),
				status = COALESCE($7, status),
				collection_id = COALESCE($9::uuid, collection_id),
				updated_at = now()
			WHERE id = $8`,
			req.Name, req.ShortDescription, req.HSNCode, req.CategoryID, req.BrandID, req.TaxSlabID, req.Status, productID, req.CollectionID,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "product not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update product")
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"product_id": productID, "status": "updated"})
	}
}

func nullableUUID(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func formatMoney(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}
