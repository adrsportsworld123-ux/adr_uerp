package inventory

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// VariantStock: GET /inventory/variants/{id}/branches?branch_id= — Phase
// 13's POS product lookup: one variant's stock in every active branch of
// the merchant (so a cashier can tell a customer "MG Road has 4"), and,
// for branch_id, its batches with stock, soonest expiry first (what the
// Detailed label prints, and what sells next). Read-only; any signed-in
// user, like GET /inventory.
func (h *Handler) VariantStock(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	variantID := chi.URLParam(r, "id")
	branchID := r.URL.Query().Get("branch_id")

	type branchStock struct {
		BranchID     string `json:"branch_id"`
		BranchName   string `json:"branch_name"`
		OnHand       string `json:"on_hand"`
		Reserved     string `json:"reserved"`
		Available    string `json:"available"`
		ReorderPoint string `json:"reorder_point"`
	}
	type batch struct {
		BatchID   string  `json:"batch_id"`
		BatchNo   string  `json:"batch_no"`
		Expiry    *string `json:"expiry_date"`
		Remaining string  `json:"quantity_remaining"`
		Expired   bool    `json:"expired"`
	}
	branches := []branchStock{}
	batches := []batch{}
	var exists bool
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM product_variants WHERE id::text = $1)`, variantID).Scan(&exists); err != nil || !exists {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT b.id, b.name, COALESCE(sl.on_hand, 0)::text, COALESCE(sl.reserved, 0)::text,
			       COALESCE(sl.on_hand - sl.reserved, 0)::text, COALESCE(sl.reorder_point, 0)::text
			FROM branches b
			LEFT JOIN stock_levels sl ON sl.branch_id = b.id AND sl.variant_id::text = $1
			WHERE b.status = 'active' AND app_branch_visible(b.id) -- Phase 15: only branches this user may see
			ORDER BY (b.id::text = $2) DESC, b.name`, variantID, branchID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var s branchStock
			if err := rows.Scan(&s.BranchID, &s.BranchName, &s.OnHand, &s.Reserved, &s.Available, &s.ReorderPoint); err != nil {
				rows.Close()
				return err
			}
			branches = append(branches, s)
		}
		rows.Close()
		if err := rows.Err(); err != nil || branchID == "" {
			return err
		}
		rows, err = tx.Query(ctx, `
			SELECT id, batch_no, expiry_date::text, quantity_remaining::text, COALESCE(expiry_date < current_date, false)
			FROM stock_batches
			WHERE variant_id::text = $1 AND branch_id::text = $2 AND quantity_remaining > 0
			ORDER BY expiry_date NULLS LAST, received_at`, variantID, branchID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b batch
			if err := rows.Scan(&b.BatchID, &b.BatchNo, &b.Expiry, &b.Remaining, &b.Expired); err != nil {
				return err
			}
			batches = append(batches, b)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load stock")
		return
	}
	if !exists {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "variant not found")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"variant_id": variantID, "branches": branches, "batches": batches})
}
