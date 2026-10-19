package purchase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/inventory"
	"erp-core-go/internal/softdelete"
)

var errGRNNotEditable = errors.New("grn is not editable")

type grnResponse struct {
	GRNID         string `json:"grn_id"`
	GRNNumber     string `json:"grn_number"`
	Status        string `json:"status"`
	SupplierID    string `json:"supplier_id"`
	BranchID      string `json:"branch_id"`
	FreightAmount string `json:"freight_amount"`
	OtherCharges  string `json:"other_charges"`
	Subtotal      string `json:"subtotal"`
	GrandTotal    string `json:"grand_total"`
	// The purchase order this GRN receives against; nil for a direct GRN.
	PurchaseOrderID *string `json:"purchase_order_id"`
	PONumber        string  `json:"po_number"`
	// The purchase bill (invoice) entered for it, once there is one.
	BillID     *string       `json:"bill_id"`
	BillNumber *string       `json:"bill_number"`
	Lines      []grnLineResp `json:"lines"`
}

type grnLineResp struct {
	LineID         string  `json:"line_id"`
	VariantID      string  `json:"variant_id"`
	SKU            string  `json:"sku"`
	ProductName    string  `json:"product_name"`
	Quantity       string  `json:"quantity"`
	UnitCost       string  `json:"unit_cost"`
	LandedUnitCost string  `json:"landed_unit_cost"`
	LineTotal      string  `json:"line_total"`
	BatchNo        string  `json:"batch_no"`
	ExpiryDate     *string `json:"expiry_date"`
	POLineID       *string `json:"po_line_id"`
}

// ---------------------------------------------------------------------
// POST /purchase/grn — open a draft GRN
// ---------------------------------------------------------------------

type createGRNRequest struct {
	SupplierID    string  `json:"supplier_id"`
	BranchID      string  `json:"branch_id"`
	FreightAmount float64 `json:"freight_amount"`
	OtherCharges  float64 `json:"other_charges"`
	Notes         string  `json:"notes"`
}

func (h *Handler) CreateGRN(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createGRNRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.SupplierID == "" || req.BranchID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "supplier_id and branch_id are required")
		return
	}

	branchPrefix := req.BranchID
	if len(branchPrefix) > 8 {
		branchPrefix = branchPrefix[:8]
	}
	grnNumber := fmt.Sprintf("GRN-%s-%d", branchPrefix, time.Now().UnixNano())

	var resp grnResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO goods_receipt_notes (id, merchant_id, branch_id, supplier_id, grn_number, status, freight_amount, other_charges, received_by)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, 'draft', $4, $5, $6)
			RETURNING id, grn_number, status, supplier_id, branch_id, freight_amount::text, other_charges::text, subtotal::text, grand_total::text`,
			req.BranchID, req.SupplierID, grnNumber, req.FreightAmount, req.OtherCharges, claims.UserID,
		).Scan(&resp.GRNID, &resp.GRNNumber, &resp.Status, &resp.SupplierID, &resp.BranchID,
			&resp.FreightAmount, &resp.OtherCharges, &resp.Subtotal, &resp.GrandTotal)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create GRN")
		return
	}
	resp.Lines = []grnLineResp{}
	httpx.JSON(w, http.StatusCreated, resp)
}

// ---------------------------------------------------------------------
// POST /purchase/grn/{id}/lines
// ---------------------------------------------------------------------

var errBatchRequired = errors.New("batch_no is required for a batch-tracked variant")

type addGRNLineRequest struct {
	VariantID  string  `json:"variant_id"`
	Quantity   float64 `json:"quantity"`
	UnitCost   float64 `json:"unit_cost"`
	BatchNo    string  `json:"batch_no"`    // Phase 8 (Grocery/FMCG) — required if the variant has track_batch = true
	ExpiryDate string  `json:"expiry_date"` // "YYYY-MM-DD", optional even for a batch-tracked variant (not everything expires)
	// POLineID: on a GRN against a purchase order, the PO line received.
	// Optional; omitted = the PO's line for this variant.
	POLineID string `json:"po_line_id"`
}

func (h *Handler) AddGRNLine(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	grnID := chi.URLParam(r, "id")

	var req addGRNLineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.VariantID == "" || req.Quantity <= 0 || req.UnitCost < 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "variant_id, a positive quantity and a non-negative unit_cost are required")
		return
	}

	var resp grnResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var poID *string
		if err := tx.QueryRow(ctx, `SELECT status, purchase_order_id::text FROM goods_receipt_notes WHERE id = $1`, grnID).Scan(&status, &poID); err != nil {
			return err
		}
		if status != "draft" {
			return errGRNNotEditable
		}
		if poID != nil {
			// Against a PO: only its items, never past what is pending.
			lineID := req.POLineID
			if lineID == "" {
				err := tx.QueryRow(ctx, `
					SELECT id::text FROM purchase_order_lines WHERE po_id = $1 AND variant_id::text = $2
					ORDER BY (quantity > received_qty) DESC, created_at LIMIT 1`, *poID, req.VariantID).Scan(&lineID)
				if errors.Is(err, pgx.ErrNoRows) {
					return errPOLineMismatch
				} else if err != nil {
					return err
				}
			}
			if err := insertPOReceiptLine(ctx, tx, *poID, grnID, receivePOLine{
				POLineID: lineID, Quantity: req.Quantity, UnitCost: &req.UnitCost, BatchNo: req.BatchNo, ExpiryDate: req.ExpiryDate,
			}); err != nil {
				return err
			}
			return recalcGRNTotals(ctx, tx, grnID, &resp)
		}

		var trackBatch bool
		if err := tx.QueryRow(ctx, `SELECT track_batch FROM product_variants WHERE id = $1`, req.VariantID).Scan(&trackBatch); err != nil {
			return err
		}
		if trackBatch && req.BatchNo == "" {
			return errBatchRequired
		}

		lineTotal := req.Quantity * req.UnitCost
		if _, err := tx.Exec(ctx, `
			INSERT INTO goods_receipt_lines (id, grn_id, variant_id, quantity, unit_cost, line_total, batch_no, expiry_date)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, NULLIF($6,''), NULLIF($7,'')::date)`,
			grnID, req.VariantID, req.Quantity, req.UnitCost, lineTotal, req.BatchNo, req.ExpiryDate); err != nil {
			return err
		}

		return recalcGRNTotals(ctx, tx, grnID, &resp)
	})

	switch {
	case errors.Is(err, errGRNNotEditable):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "this GRN is no longer a draft")
	case errors.Is(err, errBatchRequired):
		httpx.Error(w, http.StatusBadRequest, "BATCH_REQUIRED", errBatchRequired.Error())
	case isPOError(err):
		respondPO(w, err, 0, nil, "add GRN line")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "GRN not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not add GRN line")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// ---------------------------------------------------------------------
// GET /purchase/grn — list, optionally filtered by status/supplier
// ---------------------------------------------------------------------

type grnSummary struct {
	GRNID      string `json:"grn_id"`
	GRNNumber  string `json:"grn_number"`
	Status     string `json:"status"`
	SupplierID string `json:"supplier_id"`
	BranchID   string `json:"branch_id"`
	GrandTotal string `json:"grand_total"`
	CreatedAt  string `json:"created_at"`
	// The purchase order received against; nil for a direct GRN.
	PurchaseOrderID *string `json:"purchase_order_id"`
	PONumber        string  `json:"po_number"`
	Billed          bool    `json:"billed"`
}

func (h *Handler) ListGRNs(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	status := r.URL.Query().Get("status")
	supplierID := r.URL.Query().Get("supplier_id")
	poID := r.URL.Query().Get("purchase_order_id")

	grns := []grnSummary{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT g.id, g.grn_number, g.status, g.supplier_id, g.branch_id, g.grand_total::text, g.created_at::text,
			       g.purchase_order_id::text, COALESCE(po.po_number, ''),
			       EXISTS (SELECT 1 FROM purchase_bills pb WHERE pb.grn_id = g.id)
			FROM goods_receipt_notes g
			LEFT JOIN purchase_orders po ON po.id = g.purchase_order_id
			WHERE ($1 = '' OR g.status = $1) AND ($2 = '' OR g.supplier_id::text = $2)
			  AND ($3 = '' OR g.purchase_order_id::text = $3)
			ORDER BY g.created_at DESC`, status, supplierID, poID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var g grnSummary
			if err := rows.Scan(&g.GRNID, &g.GRNNumber, &g.Status, &g.SupplierID, &g.BranchID, &g.GrandTotal, &g.CreatedAt,
				&g.PurchaseOrderID, &g.PONumber, &g.Billed); err != nil {
				return err
			}
			grns = append(grns, g)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list GRNs")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"grns": grns})
}

// ---------------------------------------------------------------------
// GET /purchase/grn/{id}
// ---------------------------------------------------------------------

func (h *Handler) GetGRN(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	grnID := chi.URLParam(r, "id")

	var resp grnResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return loadGRN(ctx, tx, grnID, &resp)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "GRN not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load GRN")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------
// POST /purchase/grn/{id}/complete — the critical transaction: allocates
// landed cost across lines, increments stock, updates each variant's
// Weighted Average Cost, and writes the audit-trail stock_movements rows.
// ---------------------------------------------------------------------

func (h *Handler) CompleteGRN(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	grnID := chi.URLParam(r, "id")
	// Optional body {received_on: YYYY-MM-DD}: when the goods actually
	// arrived. The stock movements and completion carry that date.
	var req struct {
		ReceivedOn string `json:"received_on"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	receivedOn, err := pastDate(req.ReceivedOn)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "received_on: "+err.Error())
		return
	}

	var resp grnResponse
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := completeGRNTx(ctx, tx, grnID, claims.UserID, receivedOn); err != nil {
			return err
		}
		return loadGRN(ctx, tx, grnID, &resp)
	})

	switch {
	case errors.Is(err, errGRNNotEditable):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "this GRN is already completed, cancelled, or has no lines")
	case isPOError(err):
		respondPO(w, err, 0, nil, "complete GRN")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "GRN not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not complete GRN")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

// completeGRNTx is the completion itself, inside the caller's transaction:
// landed cost across lines, stock in, Weighted Average Cost, batches and
// the stock movements, dated receivedOn (nil = now). Shared by
// CompleteGRN and the invoice importer (import.go).
func completeGRNTx(ctx context.Context, tx pgx.Tx, grnID, userID string, receivedOn *string) error {
	var branchID, status string
	var poID *string
	var freight, otherCharges, subtotal float64
	if err := tx.QueryRow(ctx, `
		SELECT branch_id, status, freight_amount, other_charges, subtotal, purchase_order_id::text
		FROM goods_receipt_notes WHERE id = $1`, grnID,
	).Scan(&branchID, &status, &freight, &otherCharges, &subtotal, &poID); err != nil {
		return err
	}
	if status != "draft" {
		return errGRNNotEditable
	}

	rows, err := tx.Query(ctx, `
		SELECT id, variant_id, quantity, unit_cost, line_total, COALESCE(batch_no,''), expiry_date::text
		FROM goods_receipt_lines WHERE grn_id = $1`, grnID)
	if err != nil {
		return err
	}
	type line struct {
		id, variantID      string
		quantity, unitCost float64
		lineTotal          float64
		batchNo            string
		expiryDate         *string
	}
	var lines []line
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.id, &l.variantID, &l.quantity, &l.unitCost, &l.lineTotal, &l.batchNo, &l.expiryDate); err != nil {
			rows.Close()
			return err
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(lines) == 0 {
		return errGRNNotEditable
	}
	// Against a purchase order: count the receipt on the PO first, so an
	// over-receipt is refused before any stock moves.
	if poID != nil {
		if err := receivePOTx(ctx, tx, grnID, *poID); err != nil {
			return err
		}
	}

	landedCharges := freight + otherCharges
	for _, l := range lines {
		// Allocate freight/other charges proportionally to each line's
		// share of the GRN's pre-landed-cost subtotal.
		share := 0.0
		if subtotal > 0 {
			share = landedCharges * (l.lineTotal / subtotal)
		}
		landedUnitCost := (l.lineTotal + share) / l.quantity

		if _, err := tx.Exec(ctx, `
			UPDATE goods_receipt_lines SET landed_unit_cost = $1 WHERE id = $2`,
			landedUnitCost, l.id); err != nil {
			return err
		}

		// Phase 8 (Grocery/FMCG): a real batch/lot, with its own landed
		// cost and expiry, comes into existence here — the actual point
		// of origin this vertical's tracking needs, not invented
		// separately. No-op (empty batchNo) for a variant that was
		// never asked to supply one at AddGRNLine time.
		if l.batchNo != "" {
			if err := inventory.ReceiveBatch(ctx, tx, branchID, l.variantID, l.batchNo, l.expiryDate, landedUnitCost, l.quantity); err != nil {
				return err
			}
		}

		// Weighted Average Cost across the whole merchant's stock of
		// this variant (cost_price is a product_variants-level field,
		// not branch-scoped, so "existing qty" is the sum over every
		// branch's stock_levels row, read BEFORE this GRN's addition).
		var existingQty, currentCost float64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(on_hand), 0) FROM stock_levels WHERE variant_id = $1`, l.variantID,
		).Scan(&existingQty); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT cost_price FROM product_variants WHERE id = $1`, l.variantID).Scan(&currentCost); err != nil {
			return err
		}
		newCost := landedUnitCost
		if existingQty+l.quantity > 0 {
			newCost = (existingQty*currentCost + l.quantity*landedUnitCost) / (existingQty + l.quantity)
		}
		if _, err := tx.Exec(ctx, `UPDATE product_variants SET cost_price = $1, updated_at = now() WHERE id = $2`, newCost, l.variantID); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_levels (id, merchant_id, branch_id, variant_id, on_hand, reserved, version)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, 0, 0)
			ON CONFLICT (branch_id, variant_id) DO UPDATE SET
			  on_hand = stock_levels.on_hand + $3, version = stock_levels.version + 1, updated_at = now()`,
			branchID, l.variantID, l.quantity); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_movements (merchant_id, branch_id, variant_id, movement_type, quantity_delta, reference_type, reference_id, performed_by, created_at)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, 'purchase', $3, 'grn', $4, $5,
			        COALESCE(($6::date + time '12:00') AT TIME ZONE 'Asia/Kolkata', now()))`,
			branchID, l.variantID, l.quantity, grnID, userID, receivedOn); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE goods_receipt_notes SET status = 'completed',
		  completed_at = COALESCE(($2::date + time '12:00') AT TIME ZONE 'Asia/Kolkata', now()) WHERE id = $1`, grnID, receivedOn); err != nil {
		return err
	}
	return nil
}

func recalcGRNTotals(ctx context.Context, tx pgx.Tx, grnID string, resp *grnResponse) error {
	tag, err := tx.Exec(ctx, `
		UPDATE goods_receipt_notes SET
		  subtotal = (SELECT COALESCE(SUM(line_total),0) FROM goods_receipt_lines WHERE grn_id = $1),
		  grand_total = (SELECT COALESCE(SUM(line_total),0) FROM goods_receipt_lines WHERE grn_id = $1) + freight_amount + other_charges
		WHERE id = $1`, grnID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return loadGRN(ctx, tx, grnID, resp)
}

func loadGRN(ctx context.Context, tx pgx.Tx, grnID string, resp *grnResponse) error {
	row := tx.QueryRow(ctx, `
		SELECT g.id, g.grn_number, g.status, g.supplier_id, g.branch_id, g.freight_amount::text, g.other_charges::text,
		       g.subtotal::text, g.grand_total::text, g.purchase_order_id::text, COALESCE(po.po_number, ''),
		       pb.id::text, pb.bill_number
		FROM goods_receipt_notes g
		LEFT JOIN purchase_orders po ON po.id = g.purchase_order_id
		LEFT JOIN LATERAL (SELECT id, bill_number FROM purchase_bills WHERE grn_id = g.id ORDER BY created_at LIMIT 1) pb ON true
		WHERE g.id = $1`, grnID)
	if err := row.Scan(&resp.GRNID, &resp.GRNNumber, &resp.Status, &resp.SupplierID, &resp.BranchID,
		&resp.FreightAmount, &resp.OtherCharges, &resp.Subtotal, &resp.GrandTotal, &resp.PurchaseOrderID, &resp.PONumber,
		&resp.BillID, &resp.BillNumber); err != nil {
		return err
	}
	lines, err := loadGRNLines(ctx, tx, grnID)
	if err != nil {
		return err
	}
	resp.Lines = lines
	return nil
}

func loadGRNLines(ctx context.Context, tx pgx.Tx, grnID string) ([]grnLineResp, error) {
	rows, err := tx.Query(ctx, `
		SELECT gl.id, gl.variant_id, pv.sku, p.name, gl.quantity::text, gl.unit_cost::text,
		       COALESCE(gl.landed_unit_cost::text, ''), gl.line_total::text,
		       COALESCE(gl.batch_no,''), gl.expiry_date::text, gl.po_line_id::text
		FROM goods_receipt_lines gl
		JOIN product_variants pv ON pv.id = gl.variant_id
		JOIN products p ON p.id = pv.product_id
		WHERE gl.grn_id = $1
		ORDER BY gl.created_at`, grnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	lines := []grnLineResp{}
	for rows.Next() {
		var l grnLineResp
		if err := rows.Scan(&l.LineID, &l.VariantID, &l.SKU, &l.ProductName, &l.Quantity, &l.UnitCost, &l.LandedUnitCost, &l.LineTotal,
			&l.BatchNo, &l.ExpiryDate, &l.POLineID); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

// ---------------------------------------------------------------------
// DELETE /purchase/grn/{id}?reason= — a draft GRN entered by mistake.
// A draft has moved nothing (stock, cost and a PO's received quantities
// change only at completion), so removing it is safe; its PO's pending
// quantity is free again at once. A completed GRN is corrected with a
// purchase return instead. Soft delete with its lines (migrations/058),
// restorable from the recycle bin.
// ---------------------------------------------------------------------

var (
	errGRNNotDeletable = errors.New("only a draft grn can be deleted")
	errGRNHasReturn    = errors.New("a purchase return refers to this grn")
)

func (h *Handler) DeleteGRN(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	grnID := chi.URLParam(r, "id")
	reason := strings.TrimSpace(r.URL.Query().Get("reason"))
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var poID *string
		if err := tx.QueryRow(ctx, `SELECT status, purchase_order_id::text FROM goods_receipt_notes WHERE id::text = $1 FOR UPDATE`, grnID).
			Scan(&status, &poID); err != nil {
			return err
		}
		// Serialize with a completion or another receipt against the same PO.
		if poID != nil {
			if _, err := lockPO(ctx, tx, *poID); err != nil {
				return err
			}
		}
		if status != "draft" {
			return errGRNNotDeletable
		}
		var referenced bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM purchase_returns WHERE grn_id::text = $1)`, grnID).Scan(&referenced); err != nil {
			return err
		}
		if referenced {
			return errGRNHasReturn
		}
		if err := softdelete.DeleteChildren(ctx, tx, "goods_receipt_lines", "grn_id", grnID, claims.UserID); err != nil {
			return err
		}
		return softdelete.Delete(ctx, tx, "goods_receipt_notes", grnID, claims.UserID, reason)
	})
	switch {
	case errors.Is(err, errGRNNotDeletable):
		httpx.Error(w, http.StatusConflict, "GRN_NOT_DELETABLE", "only a draft GRN can be deleted; correct a completed one with a purchase return")
	case errors.Is(err, errGRNHasReturn):
		httpx.Error(w, http.StatusConflict, "GRN_HAS_RETURN", "a purchase return refers to this GRN")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "GRN not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not delete GRN")
	default:
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}
