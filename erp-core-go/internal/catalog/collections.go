// Phase 8: Vertical Expansion — Apparel. The one genuinely missing piece
// once attribute-set configuration existed: a product-level grouping
// (not a variant attribute like Size/Color) for a named collection or
// season, the way a real apparel retailer organizes a catalog. Same
// list+create-only, catalog.manage-gated shape as categories/brands/
// tax-slabs (taxonomy.go) — this is unblocking apparel catalog browsing,
// not building out full collection-lifecycle management (launch dates,
// markdown schedules, etc.) as its own feature.
package catalog

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

type collectionResponse struct {
	CollectionID string `json:"collection_id"`
	Name         string `json:"name"`
	Season       string `json:"season"`
}

type createCollectionRequest struct {
	Name   string `json:"name"`
	Season string `json:"season"`
}

func (h *TaxonomyHandler) ListCollections(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	list := []collectionResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, COALESCE(season,'') FROM collections ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c collectionResponse
			if err := rows.Scan(&c.CollectionID, &c.Name, &c.Season); err != nil {
				return err
			}
			list = append(list, c)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list collections")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"collections": list})
}

func (h *TaxonomyHandler) CreateCollection(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createCollectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name is required")
		return
	}

	var resp collectionResponse
	resp.Name = req.Name
	resp.Season = req.Season
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO collections (id, merchant_id, name, season)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, NULLIF($2,''))
			RETURNING id`, req.Name, req.Season,
		).Scan(&resp.CollectionID)
	})
	switch {
	case isUniqueViolation(err):
		httpx.Error(w, http.StatusConflict, "COLLECTION_NAME_EXISTS", "a collection with this name already exists")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create collection")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}
