package branches

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

var (
	errWrongStatus = errors.New("transfer is not in the right status for this action")
	errStockShort  = errors.New("source branch does not have enough available stock")
)

type transferLineResp struct {
	LineID            string  `json:"line_id"`
	VariantID         string  `json:"variant_id"`
	SKU               string  `json:"sku"`
	ProductName       string  `json:"product_name"`
	RequestedQuantity string  `json:"requested_quantity"`
	SentQuantity      *string `json:"sent_quantity"`
	ReceivedQuantity  *string `json:"received_quantity"`
}

type transferResp struct {
	TransferID      string             `json:"transfer_id"`
	TransferNumber  string             `json:"transfer_number"`
	Status          string             `json:"status"`
	FromBranchID    string             `json:"from_branch_id"`
	ToBranchID      string             `json:"to_branch_id"`
	Notes           string             `json:"notes"`
	RejectionReason string             `json:"rejection_reason,omitempty"`
	Lines           []transferLineResp `json:"lines"`
}

// ---------------------------------------------------------------------
// POST /branch-transfers
// ---------------------------------------------------------------------

type transferLineReq struct {
	VariantID string  `json:"variant_id"`
	Quantity  float64 `json:"quantity"`
}

type createTransferReq struct {
	FromBranchID string            `json:"from_branch_id"`
	ToBranchID   string            `json:"to_branch_id"`
	Notes        string            `json:"notes"`
	Lines        []transferLineReq `json:"lines"`
}

func (h *Handler) CreateTransfer(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createTransferReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.FromBranchID == "" || req.ToBranchID == "" || req.FromBranchID == req.ToBranchID || len(req.Lines) == 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "from_branch_id, to_branch_id (different from each other) and at least one line are required")
		return
	}

	transferNumber := fmt.Sprintf("XFER-%d", time.Now().UnixNano())
	var resp transferResp
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var transferID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO branch_transfers (id, merchant_id, from_branch_id, to_branch_id, transfer_number, notes, requested_by)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5)
			RETURNING id`,
			req.FromBranchID, req.ToBranchID, transferNumber, req.Notes, claims.UserID,
		).Scan(&transferID); err != nil {
			return err
		}
		for _, l := range req.Lines {
			if _, err := tx.Exec(ctx, `
				INSERT INTO branch_transfer_lines (id, branch_transfer_id, variant_id, requested_quantity)
				VALUES (gen_random_uuid(), $1, $2, $3)`, transferID, l.VariantID, l.Quantity); err != nil {
				return err
			}
		}
		return loadTransfer(ctx, tx, transferID, &resp)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create transfer")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// ---------------------------------------------------------------------
// GET /branch-transfers, GET /branch-transfers/{id}
// ---------------------------------------------------------------------

type transferSummary struct {
	TransferID     string `json:"transfer_id"`
	TransferNumber string `json:"transfer_number"`
	Status         string `json:"status"`
	FromBranchID   string `json:"from_branch_id"`
	ToBranchID     string `json:"to_branch_id"`
	CreatedAt      string `json:"created_at"`
}

func (h *Handler) ListTransfers(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")
	status := r.URL.Query().Get("status")

	transfers := []transferSummary{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, transfer_number, status, from_branch_id, to_branch_id, created_at::text
			FROM branch_transfers
			WHERE ($1 = '' OR from_branch_id::text = $1 OR to_branch_id::text = $1) AND ($2 = '' OR status = $2)
			ORDER BY created_at DESC`, branchID, status)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t transferSummary
			if err := rows.Scan(&t.TransferID, &t.TransferNumber, &t.Status, &t.FromBranchID, &t.ToBranchID, &t.CreatedAt); err != nil {
				return err
			}
			transfers = append(transfers, t)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list transfers")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"transfers": transfers})
}

func (h *Handler) GetTransfer(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	transferID := chi.URLParam(r, "id")
	var resp transferResp
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return loadTransfer(ctx, tx, transferID, &resp)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "transfer not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load transfer")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------
// POST /branch-transfers/{id}/approve, /reject — gated by
// authn.RequirePermission("branch_transfer.approve") at the router level
// ---------------------------------------------------------------------

func (h *Handler) ApproveTransfer(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	transferID := chi.URLParam(r, "id")
	var resp transferResp
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM branch_transfers WHERE id = $1`, transferID).Scan(&status); err != nil {
			return err
		}
		if status != "pending_approval" {
			return errWrongStatus
		}
		if _, err := tx.Exec(ctx, `
			UPDATE branch_transfers SET status = 'approved', approved_by = $1, approved_at = now() WHERE id = $2`,
			claims.UserID, transferID); err != nil {
			return err
		}
		return loadTransfer(ctx, tx, transferID, &resp)
	})
	respondTransferAction(w, err, resp)
}

type rejectReq struct {
	Reason string `json:"reason"`
}

func (h *Handler) RejectTransfer(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	transferID := chi.URLParam(r, "id")
	var req rejectReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Reason == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "reason is required")
		return
	}
	var resp transferResp
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM branch_transfers WHERE id = $1`, transferID).Scan(&status); err != nil {
			return err
		}
		if status != "pending_approval" {
			return errWrongStatus
		}
		if _, err := tx.Exec(ctx, `
			UPDATE branch_transfers SET status = 'rejected', rejection_reason = $1 WHERE id = $2`,
			req.Reason, transferID); err != nil {
			return err
		}
		return loadTransfer(ctx, tx, transferID, &resp)
	})
	respondTransferAction(w, err, resp)
}

// ---------------------------------------------------------------------
// POST /branch-transfers/{id}/cancel — before dispatch only, no stock
// impact to reverse since none was ever moved.
// ---------------------------------------------------------------------

func (h *Handler) CancelTransfer(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	transferID := chi.URLParam(r, "id")
	var resp transferResp
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM branch_transfers WHERE id = $1`, transferID).Scan(&status); err != nil {
			return err
		}
		if status != "pending_approval" && status != "approved" {
			return errWrongStatus
		}
		if _, err := tx.Exec(ctx, `UPDATE branch_transfers SET status = 'cancelled' WHERE id = $1`, transferID); err != nil {
			return err
		}
		return loadTransfer(ctx, tx, transferID, &resp)
	})
	respondTransferAction(w, err, resp)
}

// ---------------------------------------------------------------------
// POST /branch-transfers/{id}/dispatch — all-or-nothing: every line must
// have enough available stock (on_hand - reserved) at the source branch,
// or none of it is dispatched. Deducts on_hand immediately (goods
// physically leave), same as the FRD's "stock deducted from A."
// ---------------------------------------------------------------------

func (h *Handler) DispatchTransfer(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	transferID := chi.URLParam(r, "id")
	var resp transferResp
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status, fromBranch string
		if err := tx.QueryRow(ctx, `SELECT status, from_branch_id FROM branch_transfers WHERE id = $1`, transferID).
			Scan(&status, &fromBranch); err != nil {
			return err
		}
		if status != "approved" {
			return errWrongStatus
		}

		rows, err := tx.Query(ctx, `SELECT id, variant_id, requested_quantity FROM branch_transfer_lines WHERE branch_transfer_id = $1`, transferID)
		if err != nil {
			return err
		}
		type line struct {
			id, variantID string
			quantity      float64
		}
		var lines []line
		for rows.Next() {
			var l line
			if err := rows.Scan(&l.id, &l.variantID, &l.quantity); err != nil {
				rows.Close()
				return err
			}
			lines = append(lines, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		// Check availability for every line before touching any stock —
		// all-or-nothing, not a partial dispatch.
		for _, l := range lines {
			var onHand, reserved float64
			if err := tx.QueryRow(ctx, `SELECT on_hand, reserved FROM stock_levels WHERE branch_id = $1 AND variant_id = $2`,
				fromBranch, l.variantID).Scan(&onHand, &reserved); err != nil {
				if err == pgx.ErrNoRows {
					return errStockShort
				}
				return err
			}
			if onHand-reserved < l.quantity {
				return errStockShort
			}
		}

		for _, l := range lines {
			if _, err := tx.Exec(ctx, `
				UPDATE stock_levels SET on_hand = on_hand - $1, version = version + 1, updated_at = now()
				WHERE branch_id = $2 AND variant_id = $3`, l.quantity, fromBranch, l.variantID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO stock_movements (merchant_id, branch_id, variant_id, movement_type, quantity_delta, reference_type, reference_id, performed_by)
				VALUES (current_setting('app.tenant_id')::uuid, $1, $2, 'transfer_out', $3, 'branch_transfer', $4, $5)`,
				fromBranch, l.variantID, -l.quantity, transferID, claims.UserID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE branch_transfer_lines SET sent_quantity = $1 WHERE id = $2`, l.quantity, l.id); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE branch_transfers SET status = 'in_transit', dispatched_by = $1, dispatched_at = now() WHERE id = $2`,
			claims.UserID, transferID); err != nil {
			return err
		}
		return loadTransfer(ctx, tx, transferID, &resp)
	})

	switch {
	case errors.Is(err, errStockShort):
		httpx.Error(w, http.StatusConflict, "STOCK_UNAVAILABLE", "source branch does not have enough available stock for one or more lines")
	default:
		respondTransferAction(w, err, resp)
	}
}

// ---------------------------------------------------------------------
// POST /branch-transfers/{id}/complete — adds stock at the destination.
// received_quantity defaults to sent_quantity per line unless the request
// overrides it (a discrepancy — breakage/loss in transit). A shortfall is
// valued at the variant's current cost_price and journaled as an
// Inventory Shrinkage loss, the same plug account inventory adjustments
// use — see migrations/008's header comment for why a normal, no-
// discrepancy transfer posts no journal entry at all.
// ---------------------------------------------------------------------

type completeLineReq struct {
	LineID           string   `json:"line_id"`
	ReceivedQuantity *float64 `json:"received_quantity"`
}

type completeTransferReq struct {
	Lines []completeLineReq `json:"lines"`
}

func (h *Handler) CompleteTransfer(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	transferID := chi.URLParam(r, "id")

	var req completeTransferReq
	_ = json.NewDecoder(r.Body).Decode(&req) // body is optional — no overrides means "received exactly what was sent"
	overrides := map[string]float64{}
	for _, l := range req.Lines {
		if l.ReceivedQuantity != nil {
			overrides[l.LineID] = *l.ReceivedQuantity
		}
	}

	var resp transferResp
	var discrepancyValue float64
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status, toBranch string
		if err := tx.QueryRow(ctx, `SELECT status, to_branch_id FROM branch_transfers WHERE id = $1`, transferID).
			Scan(&status, &toBranch); err != nil {
			return err
		}
		if status != "in_transit" {
			return errWrongStatus
		}

		rows, err := tx.Query(ctx, `SELECT id, variant_id, sent_quantity FROM branch_transfer_lines WHERE branch_transfer_id = $1`, transferID)
		if err != nil {
			return err
		}
		type line struct {
			id, variantID string
			sentQuantity  float64
		}
		var lines []line
		for rows.Next() {
			var l line
			if err := rows.Scan(&l.id, &l.variantID, &l.sentQuantity); err != nil {
				rows.Close()
				return err
			}
			lines = append(lines, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, l := range lines {
			received := l.sentQuantity
			if override, ok := overrides[l.id]; ok {
				received = override
			}

			if _, err := tx.Exec(ctx, `
				INSERT INTO stock_levels (id, merchant_id, branch_id, variant_id, on_hand, reserved, version)
				VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, 0, 0)
				ON CONFLICT (branch_id, variant_id) DO UPDATE SET
				  on_hand = stock_levels.on_hand + $3, version = stock_levels.version + 1, updated_at = now()`,
				toBranch, l.variantID, received); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO stock_movements (merchant_id, branch_id, variant_id, movement_type, quantity_delta, reference_type, reference_id, performed_by)
				VALUES (current_setting('app.tenant_id')::uuid, $1, $2, 'transfer_in', $3, 'branch_transfer', $4, $5)`,
				toBranch, l.variantID, received, transferID, claims.UserID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE branch_transfer_lines SET received_quantity = $1 WHERE id = $2`, received, l.id); err != nil {
				return err
			}

			if shortfall := l.sentQuantity - received; shortfall != 0 {
				var costPrice float64
				if err := tx.QueryRow(ctx, `SELECT cost_price FROM product_variants WHERE id = $1`, l.variantID).Scan(&costPrice); err != nil {
					return err
				}
				discrepancyValue += shortfall * costPrice
			}
		}

		if discrepancyValue != 0 {
			lines := []accounting.JournalLine{
				{AccountCode: accounting.AccountInventoryLoss, Debit: posOrZero(discrepancyValue), Credit: posOrZero(-discrepancyValue)},
				{AccountCode: accounting.AccountInventory, Debit: posOrZero(-discrepancyValue), Credit: posOrZero(discrepancyValue)},
			}
			if _, err := accounting.PostJournalEntry(ctx, tx, toBranch, "inventory_adjustment", transferID,
				"Transfer discrepancy (transit loss/breakage)", claims.UserID, lines); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE branch_transfers SET status = 'completed', completed_by = $1, completed_at = now() WHERE id = $2`,
			claims.UserID, transferID); err != nil {
			return err
		}
		return loadTransfer(ctx, tx, transferID, &resp)
	})
	respondTransferAction(w, err, resp)
}

func posOrZero(v float64) float64 {
	if v > 0 {
		return v
	}
	return 0
}

func respondTransferAction(w http.ResponseWriter, err error, resp transferResp) {
	switch {
	case errors.Is(err, errWrongStatus):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "transfer is not in the right status for this action")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "transfer not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update transfer")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

func loadTransfer(ctx context.Context, tx pgx.Tx, transferID string, resp *transferResp) error {
	if err := tx.QueryRow(ctx, `
		SELECT id, transfer_number, status, from_branch_id, to_branch_id, COALESCE(notes,''), COALESCE(rejection_reason,'')
		FROM branch_transfers WHERE id = $1`, transferID,
	).Scan(&resp.TransferID, &resp.TransferNumber, &resp.Status, &resp.FromBranchID, &resp.ToBranchID, &resp.Notes, &resp.RejectionReason); err != nil {
		return err
	}

	rows, err := tx.Query(ctx, `
		SELECT btl.id, btl.variant_id, pv.sku, p.name, btl.requested_quantity::text,
		       btl.sent_quantity::text, btl.received_quantity::text
		FROM branch_transfer_lines btl
		JOIN product_variants pv ON pv.id = btl.variant_id
		JOIN products p ON p.id = pv.product_id
		WHERE btl.branch_transfer_id = $1
		ORDER BY btl.created_at`, transferID)
	if err != nil {
		return err
	}
	defer rows.Close()

	resp.Lines = []transferLineResp{}
	for rows.Next() {
		var l transferLineResp
		var sent, received *string
		if err := rows.Scan(&l.LineID, &l.VariantID, &l.SKU, &l.ProductName, &l.RequestedQuantity, &sent, &received); err != nil {
			return err
		}
		l.SentQuantity = sent
		l.ReceivedQuantity = received
		resp.Lines = append(resp.Lines, l)
	}
	return rows.Err()
}
