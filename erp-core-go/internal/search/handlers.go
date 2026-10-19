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

	// The POS names its branch: a branch that lists open-price items
	// first (migrations/063, app_open_price_first) gets them first, on
	// the full grid and within any category. Without a branch (the back
	// office's search) the order is relevance, as before.
	openPriceFirst := false
	if b := q.Get("branch_id"); b != "" {
		err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT app_open_price_first(id) FROM branches WHERE id::text = $1`, b).Scan(&openPriceFirst)
		})
		if err != nil && err != pgx.ErrNoRows {
			httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not read the branch's settings")
			return
		}
	}

	result, err := h.Client.Search(r.Context(), Params{
		TenantID:       claims.TenantID,
		Query:          q.Get("q"),
		CategoryIDs:    categoryIDs,
		BrandID:        q.Get("brand_id"),
		MinPrice:       q.Get("min_price"),
		MaxPrice:       q.Get("max_price"),
		From:           (page - 1) * limit,
		Size:           limit,
		OpenPriceFirst: openPriceFirst,
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
	type tile struct {
		openPrice bool
		color     string
	}
	tiles := map[string]tile{}
	if err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if stock, err = db.StockByVariant(ctx, tx, ids, q.Get("branch_id")); err != nil {
			return err
		}
		// The open-price flag and tile colour as they are now, whatever
		// the index last saw (migrations/063).
		rows, err := tx.Query(ctx, `
			SELECT v.id::text, v.open_price, COALESCE(p.pos_color, '')
			FROM product_variants v JOIN products p ON p.id = v.product_id
			WHERE v.id::text = ANY($1)`, ids)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var t tile
			if err := rows.Scan(&id, &t.openPrice, &t.color); err != nil {
				return err
			}
			tiles[id] = t
		}
		return rows.Err()
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
		if t, ok := tiles[hit.VariantID]; ok {
			hit.OpenPrice, hit.POSColor = t.openPrice, t.color
		}
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
