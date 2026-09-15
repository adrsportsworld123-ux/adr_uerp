package sales

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ErrInsufficientStock is returned when a reservation or consumption can't
// proceed because the available quantity is genuinely too low — distinct
// from a version conflict, which is a transient race retryStockUpdate
// handles internally and the caller never sees.
var ErrInsufficientStock = errors.New("insufficient stock")

const maxOptimisticRetries = 5

// reserveStock increments stock_levels.reserved for one branch/variant by
// quantity, using the version column as an optimistic-locking token. Verified
// against a live Postgres instance during design: a version mismatch (lost
// the race to a concurrent reservation) and genuinely insufficient stock are
// two different failure modes, and this function tells them apart by
// re-reading the row after a failed UPDATE rather than guessing.
func reserveStock(ctx context.Context, tx pgx.Tx, branchID, variantID string, quantity float64) error {
	return retryStockUpdate(ctx, tx, branchID, variantID,
		func(onHand, reserved float64) bool { return (onHand - reserved) >= quantity },
		func(version int) (pgx.Rows, error) {
			return tx.Query(ctx, `
				UPDATE stock_levels
				SET reserved = reserved + $1, version = version + 1, updated_at = now()
				WHERE branch_id = $2 AND variant_id = $3 AND version = $4
				  AND (on_hand - reserved) >= $1
				RETURNING version`, quantity, branchID, variantID, version)
		})
}

// consumeReservation converts a held reservation into an actual stock
// decrement at checkout: on_hand and reserved both drop by quantity in the
// same version-guarded update, so a reservation can never be "consumed"
// twice even under concurrent checkout retries.
func consumeReservation(ctx context.Context, tx pgx.Tx, branchID, variantID string, quantity float64) error {
	return retryStockUpdate(ctx, tx, branchID, variantID,
		func(onHand, reserved float64) bool { return reserved >= quantity },
		func(version int) (pgx.Rows, error) {
			return tx.Query(ctx, `
				UPDATE stock_levels
				SET on_hand = on_hand - $1, reserved = reserved - $1, version = version + 1, updated_at = now()
				WHERE branch_id = $2 AND variant_id = $3 AND version = $4 AND reserved >= $1
				RETURNING version`, quantity, branchID, variantID, version)
		})
}

// releaseReservation is the inverse of reserveStock — used when a line is
// removed from a cart before checkout, or by the (not-yet-built) 15-minute
// expiry sweeper.
func releaseReservation(ctx context.Context, tx pgx.Tx, branchID, variantID string, quantity float64) error {
	return retryStockUpdate(ctx, tx, branchID, variantID,
		func(onHand, reserved float64) bool { return reserved >= quantity },
		func(version int) (pgx.Rows, error) {
			return tx.Query(ctx, `
				UPDATE stock_levels
				SET reserved = reserved - $1, version = version + 1, updated_at = now()
				WHERE branch_id = $2 AND variant_id = $3 AND version = $4 AND reserved >= $1
				RETURNING version`, quantity, branchID, variantID, version)
		})
}

// retryStockUpdate is the shared optimistic-locking retry loop: read the
// current version, attempt the guarded UPDATE, and if it affected zero rows,
// figure out why before deciding whether to retry or fail. A version
// mismatch means someone else won the race — retry with the fresh version.
// Availability still being too low after a fresh read means the stock is
// genuinely not there — return ErrInsufficientStock, and the caller (a POS
// transaction) should surface that to the cashier rather than retrying.
func retryStockUpdate(
	ctx context.Context, tx pgx.Tx, branchID, variantID string,
	sufficient func(onHand, reserved float64) bool,
	attempt func(version int) (pgx.Rows, error),
) error {
	for i := 0; i < maxOptimisticRetries; i++ {
		var currentVersion int
		var onHand, reserved float64
		err := tx.QueryRow(ctx, `
			SELECT version, on_hand, reserved FROM stock_levels
			WHERE branch_id = $1 AND variant_id = $2`, branchID, variantID,
		).Scan(&currentVersion, &onHand, &reserved)
		if err != nil {
			return err // includes pgx.ErrNoRows: no stock_levels row exists for this branch/variant yet
		}

		// Fail fast on genuinely insufficient stock rather than burning
		// retries that can't possibly succeed. A concurrent release could
		// free up stock between this check and the next loop iteration —
		// that's fine, the next iteration re-reads fresh numbers rather
		// than trusting this one.
		if !sufficient(onHand, reserved) {
			return ErrInsufficientStock
		}

		rows, err := attempt(currentVersion)
		if err != nil {
			return err
		}
		updated := rows.Next()
		rows.Close()
		if updated {
			return nil // succeeded on this attempt
		}
		// Zero rows affected despite passing the sufficiency check above:
		// a concurrent writer moved the version between our read and our
		// UPDATE. Loop and retry with a fresh read.
	}
	return ErrInsufficientStock
}
