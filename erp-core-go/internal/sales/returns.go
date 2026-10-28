package sales

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/approvals"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/inventory"
	"erp-core-go/internal/taxes"
)

// Phase 13: sales returns and refunds (migrations/038_sales_returns.sql;
// FRD §3 "Return & Exchange"). A return takes back part or all of some
// lines of a finalized sale, refunds exactly their share of what was paid,
// optionally puts the stock back (to the batches the sale drew from), and
// posts the reversing journal entry for that share only. Whether a manager
// must approve is the merchant's setting (sales_settings).

var (
	errNotReturnable     = errors.New("order can't be returned")
	errReturnWindow      = errors.New("return window has passed")
	errReturnQuantity    = errors.New("quantity exceeds what can still be returned")
	errReturnNotApproved = errors.New("return needs a manager's approval")
	errCreditRefund      = errors.New("credit refund exceeds the outstanding credit on this sale")
)

type salesSettings struct {
	ReturnsRequireApproval bool `json:"returns_require_approval"`
	ReturnWindowDays       *int `json:"return_window_days"`
	// Phase 13 shifts: refuse POS checkout on a till with no open shift.
	ShiftsRequired bool `json:"shifts_required"`
	// Round-off waiver (migrations/059): the most a sale may be settled
	// below its total ("0.00" = off), and whether the till offers rounding
	// down to the rupee by default.
	RoundOffMax  string `json:"round_off_max"`
	RoundOffAuto bool   `json:"round_off_auto"`
	roundOffMax  float64
	// migrations/062: whether open-price items take discounts and
	// promotions — the merchant's default; branches may override it
	// (branch_sales.go).
	OpenPriceDiscounts bool `json:"open_price_discounts"`
	// migrations/063: the POS grid lists open-price items first (the
	// merchant's default; branches may override it).
	OpenPriceFirst bool `json:"open_price_first"`
	// migrations/071: which price a POS line gets: standard (as before),
	// customer (the lowest of MRP, selling, markup and price-rule prices)
	// or shop (the highest). The merchant's default; branches may override.
	PriceBenefit string `json:"price_benefit"`
}

func loadSalesSettings(ctx context.Context, tx pgx.Tx) (salesSettings, error) {
	s := salesSettings{ReturnsRequireApproval: true, RoundOffMax: "0.00", OpenPriceDiscounts: true, PriceBenefit: "standard"} // the safe default when never configured
	err := tx.QueryRow(ctx, `
		SELECT returns_require_approval, return_window_days, shifts_required, round_off_max::text, round_off_auto, round_off_max::float8,
		       open_price_discounts, open_price_first, price_benefit
		FROM sales_settings`).
		Scan(&s.ReturnsRequireApproval, &s.ReturnWindowDays, &s.ShiftsRequired, &s.RoundOffMax, &s.RoundOffAuto, &s.roundOffMax,
			&s.OpenPriceDiscounts, &s.OpenPriceFirst, &s.PriceBenefit)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil
	}
	return s, err
}

// GetSettings: GET /sales/settings
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var s salesSettingsResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		s, err = loadSalesSettingsResponse(ctx, tx)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load sales settings")
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

// UpdateSettings: PUT /sales/settings — sales.settings permission.
// return_window_days: null = no limit.
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	// shifts_required and the round-off fields are optional so a client
	// that predates them (the return-policy form) doesn't switch them off
	// by omission.
	var req struct {
		ReturnsRequireApproval bool     `json:"returns_require_approval"`
		ReturnWindowDays       *int     `json:"return_window_days"`
		ShiftsRequired         *bool    `json:"shifts_required"`
		RoundOffMax            *float64 `json:"round_off_max"`
		RoundOffAuto           *bool    `json:"round_off_auto"`
		OpenPriceDiscounts     *bool    `json:"open_price_discounts"`
		OpenPriceFirst         *bool    `json:"open_price_first"`
		PriceBenefit           *string  `json:"price_benefit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.PriceBenefit != nil && !validBenefit(*req.PriceBenefit) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "price_benefit must be standard, customer or shop")
		return
	}
	if req.ReturnWindowDays != nil && *req.ReturnWindowDays <= 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "return_window_days must be positive, or null for no limit")
		return
	}
	if req.RoundOffMax != nil && (*req.RoundOffMax < 0 || *req.RoundOffMax > MaxRoundOff+0.0001) {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf("round_off_max must be between 0 and %.2f", MaxRoundOff))
		return
	}
	var saved salesSettingsResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO sales_settings (merchant_id, returns_require_approval, return_window_days, shifts_required,
			    round_off_max, round_off_auto, updated_at)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, COALESCE($3, false),
			    round(COALESCE($4::numeric, 0), 2), COALESCE($5, false), now())
			ON CONFLICT (merchant_id) DO UPDATE SET returns_require_approval = $1, return_window_days = $2,
			    shifts_required = COALESCE($3, sales_settings.shifts_required),
			    round_off_max = COALESCE(round($4::numeric, 2), sales_settings.round_off_max),
			    round_off_auto = COALESCE($5, sales_settings.round_off_auto), updated_at = now()`,
			req.ReturnsRequireApproval, req.ReturnWindowDays, req.ShiftsRequired, req.RoundOffMax, req.RoundOffAuto); err != nil {
			return err
		}
		if req.OpenPriceDiscounts != nil {
			if _, err := tx.Exec(ctx, `UPDATE sales_settings SET open_price_discounts = $1`, *req.OpenPriceDiscounts); err != nil {
				return err
			}
		}
		if req.OpenPriceFirst != nil {
			if _, err := tx.Exec(ctx, `UPDATE sales_settings SET open_price_first = $1`, *req.OpenPriceFirst); err != nil {
				return err
			}
		}
		if req.PriceBenefit != nil {
			if _, err := tx.Exec(ctx, `UPDATE sales_settings SET price_benefit = $1`, *req.PriceBenefit); err != nil {
				return err
			}
		}
		var err error
		saved, err = loadSalesSettingsResponse(ctx, tx)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save sales settings")
		return
	}
	httpx.JSON(w, http.StatusOK, saved)
}

// returnableLine is one sale line with what's already gone back.
type returnableLine struct {
	LineID        string `json:"line_id"`
	VariantID     string `json:"variant_id"`
	ProductName   string `json:"product_name"`
	SKU           string `json:"sku"`
	SoldQuantity  string `json:"sold_quantity"`
	ReturnedQty   string `json:"returned_quantity"`
	ReturnableQty string `json:"returnable_quantity"`
	UnitRefund    string `json:"unit_refund"` // line_total / sold qty — what one unit refunds
	soldQty       float64
	returnedQty   float64
	lineTotal     float64
	taxableValue  float64
	taxAmount     float64
	returnedTotal float64
	returnedTax   float64
	returnedValue float64
}

func loadReturnableLines(ctx context.Context, tx pgx.Tx, orderID string) ([]returnableLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT l.id, l.variant_id, COALESCE(l.description, p.name), v.sku, l.quantity, l.line_total, l.taxable_value, l.tax_amount,
		       COALESCE(sum(rl.quantity), 0), COALESCE(sum(rl.refund_amount), 0),
		       COALESCE(sum(rl.tax_amount), 0), COALESCE(sum(rl.taxable_amount), 0)
		FROM sales_order_lines l
		JOIN product_variants v ON v.id = l.variant_id
		JOIN products p ON p.id = v.product_id
		LEFT JOIN sales_return_lines rl ON rl.sales_order_line_id = l.id
		WHERE l.sales_order_id = $1
		GROUP BY l.id, l.variant_id, l.description, p.name, v.sku, l.quantity, l.line_total, l.taxable_value, l.tax_amount, l.created_at
		ORDER BY l.created_at`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []returnableLine
	for rows.Next() {
		var l returnableLine
		if err := rows.Scan(&l.LineID, &l.VariantID, &l.ProductName, &l.SKU, &l.soldQty, &l.lineTotal, &l.taxableValue, &l.taxAmount,
			&l.returnedQty, &l.returnedTotal, &l.returnedTax, &l.returnedValue); err != nil {
			return nil, err
		}
		l.SoldQuantity = fmt.Sprintf("%.3f", l.soldQty)
		l.ReturnedQty = fmt.Sprintf("%.3f", l.returnedQty)
		l.ReturnableQty = fmt.Sprintf("%.3f", math.Max(0, l.soldQty-l.returnedQty))
		if l.soldQty > 0 {
			l.UnitRefund = fmt.Sprintf("%.2f", l.lineTotal/l.soldQty)
		} else {
			l.UnitRefund = "0.00"
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetReturnable: GET /sales/orders/{id}/returnable — what the POS return
// screen needs: every line with what can still be returned, plus whether
// approval is required and whether the window is still open.
func (h *Handler) GetReturnable(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")
	resp := map[string]any{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var status, number string
		var finalizedAt *time.Time
		var creditOutstanding float64
		var roundOff, roundOffBack string
		if err := tx.QueryRow(ctx, `
			SELECT status, order_number, finalized_at, credit_amount - credit_paid, round_off::text,
			       COALESCE((SELECT sum(r.round_off) FROM sales_returns r WHERE r.sales_order_id = so.id), 0)::numeric(14,2)::text
			FROM sales_orders so WHERE id::text = $1`, orderID).
			Scan(&status, &number, &finalizedAt, &creditOutstanding, &roundOff, &roundOffBack); err != nil {
			return err
		}
		settings, err := loadSalesSettings(ctx, tx)
		if err != nil {
			return err
		}
		lines, err := loadReturnableLines(ctx, tx, orderID)
		if err != nil {
			return err
		}
		windowOpen := true
		if settings.ReturnWindowDays != nil && finalizedAt != nil {
			windowOpen = time.Since(*finalizedAt) <= time.Duration(*settings.ReturnWindowDays)*24*time.Hour
		}
		resp["order_id"], resp["order_number"], resp["status"] = orderID, number, status
		resp["returnable"] = (status == "finalized" || status == "partially_refunded") && windowOpen
		resp["window_open"] = windowOpen
		resp["requires_approval"] = settings.ReturnsRequireApproval
		// Phase 15: refunds up to this amount need no approval (null = all).
		policy, err := approvals.Load(ctx, tx)
		if err != nil {
			return err
		}
		resp["approval_above"] = policy.RefundApprovalAbove
		resp["return_window_days"] = settings.ReturnWindowDays
		resp["credit_outstanding"] = fmt.Sprintf("%.2f", math.Max(0, creditOutstanding))
		// The sale's checkout waiver (signed) and how much of it returns
		// have already given back: the last returns refund up to that much
		// less than the goods' value (round_off.go).
		resp["round_off"], resp["round_off_returned"] = roundOff, roundOffBack
		if lines == nil {
			lines = []returnableLine{}
		}
		resp["lines"] = lines
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the order")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

type createReturnRequest struct {
	Lines []struct {
		LineID   string  `json:"line_id"`
		Quantity float64 `json:"quantity"`
	} `json:"lines"`
	Reason          string `json:"reason"`
	RefundMethod    string `json:"refund_method"`
	RefundReference string `json:"refund_reference"`
	// Restock: put the returned units back on the shelf. Default true;
	// false for damaged goods (refund without restocking).
	Restock        *bool  `json:"restock"`
	ApproverCode   string `json:"approver_code"`
	ApproverPIN    string `json:"approver_pin"`
	IdempotencyKey string `json:"idempotency_key"`
	POSTerminalID  string `json:"pos_terminal_id"`
}

type returnResponse struct {
	ReturnID     string `json:"return_id"`
	ReturnNumber string `json:"return_number"`
	OrderID      string `json:"order_id"`
	OrderStatus  string `json:"order_status"`
	RefundMethod string `json:"refund_method"`
	RefundAmount string `json:"refund_amount"`
	TaxAmount    string `json:"tax_amount"`
	// RoundOff: the part of the sale's checkout waiver this return gave
	// back; RefundAmount is the goods' value less it.
	RoundOff   string  `json:"round_off"`
	Restocked  bool    `json:"restocked"`
	ApprovedBy *string `json:"approved_by"`
	CreatedAt  string  `json:"created_at"`
	Replayed   bool    `json:"replayed"`
	// The credit note's tax, split as the invoice charged it
	// (credit_note.go, migrations/060). Additive.
	TaxBreakup *taxes.Breakup `json:"tax_breakup"`
}

// approverFor resolves an employee code + PIN to a Branch Manager or
// Merchant Admin who covers branchID; "" means not approved (unknown code,
// wrong PIN, a role that can't approve, or another branch's manager), one
// answer for all. Phase 15: the check itself is approvals.Approve.
func approverFor(ctx context.Context, tx pgx.Tx, code, pin, branchID string) (string, error) {
	return approvals.Approve(ctx, tx, code, "", pin, approvals.LevelBranchManager, branchID)
}

func loadReturn(ctx context.Context, tx pgx.Tx, returnID string) (returnResponse, error) {
	var resp returnResponse
	err := tx.QueryRow(ctx, `
		SELECT r.id, r.return_number, r.sales_order_id, so.status, r.refund_method, r.refund_amount::text, r.tax_amount::text,
		       r.round_off::text, r.restocked, r.approved_by::text, r.created_at::text
		FROM sales_returns r JOIN sales_orders so ON so.id = r.sales_order_id
		WHERE r.id = $1`, returnID).Scan(&resp.ReturnID, &resp.ReturnNumber, &resp.OrderID, &resp.OrderStatus, &resp.RefundMethod,
		&resp.RefundAmount, &resp.TaxAmount, &resp.RoundOff, &resp.Restocked, &resp.ApprovedBy, &resp.CreatedAt)
	if err != nil {
		return resp, err
	}
	resp.TaxBreakup, _, err = returnTaxBreakup(ctx, tx, returnID)
	return resp, err
}

// CreateReturn: POST /sales/orders/{id}/returns
func (h *Handler) CreateReturn(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")
	var req createReturnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	switch {
	case len(req.Lines) == 0:
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "choose at least one line to return")
		return
	case req.Reason == "":
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "a reason is required")
		return
	case req.RefundMethod != "cash" && req.RefundMethod != "card" && req.RefundMethod != "upi" && req.RefundMethod != "credit":
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "refund_method must be cash, card, upi or credit")
		return
	case strings.TrimSpace(req.IdempotencyKey) == "":
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "idempotency_key is required")
		return
	}
	seen := map[string]bool{}
	for _, l := range req.Lines {
		if l.Quantity <= 0 || seen[l.LineID] {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "each line once, with a positive quantity")
			return
		}
		seen[l.LineID] = true
	}
	restock := req.Restock == nil || *req.Restock

	var resp returnResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		// Retried request (offline-first clients retry): answer with the
		// return already recorded under this key.
		var existing string
		if err := tx.QueryRow(ctx, `SELECT id FROM sales_returns WHERE idempotency_key = $1`, req.IdempotencyKey).Scan(&existing); err == nil {
			var lerr error
			resp, lerr = loadReturn(ctx, tx, existing)
			resp.Replayed = true
			return lerr
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		// Lock the order: two tills returning the same sale at once must
		// not both pass the "still returnable" check.
		var status, branchID string
		var customerID *string
		var finalizedAt *time.Time
		var creditAmount, creditPaid float64
		if err := tx.QueryRow(ctx, `
			SELECT status, branch_id, customer_id::text, finalized_at, credit_amount, credit_paid
			FROM sales_orders WHERE id::text = $1 FOR UPDATE`, orderID).
			Scan(&status, &branchID, &customerID, &finalizedAt, &creditAmount, &creditPaid); err != nil {
			return err
		}
		if status != "finalized" && status != "partially_refunded" {
			return errNotReturnable
		}
		settings, err := loadSalesSettings(ctx, tx)
		if err != nil {
			return err
		}
		if settings.ReturnWindowDays != nil && finalizedAt != nil &&
			time.Since(*finalizedAt) > time.Duration(*settings.ReturnWindowDays)*24*time.Hour {
			return errReturnWindow
		}

		lines, err := loadReturnableLines(ctx, tx, orderID)
		if err != nil {
			return err
		}
		byID := map[string]returnableLine{}
		for _, l := range lines {
			byID[l.LineID] = l
		}

		type plan struct {
			line                     returnableLine
			qty, taxable, tax, total float64
		}
		var plans []plan
		var subtotal, taxTotal float64
		for _, rl := range req.Lines {
			l, ok := byID[rl.LineID]
			if !ok {
				return fmt.Errorf("%w: line %s is not on this order", errReturnQuantity, rl.LineID)
			}
			remaining := l.soldQty - l.returnedQty
			if rl.Quantity > remaining+1e-9 {
				return fmt.Errorf("%w: %s — %.3f can still be returned", errReturnQuantity, l.ProductName, math.Max(0, remaining))
			}
			var p plan
			p.line, p.qty = l, rl.Quantity
			if math.Abs(rl.Quantity-remaining) < 1e-9 {
				// The last units of a line take whatever is left, so the
				// line's returns always add up to exactly what was paid.
				p.taxable = round2(l.taxableValue - l.returnedValue)
				p.tax = round2(l.taxAmount - l.returnedTax)
			} else {
				share := rl.Quantity / l.soldQty
				p.taxable = round2(l.taxableValue * share)
				p.tax = round2(l.taxAmount * share)
			}
			p.total = round2(p.taxable + p.tax)
			subtotal += p.taxable
			taxTotal += p.tax
			plans = append(plans, p)
		}
		subtotal, taxTotal = round2(subtotal), round2(taxTotal)
		// The goods' value, less the part of a checkout round-off waiver
		// this return gives back (round_off.go): together the refunds never
		// come to more than the customer paid.
		returnRoundOffAmt, err := returnRoundOff(ctx, tx, orderID, round2(subtotal+taxTotal))
		if err != nil {
			return err
		}
		refund := round2(subtotal + taxTotal - returnRoundOffAmt)

		if req.RefundMethod == "credit" {
			if customerID == nil || refund > round2(creditAmount-creditPaid)+0.004 {
				return errCreditRefund
			}
		}

		// Approval (Phase 15: the merchant's approval policy may exempt
		// refunds up to an amount, and names the approver level). Decided
		// after the refund is known; the approver must cover this branch.
		var approvedBy *string
		policy, err := approvals.Load(ctx, tx)
		if err != nil {
			return err
		}
		if settings.ReturnsRequireApproval && (policy.RefundApprovalAbove == nil || refund > *policy.RefundApprovalAbove+0.004) {
			id, err := approvals.Approve(ctx, tx, req.ApproverCode, "", req.ApproverPIN, policy.RefundApprover, branchID)
			if err != nil {
				return err
			}
			if id == "" {
				return errReturnNotApproved
			}
			approvedBy = &id
		}

		var terminalID *string
		if strings.TrimSpace(req.POSTerminalID) != "" {
			t := req.POSTerminalID
			terminalID = &t
		}
		number := fmt.Sprintf("SRET-%d", time.Now().UnixNano())
		var returnID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO sales_returns (merchant_id, sales_order_id, branch_id, pos_terminal_id, return_number, reason,
			    refund_method, refund_reference, subtotal, tax_amount, refund_amount, restocked, processed_by, approved_by, idempotency_key, round_off)
			VALUES (current_setting('app.tenant_id')::uuid, $1::uuid, $2, NULLIF($3,'')::uuid, $4, $5, $6, NULLIF($7,''), $8, $9, $10, $11, $12, $13, $14, $15)
			RETURNING id`,
			orderID, branchID, deref(terminalID), number, req.Reason, req.RefundMethod, strings.TrimSpace(req.RefundReference),
			subtotal, taxTotal, refund, restock, claims.UserID, approvedBy, req.IdempotencyKey, returnRoundOffAmt).Scan(&returnID); err != nil {
			return err
		}

		for _, p := range plans {
			var returnLineID string
			if err := tx.QueryRow(ctx, `
				INSERT INTO sales_return_lines (sales_return_id, sales_order_line_id, variant_id, quantity, taxable_amount, tax_amount, refund_amount)
				VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
				returnID, p.line.LineID, p.line.VariantID, p.qty, p.taxable, p.tax, p.total).Scan(&returnLineID); err != nil {
				return err
			}
			if !restock {
				continue
			}
			if _, err := tx.Exec(ctx, `
				UPDATE stock_levels SET on_hand = on_hand + $1, version = version + 1, updated_at = now()
				WHERE branch_id = $2 AND variant_id = $3`, p.qty, branchID, p.line.VariantID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO stock_movements (merchant_id, branch_id, variant_id, movement_type, quantity_delta, reference_type, reference_id, reason, performed_by)
				VALUES (current_setting('app.tenant_id')::uuid, $1, $2, 'return', $3, 'sales_return', $4, $5, $6)`,
				branchID, p.line.VariantID, p.qty, returnID, req.Reason, claims.UserID); err != nil {
				return err
			}
			if err := restoreReturnBatches(ctx, tx, p.line.LineID, returnLineID, returnID, p.qty, claims.UserID); err != nil {
				return err
			}
		}

		// Order status: refunded once every unit of every line is back.
		var outstanding float64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(sum(l.quantity), 0) - COALESCE((SELECT sum(rl.quantity) FROM sales_return_lines rl
			       JOIN sales_order_lines ol ON ol.id = rl.sales_order_line_id WHERE ol.sales_order_id = $1::uuid), 0)
			FROM sales_order_lines l WHERE l.sales_order_id = $1::uuid`, orderID).Scan(&outstanding); err != nil {
			return err
		}
		newStatus := "partially_refunded"
		if outstanding <= 1e-9 {
			newStatus = "refunded"
		}
		if _, err := tx.Exec(ctx, `UPDATE sales_orders SET status = $1 WHERE id::text = $2`, newStatus, orderID); err != nil {
			return err
		}
		if req.RefundMethod == "credit" {
			if _, err := tx.Exec(ctx, `UPDATE sales_orders SET credit_amount = credit_amount - $1 WHERE id::text = $2`, refund, orderID); err != nil {
				return err
			}
		}

		// Reverse exactly this share of the sale's entry: revenue and GST
		// back out, and the money leaves the account the refund is paid from.
		refundLine := accounting.JournalLine{AccountCode: accounting.AccountCodeForPaymentMethod(req.RefundMethod), Credit: refund}
		if req.RefundMethod == "credit" {
			refundLine = accounting.JournalLine{AccountCode: accounting.AccountReceivable, Credit: refund, PartyType: "customer", PartyID: *customerID}
		}
		journal := []accounting.JournalLine{refundLine}
		if returnRoundOffAmt > 0 {
			journal = append(journal, accounting.JournalLine{AccountCode: accounting.AccountRoundOff, Credit: returnRoundOffAmt})
		}
		if subtotal > 0 {
			journal = append(journal, accounting.JournalLine{AccountCode: accounting.AccountSalesRevenue, Debit: subtotal})
		}
		if taxTotal > 0 {
			journal = append(journal, accounting.JournalLine{AccountCode: accounting.AccountGSTPayable, Debit: taxTotal})
		}
		if _, err := accounting.PostJournalEntry(ctx, tx, branchID, "sales_return", returnID, "Sales return "+number, claims.UserID, journal); err != nil {
			return err
		}

		resp, err = loadReturn(ctx, tx, returnID)
		return err
	})

	switch {
	case err == nil:
		code := http.StatusCreated
		if resp.Replayed {
			code = http.StatusOK
		}
		httpx.JSON(w, code, resp)
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case errors.Is(err, errNotReturnable):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_RETURNABLE", "only a completed sale can be returned (this one is voided, already fully refunded, or not finished)")
	case errors.Is(err, errReturnWindow):
		httpx.Error(w, http.StatusConflict, "RETURN_WINDOW_CLOSED", "the return window for this sale has passed")
	case errors.Is(err, errReturnNotApproved):
		httpx.Error(w, http.StatusForbidden, "RETURN_NOT_APPROVED", "a Branch Manager or Merchant Admin must approve this return with their employee code and PIN")
	case errors.Is(err, errReturnQuantity):
		httpx.Error(w, http.StatusConflict, "RETURN_QUANTITY_EXCEEDED", err.Error())
	case errors.Is(err, errCreditRefund):
		httpx.Error(w, http.StatusConflict, "CREDIT_REFUND_EXCEEDED", "a credit refund can't exceed this sale's unpaid credit — refund the rest in cash, card or UPI")
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not record the return")
	}
}

// restoreReturnBatches puts returned units back into the batches the sale
// drew them from (sales_order_line_batches), never more per batch than the
// sale took minus what earlier returns already put back. A variant that
// isn't batch-tracked has no allocations, and this is a no-op.
func restoreReturnBatches(ctx context.Context, tx pgx.Tx, orderLineID, returnLineID, returnID string, qty float64, performedBy string) error {
	rows, err := tx.Query(ctx, `
		SELECT a.batch_id, a.quantity - COALESCE((
		         SELECT sum(rb.quantity) FROM sales_return_line_batches rb
		         JOIN sales_return_lines rl ON rl.id = rb.sales_return_line_id
		         WHERE rl.sales_order_line_id = a.sales_order_line_id AND rb.batch_id = a.batch_id), 0)
		FROM sales_order_line_batches a
		WHERE a.sales_order_line_id = $1
		ORDER BY a.batch_id`, orderLineID)
	if err != nil {
		return err
	}
	type alloc struct {
		batchID   string
		available float64
	}
	var allocs []alloc
	for rows.Next() {
		var a alloc
		if err := rows.Scan(&a.batchID, &a.available); err != nil {
			rows.Close()
			return err
		}
		allocs = append(allocs, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	left := qty
	for _, a := range allocs {
		if left <= 1e-9 {
			break
		}
		take := math.Min(left, a.available)
		if take <= 1e-9 {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE stock_batches SET quantity_remaining = quantity_remaining + $1 WHERE id = $2`, take, a.batchID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sales_return_line_batches (sales_return_line_id, batch_id, quantity) VALUES ($1, $2, $3)`,
			returnLineID, a.batchID, take); err != nil {
			return err
		}
		if err := inventory.RecordBatchMovement(ctx, tx, a.batchID, take, "sales_return", returnID, performedBy); err != nil {
			return err
		}
		left -= take
	}
	return nil
}

// ListReturns: GET /sales/orders/{id}/returns — the returns already made
// against one sale, newest first.
func (h *Handler) ListReturns(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")
	list := []returnResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM sales_returns WHERE sales_order_id::text = $1 ORDER BY created_at DESC`, orderID)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			rr, err := loadReturn(ctx, tx, id)
			if err != nil {
				return err
			}
			list = append(list, rr)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list returns")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"returns": list})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
