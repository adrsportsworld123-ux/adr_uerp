// Categories, brands, and tax slabs — the three lookups a product needs
// (all nullable FKs except tax_slab_id, which GST calculation at
// checkout actually depends on) that had a schema and seed data since
// migrations/001_schema.sql but never an API surface at all, the same
// gap products_write.go closes for products/variants themselves. See
// migrations/019_catalog_management.sql's header comment for the full
// story.
//
// Deliberately minimal: list + create only, no update/delete, no
// category hierarchy management beyond a single optional parent_id —
// this is unblocking product creation, not building out full
// catalog-taxonomy management as its own feature.
package catalog

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type TaxonomyHandler struct {
	DB *db.DB
}

// ---------------------------------------------------------------------
// Categories: GET/POST /categories — gated by catalog.manage on write.
// ---------------------------------------------------------------------

type categoryResponse struct {
	CategoryID string  `json:"category_id"`
	ParentID   *string `json:"parent_id"`
	Name       string  `json:"name"`
	Path       string  `json:"path"`
}

type createCategoryRequest struct {
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
}

func (h *TaxonomyHandler) ListCategories(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	list := []categoryResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, parent_id::text, name, COALESCE(path,'') FROM categories ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c categoryResponse
			var parentID *string
			if err := rows.Scan(&c.CategoryID, &parentID, &c.Name, &c.Path); err != nil {
				return err
			}
			c.ParentID = parentID
			list = append(list, c)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list categories")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"categories": list})
}

func (h *TaxonomyHandler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name is required")
		return
	}

	var resp categoryResponse
	resp.Name = req.Name
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		parentID := nullableUUID(req.ParentID)
		var path string
		if parentID != nil {
			var parentPath string
			if err := tx.QueryRow(ctx, `SELECT COALESCE(path,'') FROM categories WHERE id = $1`, *parentID).Scan(&parentPath); err != nil {
				return err
			}
			path = parentPath + "/" + req.Name
		} else {
			path = "/" + req.Name
		}
		return tx.QueryRow(ctx, `
			INSERT INTO categories (id, merchant_id, parent_id, name, path)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3)
			RETURNING id, parent_id::text, path`,
			parentID, req.Name, path,
		).Scan(&resp.CategoryID, &resp.ParentID, &resp.Path)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create category")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// ---------------------------------------------------------------------
// Brands: GET/POST /brands — gated by catalog.manage on write.
// ---------------------------------------------------------------------

type brandResponse struct {
	BrandID string `json:"brand_id"`
	Name    string `json:"name"`
}

func (h *TaxonomyHandler) ListBrands(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	list := []brandResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name FROM brands ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b brandResponse
			if err := rows.Scan(&b.BrandID, &b.Name); err != nil {
				return err
			}
			list = append(list, b)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list brands")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"brands": list})
}

func (h *TaxonomyHandler) CreateBrand(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name is required")
		return
	}

	var resp brandResponse
	resp.Name = req.Name
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO brands (id, merchant_id, name)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1)
			RETURNING id`, req.Name,
		).Scan(&resp.BrandID)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create brand")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// ---------------------------------------------------------------------
// Tax slabs: GET/POST /tax-slabs — gated by catalog.manage on write.
// ---------------------------------------------------------------------

type taxSlabResponse struct {
	TaxSlabID string `json:"tax_slab_id"`
	Name      string `json:"name"`
	CGSTRate  string `json:"cgst_rate"`
	SGSTRate  string `json:"sgst_rate"`
	IGSTRate  string `json:"igst_rate"`
	CessRate  string `json:"cess_rate"`
}

type createTaxSlabRequest struct {
	Name     string  `json:"name"`
	CGSTRate float64 `json:"cgst_rate"`
	SGSTRate float64 `json:"sgst_rate"`
	IGSTRate float64 `json:"igst_rate"`
	CessRate float64 `json:"cess_rate"`
}

func (h *TaxonomyHandler) ListTaxSlabs(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	list := []taxSlabResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, cgst_rate::text, sgst_rate::text, igst_rate::text, cess_rate::text FROM tax_slabs ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t taxSlabResponse
			if err := rows.Scan(&t.TaxSlabID, &t.Name, &t.CGSTRate, &t.SGSTRate, &t.IGSTRate, &t.CessRate); err != nil {
				return err
			}
			list = append(list, t)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list tax slabs")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"tax_slabs": list})
}

func (h *TaxonomyHandler) CreateTaxSlab(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createTaxSlabRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name is required")
		return
	}

	var resp taxSlabResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO tax_slabs (id, merchant_id, name, cgst_rate, sgst_rate, igst_rate, cess_rate)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5)
			RETURNING id, name, cgst_rate::text, sgst_rate::text, igst_rate::text, cess_rate::text`,
			req.Name, req.CGSTRate, req.SGSTRate, req.IGSTRate, req.CessRate,
		).Scan(&resp.TaxSlabID, &resp.Name, &resp.CGSTRate, &resp.SGSTRate, &resp.IGSTRate, &resp.CessRate)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create tax slab")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}
