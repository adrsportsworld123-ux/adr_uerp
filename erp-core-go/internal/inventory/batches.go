// Phase 8: Vertical Expansion — Grocery/FMCG's batch/lot + expiry
// tracking and FIFO valuation. See migrations/029_grocery.sql's header
// for the design constraint this honors: stock_levels' optimistic-locked
// on_hand/reserved/version (internal/sales/reservation.go) stays the one
// unchanged authority for "is there enough stock" — everything here is
// an additive layer on top, active only for a variant that opts in via
// product_variants.track_batch, deciding WHICH batch a sale draws from
// (oldest-expiry-first — the actual FIFO valuation this phase asks for)
// and refusing to draw from an expired one.
package inventory

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// ErrExpiredOrUntrackedStock means stock_levels showed enough total
// quantity to reserve, but not enough of it is in a real, non-expired
// batch — either every remaining unit is past its expiry_date, or some
// of stock_levels.on_hand was never batch-attributed at all (a manual
// adjustment/reconciliation touched this variant, which migrations/
// 029_grocery.sql's header discloses as not batch-aware yet). The caller
// must release whatever stock_levels reservation it already made.
var ErrExpiredOrUntrackedStock = errors.New("no unexpired, batch-tracked stock is available to fulfill this quantity")

type BatchAllocation struct {
	BatchID  string
	Quantity float64
}

// AllocateBatchesFIFO greedily allocates quantity across variantID's
// non-expired batches at branchID, oldest-expiry-first (NULLs — no
// tracked expiry — last, then oldest-received first), decrementing each
// allocated batch's quantity_remaining in the same transaction. FOR
// UPDATE on the batch read serializes concurrent allocations against the
// same batch, the same reasoning stock_levels.version exists for at the
// variant level, just via a row lock instead of an optimistic retry
// (batch contention is rare enough at this scale not to need the
// optimistic-retry machinery reservation.go uses for the much hotter
// per-variant path).
//
// minShelfLifeDays (Phase 8, Pharmacy) is a plain, vertical-agnostic
// buffer on top of "not literally expired" — a batch expiring within
// that many days from today is treated the same as an already-expired
// one for this call. 0 (the default for every category that hasn't set
// it) reproduces Grocery/FMCG's original behavior exactly.
func AllocateBatchesFIFO(ctx context.Context, tx pgx.Tx, branchID, variantID string, quantity float64, minShelfLifeDays int) ([]BatchAllocation, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, quantity_remaining FROM stock_batches
		WHERE branch_id = $1 AND variant_id = $2 AND quantity_remaining > 0
		  AND (expiry_date IS NULL OR expiry_date >= current_date + make_interval(days => $3::int))
		ORDER BY expiry_date ASC NULLS LAST, received_at ASC
		FOR UPDATE`, branchID, variantID, minShelfLifeDays)
	if err != nil {
		return nil, err
	}
	type batchRow struct {
		id        string
		remaining float64
	}
	var batches []batchRow
	for rows.Next() {
		var b batchRow
		if err := rows.Scan(&b.id, &b.remaining); err != nil {
			rows.Close()
			return nil, err
		}
		batches = append(batches, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	remaining := quantity
	var allocations []BatchAllocation
	for _, b := range batches {
		if remaining <= 0 {
			break
		}
		take := b.remaining
		if take > remaining {
			take = remaining
		}
		allocations = append(allocations, BatchAllocation{BatchID: b.id, Quantity: take})
		remaining -= take
	}
	if remaining > 1e-9 {
		return nil, ErrExpiredOrUntrackedStock
	}

	for _, a := range allocations {
		if _, err := tx.Exec(ctx, `UPDATE stock_batches SET quantity_remaining = quantity_remaining - $1 WHERE id = $2`,
			a.Quantity, a.BatchID); err != nil {
			return nil, err
		}
	}
	return allocations, nil
}

// RecordLineBatchAllocations persists which batches a sales_order_line
// drew from — called right after AllocateBatchesFIFO succeeds and the
// line itself has been inserted (so its real id exists to reference).
func RecordLineBatchAllocations(ctx context.Context, tx pgx.Tx, lineID string, allocations []BatchAllocation) error {
	for _, a := range allocations {
		if _, err := tx.Exec(ctx, `
			INSERT INTO sales_order_line_batches (id, sales_order_line_id, batch_id, quantity)
			VALUES (gen_random_uuid(), $1, $2, $3)`, lineID, a.BatchID, a.Quantity); err != nil {
			return err
		}
	}
	return nil
}

// ReleaseLineBatchAllocations is AllocateBatchesFIFO's inverse — called
// by DeleteLine alongside the existing releaseReservation, for a line
// that had batch allocations recorded against it.
func ReleaseLineBatchAllocations(ctx context.Context, tx pgx.Tx, lineID string) error {
	rows, err := tx.Query(ctx, `SELECT batch_id, quantity FROM sales_order_line_batches WHERE sales_order_line_id = $1`, lineID)
	if err != nil {
		return err
	}
	type alloc struct {
		batchID  string
		quantity float64
	}
	var allocs []alloc
	for rows.Next() {
		var a alloc
		if err := rows.Scan(&a.batchID, &a.quantity); err != nil {
			rows.Close()
			return err
		}
		allocs = append(allocs, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, a := range allocs {
		if _, err := tx.Exec(ctx, `UPDATE stock_batches SET quantity_remaining = quantity_remaining + $1 WHERE id = $2`,
			a.quantity, a.batchID); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `DELETE FROM sales_order_line_batches WHERE sales_order_line_id = $1`, lineID)
	return err
}

// ReceiveBatch is GRN's CompleteGRN calling into this to create or
// top up a batch — the real point of origin for batch/expiry data (a
// supplier delivery note has it; a manual stock adjustment or
// reconciliation doesn't, which is exactly why those paths stay
// unaware of stock_batches for now). Same batch_no delivered again for
// the same branch/variant adds to the existing batch rather than
// creating a duplicate row, keeping its original expiry_date.
func ReceiveBatch(ctx context.Context, tx pgx.Tx, branchID, variantID, batchNo string, expiryDate *string, costPrice, quantity float64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO stock_batches (id, merchant_id, branch_id, variant_id, batch_no, expiry_date, cost_price, quantity_received, quantity_remaining)
		VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4::date, $5, $6, $6)
		ON CONFLICT (branch_id, variant_id, batch_no) DO UPDATE SET
			quantity_received = stock_batches.quantity_received + EXCLUDED.quantity_received,
			quantity_remaining = stock_batches.quantity_remaining + EXCLUDED.quantity_remaining`,
		branchID, variantID, batchNo, expiryDate, costPrice, quantity)
	return err
}

// ---------------------------------------------------------------------
// GET /inventory/expiring-batches?branch_id=&days= — the real "expiry
// tracking" report: every non-exhausted batch expiring within `days`
// (default 7), soonest first. Already-expired batches (expiry_date in
// the past) are included too — they're exactly what "stricter expiry
// compliance" needs surfaced, not hidden once the deadline passes.
// ---------------------------------------------------------------------

type ExpiringBatch struct {
	BatchID           string  `json:"batch_id"`
	BranchID          string  `json:"branch_id"`
	VariantID         string  `json:"variant_id"`
	SKU               string  `json:"sku"`
	ProductName       string  `json:"product_name"`
	BatchNo           string  `json:"batch_no"`
	ExpiryDate        *string `json:"expiry_date"`
	QuantityRemaining string  `json:"quantity_remaining"`
	DaysUntilExpiry   *int    `json:"days_until_expiry"` // negative = already expired
}

func (h *Handler) GetExpiringBatches(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	branchID := r.URL.Query().Get("branch_id")
	days := 7
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 {
		days = v
	}

	batches := []ExpiringBatch{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			WITH params AS (
				SELECT $1::int AS window_days, NULLIF($2,'')::uuid AS branch_filter
			)
			SELECT sb.id, sb.branch_id, sb.variant_id, pv.sku, p.name, sb.batch_no,
			       sb.expiry_date::text, sb.quantity_remaining::text,
			       (sb.expiry_date - current_date)
			FROM stock_batches sb
			JOIN product_variants pv ON pv.id = sb.variant_id
			JOIN products p ON p.id = pv.product_id
			CROSS JOIN params
			WHERE sb.quantity_remaining > 0
			  AND (params.branch_filter IS NULL OR sb.branch_id = params.branch_filter)
			  AND sb.expiry_date IS NOT NULL
			  AND sb.expiry_date <= current_date + make_interval(days => params.window_days)
			ORDER BY sb.expiry_date ASC`, days, branchID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b ExpiringBatch
			if err := rows.Scan(&b.BatchID, &b.BranchID, &b.VariantID, &b.SKU, &b.ProductName, &b.BatchNo,
				&b.ExpiryDate, &b.QuantityRemaining, &b.DaysUntilExpiry); err != nil {
				return err
			}
			batches = append(batches, b)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list expiring batches")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"window_days": days, "batches": batches})
}
