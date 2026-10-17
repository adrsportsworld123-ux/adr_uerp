package search

import (
	"context"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB     *db.DB
	Client *Client
}

// Search: GET /products/search?q=&category_id=&brand_id=&min_price=&max_price=&page=&limit=
// The OpenSearch-backed counterpart to catalog.ListHandler.ListProducts —
// that endpoint stays as the plain-Postgres browse/pricing view; this one
// is the free-text, typo-tolerant search the FRD's <=100ms target is
// actually about, and is what erp-web-admin's /search screen calls.
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	if !h.Client.Enabled() {
		httpx.Error(w, http.StatusServiceUnavailable, "SEARCH_UNAVAILABLE", "product search is not configured")
		return
	}

	q := r.URL.Query()
	limit := 50
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 && l <= 200 {
		limit = l
	}
	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}

	// A category means it and its subcategories, as on GET /products:
	// the index holds each product's own (usually leaf) category only.
	var categoryIDs []string
	if id := q.Get("category_id"); id != "" {
		err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			categoryIDs, err = db.CategorySubtree(ctx, tx, id)
			return err
		})
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not read categories")
			return
		}
		if categoryIDs == nil {
			categoryIDs = []string{}
		}
	}

	result, err := h.Client.Search(r.Context(), Params{
		TenantID:    claims.TenantID,
		Query:       q.Get("q"),
		CategoryIDs: categoryIDs,
		BrandID:     q.Get("brand_id"),
		MinPrice:    q.Get("min_price"),
		MaxPrice:    q.Get("max_price"),
		From:        (page - 1) * limit,
		Size:        limit,
	})
	if err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "SEARCH_UNAVAILABLE", "product search is temporarily unavailable")
		return
	}

	// Live quantities beside each hit (db.StockByVariant): the index holds
	// the catalogue, never stock, which changes with every sale.
	ids := make([]string, len(result.Hits))
	for i, hit := range result.Hits {
		ids[i] = hit.VariantID
	}
	var stock map[string]db.Stock
	if err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		stock, err = db.StockByVariant(ctx, tx, ids, q.Get("branch_id"))
		return err
	}); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not read stock")
		return
	}
	type hitWithStock struct {
		Hit
		db.Stock
	}
	hits := make([]hitWithStock, len(result.Hits))
	for i, hit := range result.Hits {
		hits[i] = hitWithStock{Hit: hit, Stock: stock[hit.VariantID]}
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"results": hits,
		"total":   result.Total,
		"page":    page,
		"limit":   limit,
	})
}

// Reindex: POST /search/reindex — rebuilds the caller's tenant's slice of
// the index from Postgres. Gated by search.reindex (see
// migrations/010_search.sql) since it's a bulk operation, not a
// day-to-day one. Safe to run anytime: OpenSearch is a derived read
// model, and this is a full overwrite of every existing document, not an
// additive one, so a reindex fixes drift rather than causing it.
func (h *Handler) Reindex(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	if !h.Client.Enabled() {
		httpx.Error(w, http.StatusServiceUnavailable, "SEARCH_UNAVAILABLE", "product search is not configured")
		return
	}

	docs, err := FetchAllDocuments(r.Context(), h.DB, claims.TenantID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not read catalog")
		return
	}
	if err := h.Client.EnsureIndex(r.Context()); err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "SEARCH_UNAVAILABLE", "could not prepare search index")
		return
	}
	if err := h.Client.BulkIndex(r.Context(), docs); err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "SEARCH_UNAVAILABLE", "could not write to search index")
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"indexed": len(docs)})
}
