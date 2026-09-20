package purchase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

var errReturnExceedsStock = errors.New("return quantity exceeds on-hand stock")

type purchaseReturnLineRequest struct {
	VariantID string  `json:"variant_id"`
	Quantity  float64 `json:"quantity"`
	UnitCost  float64 `json:"unit_cost"`
}

type createReturnRequest struct {
	SupplierID string                      `json:"supplier_id"`
	BranchID   string                      `json:"branch_id"`
	GRNID      string                      `json:"grn_id"` // optional
	Reason     string                      `json:"reason"`
	Lines      []purchaseReturnLineRequest `json:"lines"`
}

type returnResponse struct {
	ReturnID     string `json:"return_id"`
	ReturnNumber string `json:"return_number"`
	Status       string `json:"status"`
	GrandTotal   string `json:"grand_total"`
}

type returnSummary struct {
	ReturnID     string `json:"return_id"`
	ReturnNumber string `json:"return_number"`
	SupplierID   string `json:"supplier_id"`
	Reason       string `json:"reason"`
	Status       string `json:"status"`
	GrandTotal   string `json:"grand_total"`
	CreatedAt    string `json:"created_at"`
}

// ListPurchaseReturns: GET /purchase/returns
func (h *Handler) ListPurchaseReturns(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	supplierID := r.URL.Query().Get("supplier_id")

	returns := []returnSummary{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, return_number, supplier_id, reason, status, grand_total::text, created_at::text
			FROM purchase_returns
			WHERE ($1 = '' OR supplier_id::text = $1)
			ORDER BY created_at DESC`, supplierID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s returnSummary
			if err := rows.Scan(&s.ReturnID, &s.ReturnNumber, &s.SupplierID, &s.Reason, &s.Status, &s.GrandTotal, &s.CreatedAt); err != nil {
				return err
			}
			returns = append(returns, s)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list purchase returns")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"returns": returns})
}

// CreatePurchaseReturn: POST /purchase/returns — a debit note against a
// supplier. Unlike offline-sync's oversell handling, this is staff
// entering a real-time action themselves (not reconciling something that
// already happened out of our control), so it validates against on-hand
// stock rather than accepting anything and flagging it after the fact —
// a typo'd quantity here is a data-entry mistake worth catching, not a
// fait accompli.
func (h *Handler) CreatePurchaseReturn(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createReturnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.SupplierID == "" || req.BranchID == "" || req.Reason == "" || len(req.Lines) == 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "supplier_id, branch_id, reason and at least one line are required")
		return
	}

	var resp returnResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		returnNumber := fmt.Sprintf("PRET-%d", time.Now().UnixNano())
		var grnID *string
		if req.GRNID != "" {
			grnID = &req.GRNID
		}

		var grandTotal float64
		for _, l := range req.Lines {
			grandTotal += l.Quantity * l.UnitCost
		}

		var returnID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO purchase_returns (id, merchant_id, branch_id, supplier_id, grn_id, return_number, reason, grand_total, created_by)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6, $7)
			RETURNING id`,
			req.BranchID, req.SupplierID, grnID, returnNumber, req.Reason, grandTotal, claims.UserID,
		).Scan(&returnID); err != nil {
			return err
		}

		for _, l := range req.Lines {
			var onHand float64
			if err := tx.QueryRow(ctx, `
				SELECT on_hand FROM stock_levels WHERE branch_id = $1 AND variant_id = $2`, req.BranchID, l.VariantID,
			).Scan(&onHand); err != nil {
				return err
			}
			if onHand < l.Quantity {
				return errReturnExceedsStock
			}

			lineTotal := l.Quantity * l.UnitCost
			if _, err := tx.Exec(ctx, `
				INSERT INTO purchase_return_lines (id, purchase_return_id, variant_id, quantity, unit_cost, line_total)
				VALUES (gen_random_uuid(), $1, $2, $3, $4, $5)`,
				returnID, l.VariantID, l.Quantity, l.UnitCost, lineTotal); err != nil {
				return err
			}

			if _, err := tx.Exec(ctx, `
				UPDATE stock_levels SET on_hand = on_hand - $1, version = version + 1, updated_at = now()
				WHERE branch_id = $2 AND variant_id = $3`, l.Quantity, req.BranchID, l.VariantID); err != nil {
				return err
			}

			if _, err := tx.Exec(ctx, `
				INSERT INTO stock_movements (merchant_id, branch_id, variant_id, movement_type, quantity_delta, reference_type, reference_id, reason, performed_by)
				VALUES (current_setting('app.tenant_id')::uuid, $1, $2, 'adjustment', $3, 'purchase_return', $4, $5, $6)`,
				req.BranchID, l.VariantID, -l.Quantity, returnID, req.Reason, claims.UserID); err != nil {
				return err
			}
		}

		// Reduces both what we owe the supplier and the capitalized value of
		// the returned stock — the mirror image of the bill's Dr Inventory /
		// Cr Payable entry. Skipped when there's nothing to book (a return
		// against a never-billed GRN still adjusts stock above; without a
		// bill there's no payable to reduce, but grand_total still reflects
		// a real value reduction worth recording against Accounts Payable
		// as a credit note the next bill can net against).
		if grandTotal > 0 {
			if _, err := accounting.PostJournalEntry(ctx, tx, req.BranchID, "purchase_return", returnID, "Purchase return "+returnNumber, claims.UserID, []accounting.JournalLine{
				{AccountCode: accounting.AccountPayable, Debit: grandTotal, PartyType: "supplier", PartyID: req.SupplierID},
				{AccountCode: accounting.AccountInventory, Credit: grandTotal},
			}); err != nil {
				return err
			}
		}

		resp.ReturnID = returnID
		resp.ReturnNumber = returnNumber
		resp.Status = "completed"
		resp.GrandTotal = fmt.Sprintf("%.2f", grandTotal)
		return nil
	})

	switch {
	case errors.Is(err, errReturnExceedsStock):
		httpx.Error(w, http.StatusConflict, "STOCK_UNAVAILABLE", "return quantity exceeds on-hand stock for one or more lines")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create purchase return")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}
