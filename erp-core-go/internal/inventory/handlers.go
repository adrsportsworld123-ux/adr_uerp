// Package inventory implements the two Phase 1 inventory endpoints that
// AddLine's automatic reservation flow doesn't cover on its own: querying
// current stock, and manually correcting it with an audit trail
// (phase0_1_design.md §3.3, phased_roadmap.md's "stock adjustments with
// audit trail").
package inventory

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB *db.DB
}

type stockResponse struct {
	BranchID  string `json:"branch_id"`
	VariantID string `json:"variant_id"`
	OnHand    string `json:"on_hand"`
	Reserved  string `json:"reserved"`
	Available string `json:"available"`
}

// GetStock: GET /inventory?branch_id=&variant_id=
func (h *Handler) GetStock(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")
	variantID := r.URL.Query().Get("variant_id")
	if branchID == "" || variantID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id and variant_id are required")
		return
	}

	var resp stockResponse
	resp.BranchID = branchID
	resp.VariantID = variantID
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT on_hand::text, reserved::text, (on_hand - reserved)::text
			FROM stock_levels WHERE branch_id = $1 AND variant_id = $2`, branchID, variantID,
		).Scan(&resp.OnHand, &resp.Reserved, &resp.Available)
	})
	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no stock record for this branch/variant")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load stock")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

type adjustmentRequest struct {
	VariantID     string  `json:"variant_id"`
	BranchID      string  `json:"branch_id"`
	QuantityDelta float64 `json:"quantity_delta"`
	Reason        string  `json:"reason"`
}

// AdjustStock: POST /inventory/adjustments — manual correction, writes a
// stock_movements row and an audit_logs row (before/after on_hand).
//
// Authorization is enforced by authn.RequirePermission("inventory.adjust")
// at the router level (see internal/httpserver/router.go) — the general
// permission-code mechanism this handler used to gate by role name as an
// interim measure before that middleware existed.
func (h *Handler) AdjustStock(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}

	var req adjustmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.VariantID == "" || req.BranchID == "" || req.QuantityDelta == 0 || req.Reason == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "variant_id, branch_id, a non-zero quantity_delta and reason are required")
		return
	}

	var resp stockResponse
	resp.BranchID = req.BranchID
	resp.VariantID = req.VariantID
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		// Best-effort "before" value for the audit log only — if no row exists
		// yet (first stock for this branch/variant), before stays 0, which is
		// the correct value anyway.
		var before float64
		_ = tx.QueryRow(ctx, `SELECT on_hand FROM stock_levels WHERE branch_id = $1 AND variant_id = $2`, req.BranchID, req.VariantID).Scan(&before)

		var onHand, reserved float64
		if err := tx.QueryRow(ctx, `
			INSERT INTO stock_levels (id, merchant_id, branch_id, variant_id, on_hand, reserved, version)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, 0, 0)
			ON CONFLICT (branch_id, variant_id) DO UPDATE SET
			  on_hand = stock_levels.on_hand + EXCLUDED.on_hand,
			  version = stock_levels.version + 1,
			  updated_at = now()
			RETURNING on_hand, reserved`, req.BranchID, req.VariantID, req.QuantityDelta,
		).Scan(&onHand, &reserved); err != nil {
			return err
		}
		resp.OnHand = formatQty(onHand)
		resp.Reserved = formatQty(reserved)
		resp.Available = formatQty(onHand - reserved)

		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_movements (merchant_id, branch_id, variant_id, movement_type, quantity_delta, reference_type, reason, performed_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, 'adjustment', $3, 'adjustment', $4, $5)`,
			req.BranchID, req.VariantID, req.QuantityDelta, req.Reason, claims.UserID); err != nil {
			return err
		}

		beforeJSON, _ := json.Marshal(map[string]float64{"on_hand": before})
		afterJSON, _ := json.Marshal(map[string]float64{"on_hand": onHand})
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_logs (merchant_id, entity_type, entity_id, action, performed_by, before_value, after_value, reason)
			VALUES (current_setting('app.tenant_id')::uuid, 'stock_levels', $1, 'update', $2, $3, $4, $5)`,
			req.VariantID, claims.UserID, beforeJSON, afterJSON, req.Reason); err != nil {
			return err
		}

		// Values the adjustment at the variant's current (WAC) cost and
		// books it against Inventory Shrinkage in whichever direction makes
		// the entry balance — this is a simplified model (one plug account
		// for both gains and losses) since the FRD doesn't specify separate
		// gain/loss accounts for adjustments; skipped entirely when
		// cost_price is 0 (nothing ever purchased through a GRN yet) since
		// there's no value to move.
		var costPrice float64
		if err := tx.QueryRow(ctx, `SELECT cost_price FROM product_variants WHERE id = $1`, req.VariantID).Scan(&costPrice); err != nil {
			return err
		}
		if adjValue := req.QuantityDelta * costPrice; adjValue != 0 {
			lines := []accounting.JournalLine{
				{AccountCode: accounting.AccountInventory, Debit: posOrZero(adjValue), Credit: posOrZero(-adjValue)},
				{AccountCode: accounting.AccountInventoryLoss, Debit: posOrZero(-adjValue), Credit: posOrZero(adjValue)},
			}
			if _, err := accounting.PostJournalEntry(ctx, tx, req.BranchID, "inventory_adjustment", req.VariantID, req.Reason, claims.UserID, lines); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not apply stock adjustment")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// posOrZero returns v if positive, else 0 — used to build a two-sided
// journal line from a single signed value without an if/else per line.
func posOrZero(v float64) float64 {
	if v > 0 {
		return v
	}
	return 0
}

func formatQty(v float64) string {
	return strconv.FormatFloat(v, 'f', 3, 64)
}
