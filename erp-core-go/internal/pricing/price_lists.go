package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// Phase 7: wholesale price lists at scale (migrations/026_wholesale_b2b.sql).
// A price list is just a named set of per-variant override prices — a
// customer assigned to one (customers.price_list_id, edited via
// PATCH /customers/{id}) gets that price automatically wherever
// ResolvePrice (resolve.go) is called, which today is both POS add-line
// and quotation creation. Reads are open to any authenticated user (a
// sales rep building a quote needs to see wholesale prices); only
// creating/editing a list needs pricing.manage, the same permission that
// already gates changing a single variant's price — this is the same
// class of "getting a price wrong costs real money" decision.

type priceListSummary struct {
	ID        string `json:"price_list_id"`
	Name      string `json:"name"`
	ItemCount int    `json:"item_count"`
}

type priceListItem struct {
	VariantID string `json:"variant_id"`
	SKU       string `json:"sku"`
	Product   string `json:"product_name"`
	Price     string `json:"price"`
}

type priceListDetail struct {
	ID    string          `json:"price_list_id"`
	Name  string          `json:"name"`
	Items []priceListItem `json:"items"`
}

type createPriceListRequest struct {
	Name string `json:"name"`
}

var errPriceListInUse = errors.New("price list is assigned to one or more customers")

// CreatePriceList: POST /pricing/price-lists
func (h *Handler) CreatePriceList(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createPriceListRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name is required")
		return
	}

	var id string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO price_lists (id, merchant_id, name)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1)
			RETURNING id`, req.Name).Scan(&id)
	})
	switch {
	case isUniqueViolation(err):
		httpx.Error(w, http.StatusConflict, "PRICE_LIST_NAME_EXISTS", "a price list with this name already exists")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create price list")
	default:
		httpx.JSON(w, http.StatusCreated, priceListSummary{ID: id, Name: req.Name})
	}
}

// ListPriceLists: GET /pricing/price-lists
func (h *Handler) ListPriceLists(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	lists := []priceListSummary{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT pl.id::text, pl.name, COUNT(pli.id)
			FROM price_lists pl
			LEFT JOIN price_list_items pli ON pli.price_list_id = pl.id
			GROUP BY pl.id, pl.name
			ORDER BY pl.name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l priceListSummary
			if err := rows.Scan(&l.ID, &l.Name, &l.ItemCount); err != nil {
				return err
			}
			lists = append(lists, l)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list price lists")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"price_lists": lists})
}

// GetPriceList: GET /pricing/price-lists/{id}
func (h *Handler) GetPriceList(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	listID := chi.URLParam(r, "id")

	var detail priceListDetail
	detail.ID = listID
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM price_lists WHERE id = $1`, listID).Scan(&detail.Name); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT pli.variant_id::text, pv.sku, p.name, pli.price::text
			FROM price_list_items pli
			JOIN product_variants pv ON pv.id = pli.variant_id
			JOIN products p ON p.id = pv.product_id
			WHERE pli.price_list_id = $1
			ORDER BY p.name`, listID)
		if err != nil {
			return err
		}
		defer rows.Close()
		detail.Items = []priceListItem{}
		for rows.Next() {
			var it priceListItem
			if err := rows.Scan(&it.VariantID, &it.SKU, &it.Product, &it.Price); err != nil {
				return err
			}
			detail.Items = append(detail.Items, it)
		}
		return rows.Err()
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "price list not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load price list")
	default:
		httpx.JSON(w, http.StatusOK, detail)
	}
}

type upsertPriceListItemRequest struct {
	Price float64 `json:"price"`
}

// UpsertPriceListItem: PUT /pricing/price-lists/{id}/items/{variant_id}
func (h *Handler) UpsertPriceListItem(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	listID := chi.URLParam(r, "id")
	variantID := chi.URLParam(r, "variant_id")

	var req upsertPriceListItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Price < 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a non-negative price is required")
		return
	}

	var item priceListItem
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var listExists string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM price_lists WHERE id = $1`, listID).Scan(&listExists); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT pv.sku, p.name FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $1`, variantID).
			Scan(&item.SKU, &item.Product); err != nil {
			return err
		}
		item.VariantID = variantID
		return tx.QueryRow(ctx, `
			INSERT INTO price_list_items (id, price_list_id, variant_id, price)
			VALUES (gen_random_uuid(), $1, $2, $3)
			ON CONFLICT (price_list_id, variant_id) DO UPDATE SET price = EXCLUDED.price, updated_at = now()
			RETURNING price::text`, listID, variantID, req.Price,
		).Scan(&item.Price)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "price list or variant not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not set price list item")
	default:
		httpx.JSON(w, http.StatusOK, item)
	}
}

// DeletePriceListItem: DELETE /pricing/price-lists/{id}/items/{variant_id}
func (h *Handler) DeletePriceListItem(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	listID := chi.URLParam(r, "id")
	variantID := chi.URLParam(r, "variant_id")

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM price_list_items WHERE price_list_id = $1 AND variant_id = $2`, listID, variantID)
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
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "price list item not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not remove price list item")
	default:
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// DeletePriceList: DELETE /pricing/price-lists/{id} — refuses if any
// customer is currently assigned to it, the same "don't silently strand a
// dependent record" reasoning as rbac.DeleteRole's ROLE_IN_USE.
func (h *Handler) DeletePriceList(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	listID := chi.URLParam(r, "id")

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var customerCount int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM customers WHERE price_list_id = $1`, listID).Scan(&customerCount); err != nil {
			return err
		}
		if customerCount > 0 {
			return errPriceListInUse
		}
		tag, err := tx.Exec(ctx, `DELETE FROM price_lists WHERE id = $1`, listID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})
	switch {
	case errors.Is(err, errPriceListInUse):
		httpx.Error(w, http.StatusConflict, "PRICE_LIST_IN_USE", "one or more customers are assigned to this price list — reassign them first")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "price list not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not delete price list")
	default:
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
