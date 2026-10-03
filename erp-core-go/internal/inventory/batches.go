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
// of stock_levels.on_hand was never batch-attributed at all (stock that
// predates batch tracking, or drift from before migrations/034 made the
// non-sale stock paths batch-aware). The caller must release whatever
// stock_levels reservation it already made.
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
// top up a batch — the main point of origin for batch/expiry data (a
// supplier delivery note has it). Adjustments, reconciliation and
// transfer receipts use ReceiveBatchWithMovement below. Same batch_no
// delivered again for the same branch/variant adds to the existing batch
// rather than creating a duplicate row, keeping its original expiry_date.
func ReceiveBatch(ctx context.Context, tx pgx.Tx, branchID, variantID, batchNo string, expiryDate *string, costPrice, quantity float64) error {
	_, err := ReceiveBatchReturningID(ctx, tx, branchID, variantID, batchNo, expiryDate, costPrice, quantity)
	return err
}

// ReceiveBatchReturningID is ReceiveBatch for callers that also need to
// write a stock_batch_movements row against the batch it landed in.
func ReceiveBatchReturningID(ctx context.Context, tx pgx.Tx, branchID, variantID, batchNo string, expiryDate *string, costPrice, quantity float64) (string, error) {
	var batchID string
	err := tx.QueryRow(ctx, `
		INSERT INTO stock_batches (id, merchant_id, branch_id, variant_id, batch_no, expiry_date, cost_price, quantity_received, quantity_remaining)
		VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4::date, $5, $6, $6)
		ON CONFLICT (branch_id, variant_id, batch_no) DO UPDATE SET
			quantity_received = stock_batches.quantity_received + EXCLUDED.quantity_received,
			quantity_remaining = stock_batches.quantity_remaining + EXCLUDED.quantity_remaining
		RETURNING id`,
		branchID, variantID, batchNo, expiryDate, costPrice, quantity).Scan(&batchID)
	return batchID, err
}

// ---------------------------------------------------------------------
// Batch-aware non-sale stock paths (migrations/034's header). Every path
// that moves stock_levels.on_hand for a batch-tracked variant now moves
// stock_batches in the same transaction, so the two can't drift apart:
// manual adjustments, reconciliation, purchase returns, branch transfers,
// voids and offline-synced sales. stock_levels stays the authority for
// "how much"; these only keep the batch split truthful underneath it.
// ---------------------------------------------------------------------

// ErrBatchRequired: stock is being ADDED to a batch-tracked variant
// without saying which batch it is — same rule GRN already enforces
// (internal/purchase/grn.go), now for adjustments and reconciliation too.
var ErrBatchRequired = errors.New("batch_no is required when adding stock to a batch-tracked variant")

// ErrBatchInsufficient: a caller named a specific batch to take stock
// from, and that batch doesn't exist at this branch or has less left
// than requested.
var ErrBatchInsufficient = errors.New("the named batch does not have enough remaining quantity at this branch")

// VariantTracksBatch reports product_variants.track_batch.
func VariantTracksBatch(ctx context.Context, tx pgx.Tx, variantID string) (bool, error) {
	var tracks bool
	err := tx.QueryRow(ctx, `SELECT track_batch FROM product_variants WHERE id = $1`, variantID).Scan(&tracks)
	return tracks, err
}

// DrainOrder picks which batches an unnamed removal draws from first.
type DrainOrder int

const (
	// DrainExpiredFirst — write-offs, count shortfalls, supplier returns:
	// stock that has expired (or is closest to it) is what physically
	// gets binned or sent back, so it leaves the books first.
	DrainExpiredFirst DrainOrder = iota
	// DrainSellableFirst — transfers and offline sales: goods that
	// actually moved were sellable ones, oldest-expiry-first (the same
	// FEFO order AllocateBatchesFIFO uses), touching expired batches
	// only once nothing sellable is left.
	DrainSellableFirst
)

// BatchDraw is one batch's share of a DrainBatches removal, carrying
// enough of the batch's identity for a transfer to recreate it at the
// destination branch.
type BatchDraw struct {
	BatchID    string
	BatchNo    string
	ExpiryDate *string
	CostPrice  float64
	Quantity   float64
}

// DrainBatches removes quantity from variantID's batches at branchID.
//
// With batchNo set, it comes from that one batch only, or the call
// fails with ErrBatchInsufficient — the caller asked for something
// specific, so a silent substitution would be wrong.
//
// With batchNo empty it is best-effort: drawn in the given order and
// clamped at whatever the batches hold. on_hand is the authority and has
// already been moved by the caller; if batches hold less than on_hand
// (stock that predates batch tracking, or legacy drift from before these
// paths were batch-aware), the shortfall simply comes out of the
// untracked remainder — which AllocateBatchesFIFO already refuses to
// sell, so this can only ever fail closed. Callers that need to know how
// much actually moved sum the returned draws.
//
// Every draw writes a stock_batch_movements row (refType/refID).
func DrainBatches(ctx context.Context, tx pgx.Tx, branchID, variantID string, quantity float64, batchNo string, order DrainOrder, refType, refID, performedBy string) ([]BatchDraw, error) {
	orderBy := `expiry_date ASC NULLS LAST, received_at ASC`
	if order == DrainSellableFirst {
		orderBy = `(expiry_date IS NOT NULL AND expiry_date < current_date) ASC, expiry_date ASC NULLS LAST, received_at ASC`
	}
	rows, err := tx.Query(ctx, `
		SELECT id, batch_no, expiry_date::text, cost_price, quantity_remaining FROM stock_batches
		WHERE branch_id = $1 AND variant_id = $2 AND quantity_remaining > 0
		  AND ($3 = '' OR batch_no = $3)
		ORDER BY `+orderBy+`
		FOR UPDATE`, branchID, variantID, batchNo)
	if err != nil {
		return nil, err
	}
	var candidates []BatchDraw
	for rows.Next() {
		var b BatchDraw
		if err := rows.Scan(&b.BatchID, &b.BatchNo, &b.ExpiryDate, &b.CostPrice, &b.Quantity); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if batchNo != "" {
		if len(candidates) == 0 || candidates[0].Quantity < quantity-1e-9 {
			return nil, ErrBatchInsufficient
		}
	}

	remaining := quantity
	var draws []BatchDraw
	for _, b := range candidates {
		if remaining <= 1e-9 {
			break
		}
		take := b.Quantity
		if take > remaining {
			take = remaining
		}
		b.Quantity = take
		draws = append(draws, b)
		remaining -= take
	}

	for _, d := range draws {
		if _, err := tx.Exec(ctx, `UPDATE stock_batches SET quantity_remaining = quantity_remaining - $1 WHERE id = $2`,
			d.Quantity, d.BatchID); err != nil {
			return nil, err
		}
		if err := RecordBatchMovement(ctx, tx, d.BatchID, -d.Quantity, refType, refID, performedBy); err != nil {
			return nil, err
		}
	}
	return draws, nil
}

// RecordBatchMovement appends one stock_batch_movements row. refID and
// performedBy may be empty.
func RecordBatchMovement(ctx context.Context, tx pgx.Tx, batchID string, delta float64, refType, refID, performedBy string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO stock_batch_movements (merchant_id, batch_id, quantity_delta, reference_type, reference_id, performed_by)
		VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, NULLIF($4,'')::uuid, NULLIF($5,'')::uuid)`,
		batchID, delta, refType, refID, performedBy)
	return err
}

// ReceiveBatchWithMovement is ReceiveBatchReturningID plus the matching
// stock_batch_movements row — the inbound counterpart of DrainBatches for
// adjustments, reconciliation and transfer receipts.
func ReceiveBatchWithMovement(ctx context.Context, tx pgx.Tx, branchID, variantID, batchNo string, expiryDate *string, costPrice, quantity float64, refType, refID, performedBy string) error {
	batchID, err := ReceiveBatchReturningID(ctx, tx, branchID, variantID, batchNo, expiryDate, costPrice, quantity)
	if err != nil {
		return err
	}
	return RecordBatchMovement(ctx, tx, batchID, quantity, refType, refID, performedBy)
}

// ReleaseLineBatchQuantity gives back quantity of a cart line's batch
// allocation — the partial form of ReleaseLineBatchAllocations, for a
// quantity DECREASE via PATCH .../lines/{line_id}. Releases from the
// latest-expiry allocation first, so the line keeps the oldest stock it
// was allocated (FIFO stays honored for what's still being sold).
func ReleaseLineBatchQuantity(ctx context.Context, tx pgx.Tx, lineID string, quantity float64) error {
	rows, err := tx.Query(ctx, `
		SELECT slb.id, slb.batch_id, slb.quantity
		FROM sales_order_line_batches slb
		JOIN stock_batches sb ON sb.id = slb.batch_id
		WHERE slb.sales_order_line_id = $1
		ORDER BY sb.expiry_date DESC NULLS FIRST, sb.received_at DESC`, lineID)
	if err != nil {
		return err
	}
	type alloc struct {
		id, batchID string
		quantity    float64
	}
	var allocs []alloc
	for rows.Next() {
		var a alloc
		if err := rows.Scan(&a.id, &a.batchID, &a.quantity); err != nil {
			rows.Close()
			return err
		}
		allocs = append(allocs, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	remaining := quantity
	for _, a := range allocs {
		if remaining <= 1e-9 {
			break
		}
		give := a.quantity
		if give > remaining {
			give = remaining
		}
		if _, err := tx.Exec(ctx, `UPDATE stock_batches SET quantity_remaining = quantity_remaining + $1 WHERE id = $2`,
			give, a.batchID); err != nil {
			return err
		}
		if give >= a.quantity-1e-9 {
			_, err = tx.Exec(ctx, `DELETE FROM sales_order_line_batches WHERE id = $1`, a.id)
		} else {
			_, err = tx.Exec(ctx, `UPDATE sales_order_line_batches SET quantity = quantity - $1 WHERE id = $2`, give, a.id)
		}
		if err != nil {
			return err
		}
		remaining -= give
	}
	return nil
}

// RestoreLineBatchesForVoid returns a finalized sale line's batch
// allocations to their batches. Unlike ReleaseLineBatchAllocations (a
// cart line that never sold), the sales_order_line_batches rows are KEPT
// — the sale did happen and was then reversed, and recall traceability
// should show both; the reversal is the 'void' stock_batch_movements row.
func RestoreLineBatchesForVoid(ctx context.Context, tx pgx.Tx, lineID, orderID, performedBy string) error {
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
		if err := RecordBatchMovement(ctx, tx, a.batchID, a.quantity, "void", orderID, performedBy); err != nil {
			return err
		}
	}
	return nil
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
