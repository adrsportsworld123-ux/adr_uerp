// Categories, brands, and tax slabs — the three lookups a product needs
// (all nullable FKs except tax_slab_id, which GST calculation at
// checkout actually depends on) that had a schema and seed data since
// migrations/001_schema.sql but never an API surface at all, the same
// gap products_write.go closes for products/variants themselves. See
// migrations/019_catalog_management.sql's header comment for the full
// story.
//
// Phase 11 (migrations/036_master_data_lifecycle.sql) turns list + create
// into full master maintenance: search/status/sort/paging on list, edit,
// activate/deactivate, and a delete that refuses while anything still
// references the record (internal/dependencies) — deactivate is the answer
// then. Names are unique per merchant, case-insensitively (per parent for
// categories), enforced by unique indexes and surfaced as 409
// DUPLICATE_NAME. Still no category re-parenting: moving a subtree changes
// every descendant's path and every category-scoped promotion/price rule's
// reach, which deserves its own design rather than a PATCH field.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/dependencies"
	"erp-core-go/internal/httpx"
)

type TaxonomyHandler struct {
	DB *db.DB
}

var errDuplicateName = errors.New("duplicate name")

func validStatus(s string) bool { return s == "active" || s == "inactive" }

// statusFilter turns ?status= into a SQL parameter: "" or "all" lists
// every record; otherwise only that status. Unknown values are a 400.
func statusFilter(w http.ResponseWriter, s string) (string, bool) {
	switch s {
	case "", "all":
		return "", true
	case "active", "inactive":
		return s, true
	}
	httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be all, active, or inactive")
	return "", false
}

// respondTaxonomyError maps the shared write failures for the three masters.
func respondTaxonomyError(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", what+" not found")
	case errors.Is(err, errDuplicateName) || isUniqueViolation(err):
		httpx.Error(w, http.StatusConflict, "DUPLICATE_NAME", "a "+what+" with this name already exists")
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save "+what)
	}
}

// deleteIfUnused deletes one row of table unless something still
// references it, in which case it answers 409 IN_USE with the counts — the
// same list GET /dependencies/{entity}/{id} shows, so the dialog and the
// refusal can never disagree.
func (h *TaxonomyHandler) deleteIfUnused(w http.ResponseWriter, r *http.Request, table, what string) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var blockers []dependencies.Dependency
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var locked string
		if err := tx.QueryRow(ctx, `SELECT id FROM `+table+` WHERE id::text = $1 FOR UPDATE`, id).Scan(&locked); err != nil {
			return err
		}
		deps, err := dependencies.Count(ctx, tx, table, []string{id})
		if err != nil {
			return err
		}
		if dependencies.AnyBlocking(deps) {
			blockers = deps
			return nil
		}
		_, err = tx.Exec(ctx, `DELETE FROM `+table+` WHERE id = $1`, locked)
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", what+" not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not delete "+what)
	case blockers != nil:
		httpx.ErrorWithDetails(w, http.StatusConflict, "IN_USE",
			"this "+what+" is still in use — deactivate it instead", map[string]any{"dependencies": blockers})
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---------------------------------------------------------------------
// Categories: GET/POST /categories, PATCH/DELETE /categories/{id}
// ---------------------------------------------------------------------

type categoryResponse struct {
	CategoryID   string  `json:"category_id"`
	ParentID     *string `json:"parent_id"`
	Name         string  `json:"name"`
	Path         string  `json:"path"`
	Status       string  `json:"status"`
	ProductCount int     `json:"product_count"`
}

type createCategoryRequest struct {
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
}

const categorySelect = `
	SELECT c.id, c.parent_id::text, c.name, COALESCE(c.path,''), c.status,
	       (SELECT count(*) FROM products p WHERE p.category_id = c.id)
	FROM categories c`

func scanCategory(row pgx.Row) (categoryResponse, error) {
	var c categoryResponse
	err := row.Scan(&c.CategoryID, &c.ParentID, &c.Name, &c.Path, &c.Status, &c.ProductCount)
	return c, err
}

// ListCategories: GET /categories?q=&status=&sort=name|path|products|updated&dir=&page=&limit=
// Unpaged (all rows) unless page/limit is given — the product form's
// picker relies on that. Default order stays `name`, as before Phase 11:
// existing callers take "the first category" and must keep getting the
// same one (the admin tree view asks for sort=path explicitly).
func (h *TaxonomyHandler) ListCategories(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	p := httpx.ParseListParams(r, map[string]string{
		"name": "c.name", "path": "c.path", "products": "6", "updated": "c.updated_at",
	}, "name")
	status, ok := statusFilter(w, p.Status)
	if !ok {
		return
	}
	limit, offset := p.SQLLimit()

	list := []categoryResponse{}
	total := 0
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		where := ` WHERE ($1 = '' OR c.name ILIKE '%' || $1 || '%' OR c.path ILIKE '%' || $1 || '%') AND ($2 = '' OR c.status = $2)`
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM categories c`+where, p.Q, status).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, categorySelect+where+` ORDER BY `+p.OrderBy+`, c.id LIMIT $3 OFFSET $4`, p.Q, status, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCategory(rows)
			if err != nil {
				return err
			}
			list = append(list, c)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list categories")
		return
	}
	httpx.JSON(w, http.StatusOK, p.Envelope("categories", list, total))
}

func (h *TaxonomyHandler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name is required")
		return
	}
	req.Name = strings.TrimSpace(req.Name)

	var resp categoryResponse
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
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO categories (id, merchant_id, parent_id, name, path)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3)
			RETURNING id`,
			parentID, req.Name, path,
		).Scan(&id); err != nil {
			return err
		}
		var err error
		resp, err = scanCategory(tx.QueryRow(ctx, categorySelect+` WHERE c.id = $1`, id))
		return err
	})
	if err != nil {
		respondTaxonomyError(w, err, "category")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

type updateTaxonomyRequest struct {
	Name   *string `json:"name"`
	Status *string `json:"status"`
}

func (req *updateTaxonomyRequest) validate(w http.ResponseWriter) bool {
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name can't be empty")
			return false
		}
		req.Name = &trimmed
	}
	if req.Status != nil && !validStatus(*req.Status) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be active or inactive")
		return false
	}
	return true
}

// UpdateCategory: PATCH /categories/{id} {name?, status?}. A rename
// rewrites the materialized path of the category and every descendant in
// the same transaction, so path-based lookups never see a half-renamed tree.
func (h *TaxonomyHandler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var req updateTaxonomyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if !req.validate(w) {
		return
	}

	var resp categoryResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var oldName, oldPath string
		if err := tx.QueryRow(ctx, `SELECT name, COALESCE(path,'') FROM categories WHERE id::text = $1 FOR UPDATE`, id).Scan(&oldName, &oldPath); err != nil {
			return err
		}
		if req.Name != nil && *req.Name != oldName {
			newPath := strings.TrimSuffix(oldPath, oldName) + *req.Name
			if !strings.HasSuffix(oldPath, oldName) {
				newPath = "/" + *req.Name
			}
			if _, err := tx.Exec(ctx, `UPDATE categories SET name = $1, updated_at = now() WHERE id::text = $2`, *req.Name, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE categories SET path = $1 || substr(path, length($2) + 1)
				WHERE path = $2 OR left(path, length($2) + 1) = $2 || '/'`, newPath, oldPath); err != nil {
				return err
			}
		}
		if req.Status != nil {
			if _, err := tx.Exec(ctx, `UPDATE categories SET status = $1, updated_at = now() WHERE id::text = $2`, *req.Status, id); err != nil {
				return err
			}
		}
		var err error
		resp, err = scanCategory(tx.QueryRow(ctx, categorySelect+` WHERE c.id::text = $1`, id))
		return err
	})
	if err != nil {
		respondTaxonomyError(w, err, "category")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// DeleteCategory: DELETE /categories/{id} — 204, or 409 IN_USE with the
// dependency counts (products, sub-categories, attribute sets, rules...).
func (h *TaxonomyHandler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	h.deleteIfUnused(w, r, "categories", "category")
}

// ---------------------------------------------------------------------
// Brands: GET/POST /brands, PATCH/DELETE /brands/{id}
// ---------------------------------------------------------------------

type brandResponse struct {
	BrandID      string `json:"brand_id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	ProductCount int    `json:"product_count"`
}

const brandSelect = `
	SELECT b.id, b.name, b.status, (SELECT count(*) FROM products p WHERE p.brand_id = b.id)
	FROM brands b`

func scanBrand(row pgx.Row) (brandResponse, error) {
	var b brandResponse
	err := row.Scan(&b.BrandID, &b.Name, &b.Status, &b.ProductCount)
	return b, err
}

// ListBrands: GET /brands?q=&status=&sort=name|products|updated&dir=&page=&limit=
func (h *TaxonomyHandler) ListBrands(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	p := httpx.ParseListParams(r, map[string]string{"name": "b.name", "products": "4", "updated": "b.updated_at"}, "name")
	status, ok := statusFilter(w, p.Status)
	if !ok {
		return
	}
	limit, offset := p.SQLLimit()

	list := []brandResponse{}
	total := 0
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		where := ` WHERE ($1 = '' OR b.name ILIKE '%' || $1 || '%') AND ($2 = '' OR b.status = $2)`
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM brands b`+where, p.Q, status).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, brandSelect+where+` ORDER BY `+p.OrderBy+`, b.id LIMIT $3 OFFSET $4`, p.Q, status, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			b, err := scanBrand(rows)
			if err != nil {
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
	httpx.JSON(w, http.StatusOK, p.Envelope("brands", list, total))
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name is required")
		return
	}

	var resp brandResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO brands (id, merchant_id, name)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1)
			RETURNING id`, strings.TrimSpace(req.Name),
		).Scan(&id); err != nil {
			return err
		}
		var err error
		resp, err = scanBrand(tx.QueryRow(ctx, brandSelect+` WHERE b.id = $1`, id))
		return err
	})
	if err != nil {
		respondTaxonomyError(w, err, "brand")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// UpdateBrand: PATCH /brands/{id} {name?, status?}
func (h *TaxonomyHandler) UpdateBrand(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var req updateTaxonomyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if !req.validate(w) {
		return
	}
	var resp brandResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var updated string
		if err := tx.QueryRow(ctx, `
			UPDATE brands SET name = COALESCE($1, name), status = COALESCE($2, status), updated_at = now()
			WHERE id::text = $3 RETURNING id`, req.Name, req.Status, id).Scan(&updated); err != nil {
			return err
		}
		var err error
		resp, err = scanBrand(tx.QueryRow(ctx, brandSelect+` WHERE b.id = $1`, updated))
		return err
	})
	if err != nil {
		respondTaxonomyError(w, err, "brand")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// DeleteBrand: DELETE /brands/{id} — 204, or 409 IN_USE with counts.
func (h *TaxonomyHandler) DeleteBrand(w http.ResponseWriter, r *http.Request) {
	h.deleteIfUnused(w, r, "brands", "brand")
}

// ---------------------------------------------------------------------
// Tax slabs: GET/POST /tax-slabs, PATCH/DELETE /tax-slabs/{id}
// ---------------------------------------------------------------------

type taxSlabResponse struct {
	TaxSlabID    string `json:"tax_slab_id"`
	Name         string `json:"name"`
	CGSTRate     string `json:"cgst_rate"`
	SGSTRate     string `json:"sgst_rate"`
	IGSTRate     string `json:"igst_rate"`
	CessRate     string `json:"cess_rate"`
	Status       string `json:"status"`
	ProductCount int    `json:"product_count"`
}

type createTaxSlabRequest struct {
	Name     string  `json:"name"`
	CGSTRate float64 `json:"cgst_rate"`
	SGSTRate float64 `json:"sgst_rate"`
	IGSTRate float64 `json:"igst_rate"`
	CessRate float64 `json:"cess_rate"`
}

const taxSlabSelect = `
	SELECT t.id, t.name, t.cgst_rate::text, t.sgst_rate::text, t.igst_rate::text, t.cess_rate::text, t.status,
	       (SELECT count(*) FROM products p WHERE p.tax_slab_id = t.id)
	FROM tax_slabs t`

func scanTaxSlab(row pgx.Row) (taxSlabResponse, error) {
	var t taxSlabResponse
	err := row.Scan(&t.TaxSlabID, &t.Name, &t.CGSTRate, &t.SGSTRate, &t.IGSTRate, &t.CessRate, &t.Status, &t.ProductCount)
	return t, err
}

func validRates(rates ...float64) bool {
	for _, r := range rates {
		if r < 0 || r > 100 {
			return false
		}
	}
	return true
}

// ListTaxSlabs: GET /tax-slabs?q=&status=&sort=name|rate|products&dir=&page=&limit=
func (h *TaxonomyHandler) ListTaxSlabs(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	p := httpx.ParseListParams(r, map[string]string{
		"name": "t.name", "rate": "(t.cgst_rate + t.sgst_rate + t.igst_rate + t.cess_rate)", "products": "8",
	}, "name")
	status, ok := statusFilter(w, p.Status)
	if !ok {
		return
	}
	limit, offset := p.SQLLimit()

	list := []taxSlabResponse{}
	total := 0
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		where := ` WHERE ($1 = '' OR t.name ILIKE '%' || $1 || '%') AND ($2 = '' OR t.status = $2)`
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM tax_slabs t`+where, p.Q, status).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, taxSlabSelect+where+` ORDER BY `+p.OrderBy+`, t.id LIMIT $3 OFFSET $4`, p.Q, status, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanTaxSlab(rows)
			if err != nil {
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
	httpx.JSON(w, http.StatusOK, p.Envelope("tax_slabs", list, total))
}

func (h *TaxonomyHandler) CreateTaxSlab(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createTaxSlabRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name is required")
		return
	}
	if !validRates(req.CGSTRate, req.SGSTRate, req.IGSTRate, req.CessRate) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "rates must be between 0 and 100")
		return
	}

	var resp taxSlabResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO tax_slabs (id, merchant_id, name, cgst_rate, sgst_rate, igst_rate, cess_rate)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5)
			RETURNING id`,
			strings.TrimSpace(req.Name), req.CGSTRate, req.SGSTRate, req.IGSTRate, req.CessRate,
		).Scan(&id); err != nil {
			return err
		}
		var err error
		resp, err = scanTaxSlab(tx.QueryRow(ctx, taxSlabSelect+` WHERE t.id = $1`, id))
		return err
	})
	if err != nil {
		respondTaxonomyError(w, err, "tax slab")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// UpdateTaxSlab: PATCH /tax-slabs/{id} {name?, status?} — deliberately
// not the rates. A sale line stores its own computed tax, so history is
// safe either way, but silently changing what "GST 12%" means for every
// product on it the next time they're scanned is the kind of change that
// should be a new slab plus a product reassignment the merchant can see,
// not an in-place edit. Create the new slab and deactivate the old one.
func (h *TaxonomyHandler) UpdateTaxSlab(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var req updateTaxonomyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if !req.validate(w) {
		return
	}
	var resp taxSlabResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var updated string
		if err := tx.QueryRow(ctx, `
			UPDATE tax_slabs SET name = COALESCE($1, name), status = COALESCE($2, status), updated_at = now()
			WHERE id::text = $3 RETURNING id`, req.Name, req.Status, id).Scan(&updated); err != nil {
			return err
		}
		var err error
		resp, err = scanTaxSlab(tx.QueryRow(ctx, taxSlabSelect+` WHERE t.id = $1`, updated))
		return err
	})
	if err != nil {
		respondTaxonomyError(w, err, "tax slab")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// DeleteTaxSlab: DELETE /tax-slabs/{id} — 204, or 409 IN_USE with counts.
func (h *TaxonomyHandler) DeleteTaxSlab(w http.ResponseWriter, r *http.Request) {
	h.deleteIfUnused(w, r, "tax_slabs", "tax slab")
}
