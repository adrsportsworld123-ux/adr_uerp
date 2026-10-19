package sales

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// An open-price line's own description (migrations/062): what the item
// actually was, typed at the till ("Custom engraving — 3 lines"). It
// prints on the receipt and invoice in place of the catalog name; only an
// open-price line may carry one.

const maxDescription = 120

var (
	errDescriptionNotAllowed = errors.New("only an open-price item can have its own description")
	errDescriptionTooLong    = errors.New("a description can be at most 120 characters")
)

// cleanDescription trims a description and collapses its spaces; "" means none.
func cleanDescription(s *string) (*string, error) {
	if s == nil {
		return nil, nil
	}
	v := strings.Join(strings.Fields(*s), " ")
	if v == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(v) > maxDescription {
		return nil, errDescriptionTooLong
	}
	return &v, nil
}

func writeDescriptionError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, errDescriptionNotAllowed):
		httpx.Error(w, http.StatusBadRequest, "DESCRIPTION_NOT_ALLOWED", err.Error())
	case errors.Is(err, errDescriptionTooLong):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
	default:
		return false
	}
	return true
}

// SetLineDescription: PUT /sales/orders/{id}/lines/{line_id}/description —
// {"description": "..."}; null or "" goes back to the catalog name. A cart
// or held order's open-price line only. Changes nothing else on the line.
func (h *Handler) SetLineDescription(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID, lineID := chi.URLParam(r, "id"), chi.URLParam(r, "line_id")
	var req struct {
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	desc, err := cleanDescription(req.Description)
	if writeDescriptionError(w, err) {
		return
	}
	var resp orderResponse
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var openPrice bool
		var before *string
		if err := tx.QueryRow(ctx, `
			SELECT so.status, sol.open_price, sol.description
			FROM sales_order_lines sol JOIN sales_orders so ON so.id = sol.sales_order_id
			WHERE sol.id::text = $1 AND so.id::text = $2 FOR UPDATE OF sol`, lineID, orderID).Scan(&status, &openPrice, &before); err != nil {
			return err
		}
		if status != "cart" && status != "held" {
			return errOrderNotEditable
		}
		if !openPrice {
			return errDescriptionNotAllowed
		}
		if _, err := tx.Exec(ctx, `UPDATE sales_order_lines SET description = $2 WHERE id::text = $1`, lineID, desc); err != nil {
			return err
		}
		if err := loadOrder(ctx, tx, orderID, &resp); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "sales_order_line", EntityID: lineID, Action: "update", PerformedBy: claims.UserID,
			Before: map[string]any{"description": before}, After: map[string]any{"description": desc}, Reason: "open-price description"})
	})
	if writeDescriptionError(w, err) {
		return
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "line not found")
	case errors.Is(err, errOrderNotEditable):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "the description can only change before checkout")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save the description")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}
