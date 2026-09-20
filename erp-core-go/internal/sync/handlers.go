// Package sync implements the offline-first sync protocol
// (phase0_1_design.md §3.5): a POS device that was offline builds a
// complete sale locally (no live reservation calls are possible without a
// connection) and pushes it as one already-decided unit on reconnect,
// rather than replaying CreateOrder/AddLine/Checkout call-by-call.
package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB *db.DB
}

// ---------------------------------------------------------------------
// POST /sync/push
// ---------------------------------------------------------------------

type pushLine struct {
	VariantID      string  `json:"variant_id"`
	Quantity       float64 `json:"quantity"`
	UnitPrice      float64 `json:"unit_price"`
	DiscountAmount float64 `json:"discount_amount"`
	TaxAmount      float64 `json:"tax_amount"`
	LineTotal      float64 `json:"line_total"`
}

type pushPayment struct {
	Method string  `json:"method"`
	Amount float64 `json:"amount"`
}

type pushOrder struct {
	IdempotencyKey  string        `json:"idempotency_key"`
	BranchID        string        `json:"branch_id"`
	POSTerminalID   string        `json:"pos_terminal_id"`
	CustomerID      string        `json:"customer_id"`
	DeviceCreatedAt string        `json:"device_created_at"` // RFC3339
	Lines           []pushLine    `json:"lines"`
	Payments        []pushPayment `json:"payments"`
}

type pushRequest struct {
	Orders []pushOrder `json:"orders"`
}

type pushResult struct {
	IdempotencyKey string `json:"idempotency_key"`
	Status         string `json:"status"` // "ok" | "duplicate" | "error"
	OrderID        string `json:"order_id,omitempty"`
	NegativeStock  bool   `json:"negative_stock,omitempty"`
	Error          string `json:"error,omitempty"`
}

// Push: POST /sync/push — a batch upload from a device that was offline.
// Each order is processed in its own transaction, independently of the
// others: a bad order in the batch (malformed line, unknown variant)
// fails that one order and reports why, but never aborts the rest of the
// batch or loses orders the device otherwise successfully synced —
// offline devices need to be able to retry a whole batch safely, and a
// single failing order can't be allowed to block every other sale in it.
func (h *Handler) Push(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}

	var req pushRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}

	results := make([]pushResult, 0, len(req.Orders))
	for _, o := range req.Orders {
		results = append(results, h.pushOne(r.Context(), claims, o))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"results": results})
}

func (h *Handler) pushOne(ctx context.Context, claims *authn.Claims, o pushOrder) pushResult {
	res := pushResult{IdempotencyKey: o.IdempotencyKey}
	if o.IdempotencyKey == "" || o.BranchID == "" || o.POSTerminalID == "" || len(o.Lines) == 0 {
		res.Status = "error"
		res.Error = "idempotency_key, branch_id, pos_terminal_id and at least one line are required"
		return res
	}

	deviceCreatedAt, err := time.Parse(time.RFC3339, o.DeviceCreatedAt)
	if err != nil {
		deviceCreatedAt = time.Now()
	}

	var negativeStock bool
	err = h.DB.WithTenant(ctx, claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		branchPrefix := o.BranchID
		if len(branchPrefix) > 8 {
			branchPrefix = branchPrefix[:8]
		}
		orderNumber := branchPrefix + "-sync-" + time.Now().Format("20060102150405.000000000")

		var orderID, status string
		if err := tx.QueryRow(ctx, `
			INSERT INTO sales_orders (id, merchant_id, branch_id, pos_terminal_id, cashier_id, customer_id, order_number, status, idempotency_key, device_created_at, synced_at)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, NULLIF($4, '')::uuid, $5, 'cart', $6, $7, now())
			ON CONFLICT (merchant_id, idempotency_key) DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
			RETURNING id, status`,
			o.BranchID, o.POSTerminalID, claims.UserID, o.CustomerID, orderNumber, o.IdempotencyKey, deviceCreatedAt,
		).Scan(&orderID, &status); err != nil {
			return err
		}
		res.OrderID = orderID

		// Idempotent replay: this idempotency_key was already fully
		// processed by a previous push attempt — report it as such rather
		// than reprocessing (which would double-decrement stock).
		if status == "finalized" {
			res.Status = "duplicate"
			return nil
		}

		var subtotal, discountTotal, taxTotal, grandTotal float64
		for _, l := range o.Lines {
			if _, err := tx.Exec(ctx, `
				INSERT INTO sales_order_lines (id, sales_order_id, variant_id, quantity, unit_price, discount_amount, tax_amount, line_total)
				VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7)`,
				orderID, l.VariantID, l.Quantity, l.UnitPrice, l.DiscountAmount, l.TaxAmount, l.LineTotal); err != nil {
				return err
			}

			went, err := applySyncSale(ctx, tx, o.BranchID, l.VariantID, l.Quantity, orderID, claims.UserID)
			if err != nil {
				return err
			}
			if went {
				negativeStock = true
			}

			subtotal += l.UnitPrice * l.Quantity
			discountTotal += l.DiscountAmount
			taxTotal += l.TaxAmount
			grandTotal += l.LineTotal
		}

		for _, p := range o.Payments {
			if _, err := tx.Exec(ctx, `
				INSERT INTO payments (id, sales_order_id, method, amount, status)
				VALUES (gen_random_uuid(), $1, $2, $3, 'captured')`, orderID, p.Method, p.Amount); err != nil {
				return err
			}
		}

		if negativeStock {
			beforeJSON, _ := json.Marshal(map[string]any{"note": "sync push"})
			if _, err := tx.Exec(ctx, `
				INSERT INTO audit_logs (merchant_id, entity_type, entity_id, action, performed_by, before_value, reason)
				VALUES (current_setting('app.tenant_id')::uuid, 'stock_levels', $1, 'update', $2, $3, 'NEGATIVE_STOCK: oversold during offline sync')`,
				orderID, claims.UserID, beforeJSON); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE sales_orders SET status = 'finalized', finalized_at = now(),
			  subtotal = $1, discount_total = $2, tax_total = $3, grand_total = $4
			WHERE id = $5`, subtotal, discountTotal, taxTotal, grandTotal, orderID); err != nil {
			return err
		}

		debitLines := make([]accounting.JournalLine, 0, len(o.Payments))
		for _, p := range o.Payments {
			debitLines = append(debitLines, accounting.JournalLine{AccountCode: accounting.AccountCodeForPaymentMethod(p.Method), Debit: p.Amount})
		}
		lines := debitLines
		if netRevenue := subtotal - discountTotal; netRevenue > 0 {
			lines = append(lines, accounting.JournalLine{AccountCode: accounting.AccountSalesRevenue, Credit: netRevenue})
		}
		if taxTotal > 0 {
			lines = append(lines, accounting.JournalLine{AccountCode: accounting.AccountGSTPayable, Credit: taxTotal})
		}
		_, err := accounting.PostJournalEntry(ctx, tx, o.BranchID, "sale", orderID, "Offline sale (synced)", claims.UserID, lines)
		if err == nil {
			res.Status = "ok"
		}
		return err
	})

	res.NegativeStock = negativeStock
	if err != nil && res.Status == "" {
		res.Status = "error"
		res.Error = err.Error()
	}
	return res
}

// applySyncSale decrements stock unconditionally — unlike reserveStock/
// consumeReservation (internal/sales/reservation.go), which guard against
// overselling before it happens, a synced sale already physically
// happened at the register while the device was offline. Per
// phase0_1_design.md §3.5's conflict rule: never reject a sale that
// already happened in the real world — accept it, let on_hand go
// negative if it must, and flag it (the caller writes the NEGATIVE_STOCK
// audit_logs entry) for manual reconciliation instead.
func applySyncSale(ctx context.Context, tx pgx.Tx, branchID, variantID string, quantity float64, orderID, performedBy string) (wentNegative bool, err error) {
	// INSERT ... ON CONFLICT rather than a plain UPDATE: a device that
	// pulled its catalog before any stock_levels row existed for this
	// branch/variant would otherwise hit zero affected rows and fail the
	// whole sync entry for a reason that isn't the device's fault — the
	// sale already happened either way.
	var onHand float64
	err = tx.QueryRow(ctx, `
		INSERT INTO stock_levels (id, merchant_id, branch_id, variant_id, on_hand, reserved, version)
		VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, -$3::numeric, 0, 0)
		ON CONFLICT (branch_id, variant_id) DO UPDATE SET
		  on_hand = stock_levels.on_hand - $3::numeric, version = stock_levels.version + 1, updated_at = now()
		RETURNING on_hand`, branchID, variantID, quantity,
	).Scan(&onHand)
	if err != nil {
		return false, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO stock_movements (merchant_id, branch_id, variant_id, movement_type, quantity_delta, reference_type, reference_id, performed_by)
		VALUES (current_setting('app.tenant_id')::uuid, $1, $2, 'sale', $3, 'sales_order', $4, $5)`,
		branchID, variantID, -quantity, orderID, performedBy); err != nil {
		return false, err
	}

	return onHand < 0, nil
}
