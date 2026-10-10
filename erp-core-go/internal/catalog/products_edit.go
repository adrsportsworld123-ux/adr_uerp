package catalog

// Product editing (completeness audit, 2026-10-04): until now a product
// could be created but not really maintained — PATCH /products/{id}
// existed, but nothing could load one product to edit, an optional link
// (category/brand/collection/tax slab) couldn't be cleared ("" failed the
// ::uuid cast with a 500), a category change skipped the attribute-set
// validation CreateProduct enforces, variants couldn't be edited at all,
// a variant couldn't be added later, and edits never reached search or
// the audit trail. Prices stay on PATCH /pricing/variants/{id} — that's
// where the below-cost guard and price_history live — so a variant edit
// here refuses price fields rather than bypassing them.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/search"
)

var (
	errInvalidReference = errors.New("referenced record not found")
	// errInactiveReference: the category/brand/tax slab exists but was
	// deactivated (migrations/036) — fine for a product that already
	// points at it, refused for a new link.
	errInactiveReference  = errors.New("referenced record is inactive")
	errVariantHasStock    = errors.New("variant has stock")
	errOpenPriceForbidden = errors.New("open price needs pricing.manage")
	errPricesElsewhere    = errors.New("prices are changed via PATCH /pricing/variants/{id}")
)

// SellablePredicate is the one definition of "this variant can be sold":
// the product is active and the variant isn't inactive/discontinued
// ('out_of_stock' is informational — stock_levels decides availability).
// Used by the barcode scan here, sales.AddLineToCart, quotation lines,
// and the search index, so "discontinue" means the same thing everywhere.
// Expects the tables aliased p (products) and pv (product_variants).
const SellablePredicate = `(p.status = 'active' AND pv.status IN ('active','out_of_stock'))`

type productDetailVariant struct {
	VariantID        string         `json:"variant_id"`
	SKU              string         `json:"sku"`
	AttributeCombo   map[string]any `json:"attribute_combo"`
	CostPrice        string         `json:"cost_price"`
	MRP              string         `json:"mrp"`
	SellingPrice     string         `json:"selling_price"`
	TrackSerial      bool           `json:"track_serial"`
	TrackBatch       bool           `json:"track_batch"`
	PLUCode          *string        `json:"plu_code"`
	Status           string         `json:"status"`
	OriginalBarcode  *string        `json:"original_barcode"`
	GeneratedBarcode *string        `json:"generated_barcode"`
	OnHandTotal      string         `json:"on_hand_total"` // across all branches — why track_batch may be locked
	OpenPrice        *OpenPrice     `json:"open_price"`    // Phase 13; nil = fixed price
}

type productDetail struct {
	ProductID        string                 `json:"product_id"`
	Name             string                 `json:"name"`
	ShortDescription string                 `json:"short_description"`
	HSNCode          string                 `json:"hsn_code"`
	CategoryID       *string                `json:"category_id"`
	CategoryName     *string                `json:"category_name"`
	BrandID          *string                `json:"brand_id"`
	BrandName        *string                `json:"brand_name"`
	TaxSlabID        *string                `json:"tax_slab_id"`
	TaxSlabName      *string                `json:"tax_slab_name"`
	CollectionID     *string                `json:"collection_id"`
	CollectionName   *string                `json:"collection_name"`
	ProductType      string                 `json:"product_type"`
	Status           string                 `json:"status"`
	PriceIncludesTax bool                   `json:"price_includes_tax"`
	UpdatedAt        string                 `json:"updated_at"`
	Variants         []productDetailVariant `json:"variants"`
}

func loadProductDetail(ctx context.Context, tx pgx.Tx, productID string) (productDetail, error) {
	var d productDetail
	if err := tx.QueryRow(ctx, `
		SELECT p.id, p.name, COALESCE(p.short_description,''), COALESCE(p.hsn_code,''),
		       p.category_id::text, c.name, p.brand_id::text, b.name, p.tax_slab_id::text, ts.name,
		       p.collection_id::text, col.name, p.product_type, p.status, p.price_includes_tax, p.updated_at::text
		FROM products p
		LEFT JOIN categories c ON c.id = p.category_id
		LEFT JOIN brands b ON b.id = p.brand_id
		LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
		LEFT JOIN collections col ON col.id = p.collection_id
		WHERE p.id = $1`, productID,
	).Scan(&d.ProductID, &d.Name, &d.ShortDescription, &d.HSNCode, &d.CategoryID, &d.CategoryName, &d.BrandID, &d.BrandName,
		&d.TaxSlabID, &d.TaxSlabName, &d.CollectionID, &d.CollectionName, &d.ProductType, &d.Status, &d.PriceIncludesTax, &d.UpdatedAt); err != nil {
		return d, err
	}

	rows, err := tx.Query(ctx, `
		SELECT pv.id, pv.sku, pv.attribute_combo, pv.cost_price::text, pv.mrp::text, pv.selling_price::text,
		       pv.track_serial, pv.track_batch, pv.plu_code, pv.status,
		       (SELECT code FROM barcodes WHERE variant_id = pv.id AND source = 'original' ORDER BY created_at LIMIT 1),
		       (SELECT code FROM barcodes WHERE variant_id = pv.id AND source = 'generated' ORDER BY created_at LIMIT 1),
		       COALESCE((SELECT SUM(on_hand) FROM stock_levels WHERE variant_id = pv.id), 0)::text,
		       `+OpenPriceColumns+`
		FROM product_variants pv
		WHERE pv.product_id = $1
		ORDER BY pv.sku`, productID)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	d.Variants = []productDetailVariant{}
	for rows.Next() {
		var v productDetailVariant
		var combo []byte
		var opEnabled bool
		var opMin, opMax, opAbove *string
		if err := rows.Scan(&v.VariantID, &v.SKU, &combo, &v.CostPrice, &v.MRP, &v.SellingPrice, &v.TrackSerial, &v.TrackBatch,
			&v.PLUCode, &v.Status, &v.OriginalBarcode, &v.GeneratedBarcode, &v.OnHandTotal,
			&opEnabled, &opMin, &opMax, &opAbove); err != nil {
			return d, err
		}
		v.OpenPrice = NewOpenPrice(opEnabled, opMin, opMax, opAbove)
		v.AttributeCombo = map[string]any{}
		if len(combo) > 0 {
			if err := json.Unmarshal(combo, &v.AttributeCombo); err != nil {
				return d, err
			}
		}
		d.Variants = append(d.Variants, v)
	}
	return d, rows.Err()
}

// GetProduct: GET /products/{id} — everything an edit screen needs, in
// one call. Open to any authenticated user, same as GET /products.
func (h *ListHandler) GetProduct(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var d productDetail
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		d, err = loadProductDetail(ctx, tx, chi.URLParam(r, "id"))
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "product not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load product")
	default:
		httpx.JSON(w, http.StatusOK, d)
	}
}

// refExists checks an optional link target exists for THIS tenant. A
// foreign key alone doesn't: FK checks bypass RLS, so another merchant's
// category id would satisfy it. The table name is always a constant from
// this file, never request input.
func refExists(ctx context.Context, tx pgx.Tx, table, id string) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id::text = $1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %s %s", errInvalidReference, strings.TrimSuffix(table, "s"), id)
	}
	return nil
}

// lifecycleTables are the link targets with an active/inactive status
// (migrations/036). Collections have none yet.
var lifecycleTables = map[string]bool{"categories": true, "brands": true, "tax_slabs": true}

// refUsable is refExists plus "and it's active" — the check for a link
// being newly set. An existing link to a since-deactivated record stays
// valid, so editing an old product never forces a re-categorization.
func refUsable(ctx context.Context, tx pgx.Tx, table, id string) error {
	if err := refExists(ctx, tx, table, id); err != nil {
		return err
	}
	if !lifecycleTables[table] {
		return nil
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM `+table+` WHERE id::text = $1`, id).Scan(&status); err != nil {
		return err
	}
	if status != "active" {
		return fmt.Errorf("%w: %s %s", errInactiveReference, strings.TrimSuffix(table, "s"), id)
	}
	return nil
}

// linkChanged reports whether a PATCH field for an optional link changes
// it: nil = leave it, "" = clear it, an id = set it.
func linkChanged(req *string, current *string) bool {
	if req == nil {
		return false
	}
	cur := ""
	if current != nil {
		cur = *current
	}
	return *req != cur
}

// UpdateProduct: PATCH /products/{id} — partial update of the product's
// own fields. For category_id/brand_id/tax_slab_id/collection_id: omitted
// leaves it, "" clears it, an id sets it (checked to exist for this
// merchant). A category change re-validates every variant's attributes
// against the new category's attribute set — the same rule CreateProduct
// applies — naming the first variant that doesn't fit.
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
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name can't be empty")
			return
		}
		req.Name = &trimmed
	}

	var d productDetail
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		// Lock first, so "before" can't change under this edit.
		var locked string
		if err := tx.QueryRow(ctx, `SELECT id FROM products WHERE id = $1 FOR UPDATE`, productID).Scan(&locked); err != nil {
			return err
		}
		before, err := loadProductDetail(ctx, tx, productID)
		if err != nil {
			return err
		}

		links := []struct {
			req, current *string
			table        string
		}{
			{req.CategoryID, before.CategoryID, "categories"},
			{req.BrandID, before.BrandID, "brands"},
			{req.TaxSlabID, before.TaxSlabID, "tax_slabs"},
			{req.CollectionID, before.CollectionID, "collections"},
		}
		for _, l := range links {
			if l.req == nil || *l.req == "" {
				continue
			}
			check := refExists
			if linkChanged(l.req, l.current) {
				check = refUsable
			}
			if err := check(ctx, tx, l.table, *l.req); err != nil {
				return err
			}
		}

		if linkChanged(req.CategoryID, before.CategoryID) {
			newCategory := nullableUUID(*req.CategoryID)
			for _, v := range before.Variants {
				if err := validateAttributeCombo(ctx, tx, newCategory, v.AttributeCombo); err != nil {
					return fmt.Errorf("variant %s: %w", v.SKU, err)
				}
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE products SET
				name = COALESCE($1, name),
				short_description = COALESCE($2, short_description),
				hsn_code = COALESCE($3, hsn_code),
				category_id   = CASE WHEN $4::text IS NULL THEN category_id   ELSE NULLIF($4,'')::uuid END,
				brand_id      = CASE WHEN $5::text IS NULL THEN brand_id      ELSE NULLIF($5,'')::uuid END,
				tax_slab_id   = CASE WHEN $6::text IS NULL THEN tax_slab_id   ELSE NULLIF($6,'')::uuid END,
				status = COALESCE($7, status),
				collection_id = CASE WHEN $9::text IS NULL THEN collection_id ELSE NULLIF($9,'')::uuid END,
				price_includes_tax = COALESCE($10, price_includes_tax),
				updated_at = now()
			WHERE id = $8`,
			req.Name, req.ShortDescription, req.HSNCode, req.CategoryID, req.BrandID, req.TaxSlabID, req.Status, productID, req.CollectionID, req.PriceIncludesTax,
		); err != nil {
			return err
		}

		if d, err = loadProductDetail(ctx, tx, productID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{
			EntityType: "products", EntityID: productID, Action: "update", PerformedBy: claims.UserID,
			Before: productAuditView(before), After: productAuditView(d),
		})
	})

	if respondProductEditError(w, err, "could not update product") {
		return
	}
	h.reindexProduct(r.Context(), claims.TenantID, d)
	httpx.JSON(w, http.StatusOK, d)
}

// productAuditView is the slice of a product the audit trail records —
// the editable fields, not the variant list (variant edits log their own
// entries).
func productAuditView(d productDetail) map[string]any {
	return map[string]any{
		"name": d.Name, "short_description": d.ShortDescription, "hsn_code": d.HSNCode,
		"category_id": d.CategoryID, "brand_id": d.BrandID, "tax_slab_id": d.TaxSlabID,
		"collection_id": d.CollectionID, "status": d.Status, "price_includes_tax": d.PriceIncludesTax,
	}
}

type updateVariantRequest struct {
	SKU            *string        `json:"sku"`
	AttributeCombo map[string]any `json:"attribute_combo"` // replaces the whole combo when present
	PLUCode        *string        `json:"plu_code"`        // "" clears it
	Status         *string        `json:"status"`          // active | inactive | discontinued
	TrackSerial    *bool          `json:"track_serial"`
	TrackBatch     *bool          `json:"track_batch"`
	// Phase 13: open-price range. A pricing decision, so it also needs
	// pricing.manage (the route itself is catalog.manage).
	OpenPrice *OpenPriceInput `json:"open_price"`
	// Present only to refuse them with a pointer to the right endpoint,
	// rather than silently ignoring a price the caller meant to change.
	CostPrice    *float64 `json:"cost_price"`
	MRP          *float64 `json:"mrp"`
	SellingPrice *float64 `json:"selling_price"`
}

// UpdateVariant: PATCH /products/variants/{id}. Switching track_batch is
// refused while the variant holds any stock (409 VARIANT_HAS_STOCK):
// turning it on would strand every existing unit as unbatched (and so
// unsellable), turning it off would orphan its batches — receive or
// adjust it to zero first.
func (h *ListHandler) UpdateVariant(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	variantID := chi.URLParam(r, "id")
	var req updateVariantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.CostPrice != nil || req.MRP != nil || req.SellingPrice != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", errPricesElsewhere.Error()+" (below-cost guard and price history)")
		return
	}
	if req.SKU != nil {
		trimmed := strings.TrimSpace(*req.SKU)
		if trimmed == "" {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "sku can't be empty")
			return
		}
		req.SKU = &trimmed
	}
	if req.Status != nil && *req.Status != "active" && *req.Status != "inactive" && *req.Status != "discontinued" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be active, inactive, or discontinued")
		return
	}
	if req.OpenPrice != nil {
		if err := req.OpenPrice.validate(); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
	}

	var d productDetail
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var productID string
		var categoryID *string
		if err := tx.QueryRow(ctx, `
			SELECT pv.product_id, p.category_id::text FROM product_variants pv JOIN products p ON p.id = pv.product_id
			WHERE pv.id = $1 FOR UPDATE OF pv`, variantID).Scan(&productID, &categoryID); err != nil {
			return err
		}
		before, err := loadProductDetail(ctx, tx, productID)
		if err != nil {
			return err
		}
		var old productDetailVariant
		for _, v := range before.Variants {
			if v.VariantID == variantID {
				old = v
			}
		}

		if req.AttributeCombo != nil {
			if err := validateAttributeCombo(ctx, tx, categoryID, req.AttributeCombo); err != nil {
				return err
			}
		}
		if req.TrackBatch != nil && *req.TrackBatch != old.TrackBatch {
			var stocked bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM stock_levels WHERE variant_id = $1 AND (on_hand <> 0 OR reserved <> 0))
				    OR EXISTS (SELECT 1 FROM stock_batches WHERE variant_id = $1 AND quantity_remaining > 0)`, variantID).Scan(&stocked); err != nil {
				return err
			}
			if stocked {
				return errVariantHasStock
			}
		}

		if req.OpenPrice != nil {
			perms, err := authn.FetchPermissions(ctx, tx, claims.UserID)
			if err != nil {
				return err
			}
			if !authn.HasPermission(perms, "pricing.manage") {
				return errOpenPriceForbidden
			}
			op := req.OpenPrice
			var min, max *float64
			above := op.ApprovalAbove
			if op.Enabled {
				min, max = &op.Min, &op.Max
			} else {
				above = nil
			}
			if _, err := tx.Exec(ctx, `
				UPDATE product_variants SET open_price = $1, open_price_min = $2, open_price_max = $3,
				       open_price_approval_above = $4, updated_at = now()
				WHERE id = $5`, op.Enabled, min, max, above, variantID); err != nil {
				return err
			}
		}

		var comboJSON []byte
		if req.AttributeCombo != nil {
			if comboJSON, err = json.Marshal(req.AttributeCombo); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE product_variants SET
				sku = COALESCE($1, sku),
				attribute_combo = COALESCE($2::jsonb, attribute_combo),
				plu_code = CASE WHEN $3::text IS NULL THEN plu_code ELSE NULLIF($3,'') END,
				status = COALESCE($4, status),
				track_serial = COALESCE($5, track_serial),
				track_batch = COALESCE($6, track_batch),
				updated_at = now()
			WHERE id = $7`,
			req.SKU, comboJSON, req.PLUCode, req.Status, req.TrackSerial, req.TrackBatch, variantID); err != nil {
			return err
		}

		if d, err = loadProductDetail(ctx, tx, productID); err != nil {
			return err
		}
		var after productDetailVariant
		for _, v := range d.Variants {
			if v.VariantID == variantID {
				after = v
			}
		}
		return audit.Log(ctx, tx, audit.Entry{
			EntityType: "product_variants", EntityID: variantID, Action: "update", PerformedBy: claims.UserID,
			Before: variantAuditView(old), After: variantAuditView(after),
		})
	})

	if respondProductEditError(w, err, "could not update variant") {
		return
	}
	h.reindexProduct(r.Context(), claims.TenantID, d)
	httpx.JSON(w, http.StatusOK, d)
}

func variantAuditView(v productDetailVariant) map[string]any {
	return map[string]any{
		"sku": v.SKU, "attribute_combo": v.AttributeCombo, "plu_code": v.PLUCode, "status": v.Status,
		"track_serial": v.TrackSerial, "track_batch": v.TrackBatch, "open_price": v.OpenPrice,
	}
}

// AddVariant: POST /products/{id}/variants — a new size/colour on an
// existing product, through the same insertVariant CreateProduct uses
// (attribute validation, zero stock at every branch, optional barcodes).
func (h *ListHandler) AddVariant(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	productID := chi.URLParam(r, "id")
	var v createVariantInput
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	v.SKU = strings.TrimSpace(v.SKU)
	if v.SKU == "" || v.MRP <= 0 || v.SellingPrice <= 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a variant needs a sku, a positive mrp, and a positive selling_price")
		return
	}

	var d productDetail
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var categoryID *string
		if err := tx.QueryRow(ctx, `SELECT category_id::text FROM products WHERE id = $1 FOR UPDATE`, productID).Scan(&categoryID); err != nil {
			return err
		}
		vr, err := insertVariant(ctx, tx, productID, categoryID, v)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE products SET updated_at = now() WHERE id = $1`, productID); err != nil {
			return err
		}
		if d, err = loadProductDetail(ctx, tx, productID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{
			EntityType: "product_variants", EntityID: vr.VariantID, Action: "create", PerformedBy: claims.UserID,
			After: map[string]any{"product_id": productID, "sku": vr.SKU, "selling_price": vr.SellingPrice, "mrp": vr.MRP, "cost_price": vr.CostPrice},
		})
	})

	if respondProductEditError(w, err, "could not add variant") {
		return
	}
	h.reindexProduct(r.Context(), claims.TenantID, d)
	httpx.JSON(w, http.StatusCreated, d)
}

// respondProductEditError writes the error response for the three edit
// endpoints and reports whether it did.
func respondProductEditError(w http.ResponseWriter, err error, internalMsg string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "product or variant not found")
	case errors.Is(err, errInvalidReference):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REFERENCE", err.Error())
	case errors.Is(err, errInactiveReference):
		httpx.Error(w, http.StatusConflict, "INACTIVE_REFERENCE", err.Error()+" — reactivate it or pick another")
	case errors.Is(err, errAttributeRequired):
		httpx.Error(w, http.StatusBadRequest, "ATTRIBUTE_REQUIRED", err.Error())
	case errors.Is(err, errAttributeInvalidValue):
		httpx.Error(w, http.StatusBadRequest, "ATTRIBUTE_INVALID_VALUE", err.Error())
	case errors.Is(err, errOpenPriceForbidden):
		httpx.Error(w, http.StatusForbidden, "FORBIDDEN", "setting an open-price range needs the pricing.manage permission")
	case errors.Is(err, errVariantHasStock):
		httpx.Error(w, http.StatusConflict, "VARIANT_HAS_STOCK", "batch tracking can only be switched while the variant holds no stock — bring it to zero first")
	case errors.Is(err, errInvalidBarcode):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
	case uniqueViolationConstraint(err) == "barcodes_merchant_id_code_key":
		httpx.Error(w, http.StatusConflict, "BARCODE_EXISTS", "this barcode is already assigned to another variant")
	case uniqueViolationConstraint(err) == "idx_product_variants_plu_code":
		httpx.Error(w, http.StatusConflict, "PLU_CODE_EXISTS", "this PLU code is already used by another variant")
	case isUniqueViolation(err):
		httpx.Error(w, http.StatusConflict, "SKU_EXISTS", "this SKU is already in use")
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", internalMsg)
	}
	return true
}

// reindexProduct pushes every variant of an edited product to search —
// best-effort, after commit, same posture as pricing's reindex: a search
// write failing never undoes an edit Postgres already committed.
func (h *ListHandler) reindexProduct(ctx context.Context, tenantID string, d productDetail) {
	if !h.Search.Enabled() {
		return
	}
	for _, v := range d.Variants {
		doc, err := search.FetchDocument(ctx, h.DB, tenantID, v.VariantID)
		if err != nil {
			log.Printf("catalog: reindex variant %s: fetch: %v", v.VariantID, err)
			continue
		}
		if err := h.Search.IndexDocument(ctx, doc); err != nil {
			log.Printf("catalog: reindex variant %s: index: %v", v.VariantID, err)
		}
	}
}
