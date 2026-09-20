package search

import (
	"net/http"
	"strconv"

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

	result, err := h.Client.Search(r.Context(), Params{
		TenantID:   claims.TenantID,
		Query:      q.Get("q"),
		CategoryID: q.Get("category_id"),
		BrandID:    q.Get("brand_id"),
		MinPrice:   q.Get("min_price"),
		MaxPrice:   q.Get("max_price"),
		From:       (page - 1) * limit,
		Size:       limit,
	})
	if err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "SEARCH_UNAVAILABLE", "product search is temporarily unavailable")
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"results": result.Hits,
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
