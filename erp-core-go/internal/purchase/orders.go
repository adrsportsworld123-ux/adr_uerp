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
	"erp-core-go/internal/softdelete"
)

// Purchase Orders (migrations/057): PO → one or more GRNs → a purchase
// bill per GRN. A PO is a commitment only; stock and cost move when a GRN
// against it completes (completeGRNTx), which is also where the PO's
// received quantities and status are updated.

var (
	errPONotDraft      = errors.New("purchase order is not a draft")
	errPONotReceivable = errors.New("purchase order is not open for receiving")
	errPOOverReceipt   = errors.New("quantity exceeds what is still pending on the purchase order")
	errPOLineMismatch  = errors.New("item is not on this purchase order")
	errPOHasReceipts   = errors.New("purchase order has goods receipts")
	errPONoLines       = errors.New("purchase order has no lines")
	errPOWrongStatus   = errors.New("purchase order is not in a state that allows this")
)

type poLineResp struct {
	LineID      string `json:"line_id"`
	VariantID   string `json:"variant_id"`
	SKU         string `json:"sku"`
	ProductName string `json:"product_name"`
	Quantity    string `json:"quantity"`
	UnitCost    string `json:"unit_cost"`
	LineTotal   string `json:"line_total"`
	ReceivedQty string `json:"received_qty"`
	PendingQty  string `json:"pending_qty"`
	TrackBatch  bool   `json:"track_batch"`
	// ExcessQty: received beyond the ordered quantity on a GRN whose
	// differences were accepted (§3.74); "0" otherwise.
	ExcessQty string `json:"excess_qty"`
}

// poUnplanned: an item received on a GRN against the PO that isn't on the
// PO (§3.74), totalled over the PO's completed GRNs.
type poUnplanned struct {
	VariantID   string `json:"variant_id"`
	SKU         string `json:"sku"`
	ProductName string `json:"product_name"`
	Quantity    string `json:"quantity"`
	LineTotal   string `json:"line_total"`
}

type poGRN struct {
	GRNID      string  `json:"grn_id"`
	GRNNumber  string  `json:"grn_number"`
	Status     string  `json:"status"`
	GrandTotal string  `json:"grand_total"`
	CreatedAt  string  `json:"created_at"`
	BillID     *string `json:"bill_id"`
	BillNumber *string `json:"bill_number"`
}

type poResponse struct {
	POID         string       `json:"po_id"`
	PONumber     string       `json:"po_number"`
	Status       string       `json:"status"`
	BranchID     string       `json:"branch_id"`
	SupplierID   string       `json:"supplier_id"`
	SupplierName string       `json:"supplier_name"`
	OrderDate    string       `json:"order_date"`
	ExpectedDate *string      `json:"expected_date"`
	Subtotal     string       `json:"subtotal"`
	Notes        string       `json:"notes"`
	CloseReason  string       `json:"close_reason"`
	CreatedAt    string       `json:"created_at"`
	Lines        []poLineResp `json:"lines"`
	GRNs         []poGRN      `json:"grns"`
	// Unplanned: items received against the PO that weren't ordered.
	Unplanned []poUnplanned `json:"unplanned"`
}

type poSummary struct {
	POID         string  `json:"po_id"`
	PONumber     string  `json:"po_number"`
	Status       string  `json:"status"`
	BranchID     string  `json:"branch_id"`
	SupplierID   string  `json:"supplier_id"`
	SupplierName string  `json:"supplier_name"`
	OrderDate    string  `json:"order_date"`
	ExpectedDate *string `json:"expected_date"`
	Subtotal     string  `json:"subtotal"`
	OrderedQty   string  `json:"ordered_qty"`
	ReceivedQty  string  `json:"received_qty"`
	CreatedAt    string  `json:"created_at"`
}

type poLineRequest struct {
	VariantID string  `json:"variant_id"`
	Quantity  float64 `json:"quantity"`
	UnitCost  float64 `json:"unit_cost"`
}

func (l poLineRequest) valid() bool {
	return l.VariantID != "" && l.Quantity > 0 && l.UnitCost >= 0
}

// isPOError: an error from the PO side of a GRN (receivePOTx,
// insertPOReceiptLine), for the GRN handlers' own error switch.
func isPOError(err error) bool {
	for _, e := range []error{errPONotReceivable, errPOOverReceipt, errPOLineMismatch} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// respondPO maps the PO errors to the API's codes (phase0_1_design.md §3).
func respondPO(w http.ResponseWriter, err error, status int, resp any, action string) {
	switch {
	case errors.Is(err, errPONotDraft):
		httpx.Error(w, http.StatusConflict, "PO_NOT_EDITABLE", "only a draft purchase order can be changed")
	case errors.Is(err, errPONotReceivable):
		httpx.Error(w, http.StatusConflict, "PO_NOT_RECEIVABLE", "the purchase order must be issued and not yet completed, cancelled or closed")
	case errors.Is(err, errPOOverReceipt):
		httpx.Error(w, http.StatusConflict, "PO_OVER_RECEIPT", errPOOverReceipt.Error())
	case errors.Is(err, errPOLineMismatch):
		httpx.Error(w, http.StatusBadRequest, "PO_LINE_MISMATCH", errPOLineMismatch.Error())
	case errors.Is(err, errPOHasReceipts):
		httpx.Error(w, http.StatusConflict, "PO_HAS_RECEIPTS", "goods have been received (or a draft GRN is open) against this purchase order; close it short instead")
	case errors.Is(err, errPONoLines):
		httpx.Error(w, http.StatusConflict, "PO_NOT_EDITABLE", "add at least one line before issuing the purchase order")
	case errors.Is(err, errPOWrongStatus):
		httpx.Error(w, http.StatusConflict, "PO_NOT_EDITABLE", "the purchase order's status doesn't allow this")
	case errors.Is(err, errBatchRequired):
		httpx.Error(w, http.StatusBadRequest, "BATCH_REQUIRED", errBatchRequired.Error())
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "purchase order, supplier or product not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not "+action)
	default:
		httpx.JSON(w, status, resp)
	}
}

// ---------------------------------------------------------------------
// POST /purchase/orders — a draft PO, whole, in one request
// ---------------------------------------------------------------------

type createPORequest struct {
	SupplierID   string          `json:"supplier_id"`
	BranchID     string          `json:"branch_id"`
	ExpectedDate string          `json:"expected_date"` // YYYY-MM-DD, optional
	Notes        string          `json:"notes"`
	Lines        []poLineRequest `json:"lines"`
}

func (h *Handler) CreatePurchaseOrder(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createPORequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.SupplierID == "" || req.BranchID == "" || len(req.Lines) == 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "supplier_id, branch_id and at least one line are required")
		return
	}
	for _, l := range req.Lines {
		if !l.valid() {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "each line needs a variant_id, a positive quantity and a non-negative unit_cost")
			return
		}
	}
	if req.ExpectedDate != "" {
		if _, err := time.Parse("2006-01-02", req.ExpectedDate); err != nil {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "expected_date must be YYYY-MM-DD")
			return
		}
	}
	poNumber := fmt.Sprintf("PO-%d", time.Now().UnixNano())

	var resp poResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var supplierStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM suppliers WHERE id = $1`, req.SupplierID).Scan(&supplierStatus); err != nil {
			return err
		}
		var poID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO purchase_orders (merchant_id, branch_id, supplier_id, po_number, expected_date, notes, created_by, order_date)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, NULLIF($4,'')::date, NULLIF($5,''), $6,
			        (now() AT TIME ZONE app_merchant_tz())::date)
			RETURNING id`,
			req.BranchID, req.SupplierID, poNumber, req.ExpectedDate, req.Notes, claims.UserID,
		).Scan(&poID); err != nil {
			return err
		}
		for _, l := range req.Lines {
			if err := insertPOLine(ctx, tx, poID, l); err != nil {
				return err
			}
		}
		if err := recalcPOTotals(ctx, tx, poID); err != nil {
			return err
		}
		return loadPO(ctx, tx, poID, &resp)
	})
	respondPO(w, err, http.StatusCreated, &resp, "create purchase order")
}

func insertPOLine(ctx context.Context, tx pgx.Tx, poID string, l poLineRequest) error {
	// INSERT ... SELECT: an unknown variant inserts nothing, so the
	// RETURNING scan reports it as pgx.ErrNoRows (404). clock_timestamp,
	// not the transaction's now(), so lines entered together keep their
	// order on screen and on the PDF.
	var lineID string
	return tx.QueryRow(ctx, `
		INSERT INTO purchase_order_lines (po_id, variant_id, quantity, unit_cost, line_total, created_at)
		SELECT $1, pv.id, $3::numeric(14,3), $4::numeric(14,2), round($3::numeric(14,3) * $4::numeric(14,2), 2), clock_timestamp()
		FROM product_variants pv WHERE pv.id::text = $2
		RETURNING id`,
		poID, l.VariantID, l.Quantity, l.UnitCost).Scan(&lineID)
}

func recalcPOTotals(ctx context.Context, tx pgx.Tx, poID string) error {
	_, err := tx.Exec(ctx, `
		UPDATE purchase_orders SET
		  subtotal = (SELECT COALESCE(SUM(line_total), 0) FROM purchase_order_lines WHERE po_id = $1),
		  updated_at = now()
		WHERE id = $1`, poID)
	return err
}

// lockPO reads a PO's status under a row lock, so status changes and
// receipts against one PO are serialized.
func lockPO(ctx context.Context, tx pgx.Tx, poID string) (string, error) {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM purchase_orders WHERE id::text = $1 FOR UPDATE`, poID).Scan(&status)
	return status, err
}

// ---------------------------------------------------------------------
// GET /purchase/orders?q=&status=&supplier_id=&branch_id=&sort=&dir=&page=&limit=
// (the Phase 11 list convention, httpx.ListParams). status=pending is
// every PO still waiting for goods: open or partially received.
// ---------------------------------------------------------------------

func (h *Handler) ListPurchaseOrders(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	p := httpx.ParseListParams(r, map[string]string{
		"created": "po.created_at", "date": "po.order_date", "expected": "po.expected_date", "total": "po.subtotal",
	}, "created")
	if q.Get("dir") == "" {
		p.OrderBy = "po.created_at DESC"
	}
	status := p.Status
	if status == "all" {
		status = ""
	}
	limit, offset := p.SQLLimit()

	orders := []poSummary{}
	total := 0
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		where := `
			FROM purchase_orders po
			JOIN suppliers s ON s.id = po.supplier_id
			WHERE ($1 = '' OR po.status = $1 OR ($1 = 'pending' AND po.status IN ('open','partially_received')))
			  AND ($2 = '' OR po.supplier_id::text = $2) AND ($3 = '' OR po.branch_id::text = $3)
			  AND ($4 = '' OR po.po_number ILIKE '%' || $4 || '%' OR s.name ILIKE '%' || $4 || '%')`
		args := []any{status, q.Get("supplier_id"), q.Get("branch_id"), p.Q}
		if err := tx.QueryRow(ctx, `SELECT count(*)`+where, args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT po.id, po.po_number, po.status, po.branch_id, po.supplier_id, s.name, po.order_date::text,
			       po.expected_date::text, po.subtotal::text,
			       COALESCE((SELECT SUM(quantity) FROM purchase_order_lines WHERE po_id = po.id), 0)::numeric(14,3)::text,
			       COALESCE((SELECT SUM(received_qty) FROM purchase_order_lines WHERE po_id = po.id), 0)::numeric(14,3)::text,
			       po.created_at::text`+where+`
			ORDER BY `+p.OrderBy+`, po.id LIMIT $5 OFFSET $6`, append(args, limit, offset)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var o poSummary
			if err := rows.Scan(&o.POID, &o.PONumber, &o.Status, &o.BranchID, &o.SupplierID, &o.SupplierName, &o.OrderDate,
				&o.ExpectedDate, &o.Subtotal, &o.OrderedQty, &o.ReceivedQty, &o.CreatedAt); err != nil {
				return err
			}
			orders = append(orders, o)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list purchase orders")
		return
	}
	httpx.JSON(w, http.StatusOK, p.Envelope("purchase_orders", orders, total))
}

// GET /purchase/orders/{id}
func (h *Handler) GetPurchaseOrder(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var resp poResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return loadPO(ctx, tx, chi.URLParam(r, "id"), &resp)
	})
	respondPO(w, err, http.StatusOK, &resp, "load purchase order")
}

func loadPO(ctx context.Context, tx pgx.Tx, poID string, resp *poResponse) error {
	if err := tx.QueryRow(ctx, `
		SELECT po.id, po.po_number, po.status, po.branch_id, po.supplier_id, s.name, po.order_date::text, po.expected_date::text,
		       po.subtotal::text, COALESCE(po.notes, ''), COALESCE(po.close_reason, ''), po.created_at::text
		FROM purchase_orders po
		JOIN suppliers s ON s.id = po.supplier_id
		WHERE po.id::text = $1`, poID,
	).Scan(&resp.POID, &resp.PONumber, &resp.Status, &resp.BranchID, &resp.SupplierID, &resp.SupplierName, &resp.OrderDate,
		&resp.ExpectedDate, &resp.Subtotal, &resp.Notes, &resp.CloseReason, &resp.CreatedAt); err != nil {
		return err
	}

	rows, err := tx.Query(ctx, `
		SELECT l.id, l.variant_id, pv.sku, p.name, l.quantity::text, l.unit_cost::text, l.line_total::text,
		       l.received_qty::text, (l.quantity - l.received_qty)::text, pv.track_batch, l.excess_qty::text
		FROM purchase_order_lines l
		JOIN product_variants pv ON pv.id = l.variant_id
		JOIN products p ON p.id = pv.product_id
		WHERE l.po_id = $1
		ORDER BY l.created_at, l.id`, resp.POID)
	if err != nil {
		return err
	}
	resp.Lines = []poLineResp{}
	for rows.Next() {
		var l poLineResp
		if err := rows.Scan(&l.LineID, &l.VariantID, &l.SKU, &l.ProductName, &l.Quantity, &l.UnitCost, &l.LineTotal,
			&l.ReceivedQty, &l.PendingQty, &l.TrackBatch, &l.ExcessQty); err != nil {
			rows.Close()
			return err
		}
		resp.Lines = append(resp.Lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// The receipts against it, each with its purchase bill (the
	// invoice) once one has been entered.
	rows, err = tx.Query(ctx, `
		SELECT g.id, g.grn_number, g.status, g.grand_total::text, g.created_at::text, pb.id::text, pb.bill_number
		FROM goods_receipt_notes g
		LEFT JOIN purchase_bills pb ON pb.grn_id = g.id
		WHERE g.purchase_order_id = $1
		ORDER BY g.created_at`, resp.POID)
	if err != nil {
		return err
	}
	resp.GRNs = []poGRN{}
	for rows.Next() {
		var g poGRN
		if err := rows.Scan(&g.GRNID, &g.GRNNumber, &g.Status, &g.GrandTotal, &g.CreatedAt, &g.BillID, &g.BillNumber); err != nil {
			rows.Close()
			return err
		}
		resp.GRNs = append(resp.GRNs, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	rows, err = tx.Query(ctx, `
		SELECT gl.variant_id, pv.sku, p.name, SUM(gl.quantity)::text, SUM(gl.line_total)::text
		FROM goods_receipt_lines gl
		JOIN goods_receipt_notes g ON g.id = gl.grn_id
		JOIN product_variants pv ON pv.id = gl.variant_id
		JOIN products p ON p.id = pv.product_id
		WHERE g.purchase_order_id = $1 AND g.status = 'completed' AND gl.po_line_id IS NULL
		GROUP BY gl.variant_id, pv.sku, p.name
		ORDER BY p.name`, resp.POID)
	if err != nil {
		return err
	}
	defer rows.Close()
	resp.Unplanned = []poUnplanned{}
	for rows.Next() {
		var u poUnplanned
		if err := rows.Scan(&u.VariantID, &u.SKU, &u.ProductName, &u.Quantity, &u.LineTotal); err != nil {
			return err
		}
		resp.Unplanned = append(resp.Unplanned, u)
	}
	return rows.Err()
}

// ---------------------------------------------------------------------
// Draft editing: POST /purchase/orders/{id}/lines,
// DELETE /purchase/orders/{id}/lines/{line_id}
// ---------------------------------------------------------------------

func (h *Handler) AddPurchaseOrderLine(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	poID := chi.URLParam(r, "id")
	var req poLineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.valid() {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "variant_id, a positive quantity and a non-negative unit_cost are required")
		return
	}
	var resp poResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		status, err := lockPO(ctx, tx, poID)
		if err != nil {
			return err
		}
		if status != "draft" {
			return errPONotDraft
		}
		if err := insertPOLine(ctx, tx, poID, req); err != nil {
			return err
		}
		if err := recalcPOTotals(ctx, tx, poID); err != nil {
			return err
		}
		return loadPO(ctx, tx, poID, &resp)
	})
	respondPO(w, err, http.StatusOK, &resp, "add purchase order line")
}

func (h *Handler) DeletePurchaseOrderLine(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	poID := chi.URLParam(r, "id")
	var resp poResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		status, err := lockPO(ctx, tx, poID)
		if err != nil {
			return err
		}
		if status != "draft" {
			return errPONotDraft
		}
		tag, err := tx.Exec(ctx, `DELETE FROM purchase_order_lines WHERE po_id::text = $1 AND id::text = $2`, poID, chi.URLParam(r, "line_id"))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		if err := recalcPOTotals(ctx, tx, poID); err != nil {
			return err
		}
		return loadPO(ctx, tx, poID, &resp)
	})
	respondPO(w, err, http.StatusOK, &resp, "remove purchase order line")
}

// ---------------------------------------------------------------------
// Status changes: POST /purchase/orders/{id}/issue | cancel | close
// ---------------------------------------------------------------------

// IssuePurchaseOrder: draft → open. The PO is now the supplier's order and
// can be received against; its lines are frozen.
func (h *Handler) IssuePurchaseOrder(w http.ResponseWriter, r *http.Request) {
	h.poTransition(w, r, "issue purchase order", func(ctx context.Context, tx pgx.Tx, poID, status, userID, _ string) error {
		if status != "draft" {
			return errPONotDraft
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM purchase_order_lines WHERE po_id::text = $1`, poID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return errPONoLines
		}
		_, err := tx.Exec(ctx, `UPDATE purchase_orders SET status = 'open', issued_by = $2, issued_at = now(), updated_at = now() WHERE id::text = $1`, poID, userID)
		return err
	})
}

// CancelPurchaseOrder: a draft or open PO nothing has been received
// against (not even a draft GRN) → cancelled.
func (h *Handler) CancelPurchaseOrder(w http.ResponseWriter, r *http.Request) {
	h.poTransition(w, r, "cancel purchase order", func(ctx context.Context, tx pgx.Tx, poID, status, _, reason string) error {
		if status != "draft" && status != "open" {
			return errPOWrongStatus
		}
		if has, err := poHasGRNs(ctx, tx, poID, false); err != nil {
			return err
		} else if has {
			return errPOHasReceipts
		}
		_, err := tx.Exec(ctx, `UPDATE purchase_orders SET status = 'cancelled', close_reason = NULLIF($2,''), updated_at = now() WHERE id::text = $1`, poID, reason)
		return err
	})
}

// ClosePurchaseOrder: short-close a partially received PO whose remaining
// quantity will never arrive. Refused while a draft GRN is still open
// against it, since completing that GRN would then fail.
func (h *Handler) ClosePurchaseOrder(w http.ResponseWriter, r *http.Request) {
	h.poTransition(w, r, "close purchase order", func(ctx context.Context, tx pgx.Tx, poID, status, _, reason string) error {
		if status != "partially_received" {
			return errPOWrongStatus
		}
		if has, err := poHasGRNs(ctx, tx, poID, true); err != nil {
			return err
		} else if has {
			return errPOHasReceipts
		}
		_, err := tx.Exec(ctx, `UPDATE purchase_orders SET status = 'closed', close_reason = $2, updated_at = now() WHERE id::text = $1`, poID, reason)
		return err
	})
}

func poHasGRNs(ctx context.Context, tx pgx.Tx, poID string, draftOnly bool) (bool, error) {
	var has bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM goods_receipt_notes
		               WHERE purchase_order_id::text = $1 AND status <> 'cancelled' AND (NOT $2 OR status = 'draft'))`,
		poID, draftOnly).Scan(&has)
	return has, err
}

func (h *Handler) poTransition(w http.ResponseWriter, r *http.Request, action string,
	apply func(ctx context.Context, tx pgx.Tx, poID, status, userID, reason string) error) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	poID := chi.URLParam(r, "id")
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	body.Reason = strings.TrimSpace(body.Reason)
	if strings.HasPrefix(action, "close") && body.Reason == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a reason is required to close a purchase order short")
		return
	}
	var resp poResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		status, err := lockPO(ctx, tx, poID)
		if err != nil {
			return err
		}
		if err := apply(ctx, tx, poID, status, claims.UserID, body.Reason); err != nil {
			return err
		}
		return loadPO(ctx, tx, poID, &resp)
	})
	respondPO(w, err, http.StatusOK, &resp, action)
}

// ---------------------------------------------------------------------
// POST /purchase/orders/{id}/grn — receive against the PO: a draft GRN
// with the delivered quantities (any amount up to what is pending), which
// then completes like any other GRN.
// ---------------------------------------------------------------------

type receivePOLine struct {
	POLineID   string   `json:"po_line_id"`
	Quantity   float64  `json:"quantity"`
	UnitCost   *float64 `json:"unit_cost"` // optional; omitted = the PO's agreed cost
	BatchNo    string   `json:"batch_no"`
	ExpiryDate string   `json:"expiry_date"`
}

type receivePORequest struct {
	FreightAmount float64         `json:"freight_amount"`
	OtherCharges  float64         `json:"other_charges"`
	Notes         string          `json:"notes"`
	Lines         []receivePOLine `json:"lines"`
}

func (h *Handler) ReceivePurchaseOrder(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	poID := chi.URLParam(r, "id")
	var req receivePORequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.FreightAmount < 0 || req.OtherCharges < 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "freight and other charges can't be negative")
		return
	}
	var lines []receivePOLine
	for _, l := range req.Lines {
		if l.Quantity == 0 {
			continue // a line not delivered this time
		}
		if l.POLineID == "" || l.Quantity < 0 || (l.UnitCost != nil && *l.UnitCost < 0) {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "each line needs a po_line_id, a positive quantity and a non-negative unit_cost")
			return
		}
		lines = append(lines, l)
	}
	if len(lines) == 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "enter a received quantity on at least one line")
		return
	}

	var resp grnResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		status, err := lockPO(ctx, tx, poID)
		if err != nil {
			return err
		}
		if status != "open" && status != "partially_received" {
			return errPONotReceivable
		}
		var branchID, supplierID string
		if err := tx.QueryRow(ctx, `SELECT branch_id, supplier_id FROM purchase_orders WHERE id::text = $1`, poID).Scan(&branchID, &supplierID); err != nil {
			return err
		}

		branchPrefix := branchID
		if len(branchPrefix) > 8 {
			branchPrefix = branchPrefix[:8]
		}
		var grnID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO goods_receipt_notes (merchant_id, branch_id, supplier_id, grn_number, status, freight_amount, other_charges, notes, received_by, purchase_order_id)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, 'draft', $4, $5, NULLIF($6,''), $7, $8)
			RETURNING id`,
			branchID, supplierID, fmt.Sprintf("GRN-%s-%d", branchPrefix, time.Now().UnixNano()),
			req.FreightAmount, req.OtherCharges, req.Notes, claims.UserID, poID,
		).Scan(&grnID); err != nil {
			return err
		}

		for _, l := range lines {
			if err := insertPOReceiptLine(ctx, tx, poID, grnID, l); err != nil {
				return err
			}
		}
		return recalcGRNTotals(ctx, tx, grnID, &resp)
	})
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusCreated, resp)
	default:
		respondPO(w, err, 0, nil, "receive against purchase order")
	}
}

// insertPOReceiptLine adds one GRN line against a PO line, refusing more
// than is still pending after the PO's other open draft GRNs. (The
// authoritative check is at completion, under the PO lock; this one gives
// the error while the GRN is being entered.)
func insertPOReceiptLine(ctx context.Context, tx pgx.Tx, poID, grnID string, l receivePOLine) error {
	var variantID string
	var unitCost float64
	var trackBatch, fits bool
	if err := tx.QueryRow(ctx, `
		SELECT pol.variant_id, pol.unit_cost, pv.track_batch,
		       pol.received_qty + $3::numeric(14,3) + COALESCE((
		           SELECT SUM(gl.quantity) FROM goods_receipt_lines gl
		           JOIN goods_receipt_notes g ON g.id = gl.grn_id
		           WHERE gl.po_line_id = pol.id AND g.status = 'draft'), 0) <= pol.quantity
		FROM purchase_order_lines pol
		JOIN product_variants pv ON pv.id = pol.variant_id
		WHERE pol.id::text = $1 AND pol.po_id::text = $2`, l.POLineID, poID, l.Quantity,
	).Scan(&variantID, &unitCost, &trackBatch, &fits); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errPOLineMismatch
		}
		return err
	}
	if !fits {
		return errPOOverReceipt
	}
	if trackBatch && l.BatchNo == "" {
		return errBatchRequired
	}
	if l.UnitCost != nil {
		unitCost = *l.UnitCost
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO goods_receipt_lines (grn_id, variant_id, quantity, unit_cost, line_total, batch_no, expiry_date, po_line_id, created_at)
		VALUES ($1, $2, $3, $4, $3::numeric(14,3) * $4::numeric(14,2), NULLIF($5,''), NULLIF($6,'')::date, $7, clock_timestamp())`,
		grnID, variantID, l.Quantity, unitCost, l.BatchNo, l.ExpiryDate, l.POLineID)
	return err
}

// receivePOTx is the PO side of completing a GRN, in completeGRNTx's
// transaction: add this GRN's quantities to the PO lines' received_qty
// (never past the ordered quantity) and move the PO to partially_received
// or, once nothing is pending, completed.
func receivePOTx(ctx context.Context, tx pgx.Tx, grnID, poID string) error {
	status, err := lockPO(ctx, tx, poID)
	if err != nil {
		return err
	}
	if status != "open" && status != "partially_received" {
		return errPONotReceivable
	}
	var varianceAccepted bool
	if err := tx.QueryRow(ctx, `SELECT po_variance_accepted FROM goods_receipt_notes WHERE id = $1`, grnID).Scan(&varianceAccepted); err != nil {
		return err
	}
	if varianceAccepted {
		return receivePOWithVarianceTx(ctx, tx, grnID, poID)
	}
	var unlinked, over bool
	if err := tx.QueryRow(ctx, `
		WITH r AS (
		    SELECT po_line_id, SUM(quantity) AS qty FROM goods_receipt_lines
		    WHERE grn_id = $1 GROUP BY po_line_id
		)
		SELECT COALESCE(bool_or(r.po_line_id IS NULL OR pol.id IS NULL), false),
		       COALESCE(bool_or(pol.received_qty + r.qty > pol.quantity), false)
		FROM r LEFT JOIN purchase_order_lines pol ON pol.id = r.po_line_id AND pol.po_id::text = $2`, grnID, poID,
	).Scan(&unlinked, &over); err != nil {
		return err
	}
	if unlinked {
		return errPOLineMismatch
	}
	if over {
		return errPOOverReceipt
	}
	if _, err := tx.Exec(ctx, `
		UPDATE purchase_order_lines pol SET received_qty = pol.received_qty + r.qty
		FROM (SELECT po_line_id, SUM(quantity) AS qty FROM goods_receipt_lines WHERE grn_id = $1 GROUP BY po_line_id) r
		WHERE pol.id = r.po_line_id`, grnID); err != nil {
		return err
	}
	return updatePOStatus(ctx, tx, poID)
}

// receivePOWithVarianceTx: the same for a GRN whose differences from the
// PO were accepted (po_variance_accepted, the invoice import, §3.74). A
// line with no PO line is an unplanned item: it is stock, not a receipt on
// the PO. A PO line receives up to its ordered quantity and the rest is
// recorded as excess_qty. A line pointing at another PO's line is still
// refused.
func receivePOWithVarianceTx(ctx context.Context, tx pgx.Tx, grnID, poID string) error {
	var foreign bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM goods_receipt_lines gl
		               LEFT JOIN purchase_order_lines pol ON pol.id = gl.po_line_id AND pol.po_id::text = $2
		               WHERE gl.grn_id = $1 AND gl.po_line_id IS NOT NULL AND pol.id IS NULL)`, grnID, poID,
	).Scan(&foreign); err != nil {
		return err
	}
	if foreign {
		return errPOLineMismatch
	}
	if _, err := tx.Exec(ctx, `
		UPDATE purchase_order_lines pol SET
		  received_qty = LEAST(pol.quantity, pol.received_qty + r.qty),
		  excess_qty = pol.excess_qty + GREATEST(0, pol.received_qty + r.qty - pol.quantity)
		FROM (SELECT po_line_id, SUM(quantity) AS qty FROM goods_receipt_lines
		      WHERE grn_id = $1 AND po_line_id IS NOT NULL GROUP BY po_line_id) r
		WHERE pol.id = r.po_line_id`, grnID); err != nil {
		return err
	}
	return updatePOStatus(ctx, tx, poID)
}

// updatePOStatus: partially_received while anything is pending, else
// completed.
func updatePOStatus(ctx context.Context, tx pgx.Tx, poID string) error {
	_, err := tx.Exec(ctx, `
		UPDATE purchase_orders SET
		  status = CASE WHEN EXISTS (SELECT 1 FROM purchase_order_lines WHERE po_id = $1 AND received_qty < quantity)
		                THEN 'partially_received' ELSE 'completed' END,
		  updated_at = now()
		WHERE id = $1`, poID)
	return err
}

// ---------------------------------------------------------------------
// DELETE /purchase/orders/{id}?reason= — a PO entered by mistake: draft or
// open, with no GRN against it (delete a draft GRN first). Soft delete
// with its lines (migrations/058): audited, restorable from the recycle
// bin. A PO the supplier is working on is cancelled instead, so the
// record stays visible.
// ---------------------------------------------------------------------

var errPONotDeletable = errors.New("purchase order can't be deleted")

func (h *Handler) DeletePurchaseOrder(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	poID := chi.URLParam(r, "id")
	reason := strings.TrimSpace(r.URL.Query().Get("reason"))
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		status, err := lockPO(ctx, tx, poID)
		if err != nil {
			return err
		}
		if status != "draft" && status != "open" {
			return errPONotDeletable
		}
		if has, err := poHasGRNs(ctx, tx, poID, false); err != nil {
			return err
		} else if has {
			return errPOHasReceipts
		}
		if err := softdelete.DeleteChildren(ctx, tx, "purchase_order_lines", "po_id", poID, claims.UserID); err != nil {
			return err
		}
		return softdelete.Delete(ctx, tx, "purchase_orders", poID, claims.UserID, reason)
	})
	switch {
	case errors.Is(err, errPONotDeletable):
		httpx.Error(w, http.StatusConflict, "PO_NOT_DELETABLE", "only a draft or open purchase order can be deleted; cancel or close it instead")
	case errors.Is(err, errPOHasReceipts):
		httpx.Error(w, http.StatusConflict, "PO_HAS_RECEIPTS", "a GRN exists against this purchase order; delete its draft GRN first, or cancel or close the PO")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "purchase order not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not delete purchase order")
	default:
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}
