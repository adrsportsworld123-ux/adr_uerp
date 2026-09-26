package sales

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/db"
	"erp-core-go/internal/inventory"
)

// RunExpirySweeper releases stock reservations whose 15-minute hold
// (phase0_1_design.md §2.3) has expired without a checkout — the roadmap's
// "15-minute reservation-expiry sweeper" item. Runs until ctx is cancelled;
// call it as `go sales.RunExpirySweeper(ctx, database, time.Minute)` from
// main.go.
//
// Loops per-merchant rather than one cross-tenant query, because the
// sweeper runs as the same app_user role as every request handler — it has
// no RLS bypass, so it can only see one tenant's reservations at a time via
// db.WithTenant, exactly like every other write in this codebase. Fine at
// Phase 1 scale (a handful of merchants); worth a dedicated BYPASSRLS
// service role and a single query if the merchant count ever makes N
// sequential per-tenant sweeps too slow for a 1-minute tick.
func RunExpirySweeper(ctx context.Context, database *db.DB, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweepOnce(ctx, database)
		}
	}
}

func sweepOnce(ctx context.Context, database *db.DB) {
	rows, err := database.Pool.Query(ctx, `SELECT id FROM merchants WHERE status = 'active'`)
	if err != nil {
		log.Printf("sweeper: list merchants: %v", err)
		return
	}
	var merchantIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			log.Printf("sweeper: scan merchant id: %v", err)
			return
		}
		merchantIDs = append(merchantIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("sweeper: list merchants: %v", err)
		return
	}

	for _, merchantID := range merchantIDs {
		if err := sweepTenant(ctx, database, merchantID); err != nil {
			log.Printf("sweeper: merchant %s: %v", merchantID, err)
		}
	}
}

func sweepTenant(ctx context.Context, database *db.DB, merchantID string) error {
	return database.WithTenant(ctx, merchantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, branch_id, variant_id, quantity, sales_order_id FROM stock_reservations
			WHERE status = 'active' AND expires_at < now()`)
		if err != nil {
			return err
		}
		type expired struct {
			id, branchID, variantID, salesOrderID string
			quantity                              float64
		}
		var expiredReservations []expired
		for rows.Next() {
			var e expired
			if err := rows.Scan(&e.id, &e.branchID, &e.variantID, &e.quantity, &e.salesOrderID); err != nil {
				rows.Close()
				return err
			}
			expiredReservations = append(expiredReservations, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, e := range expiredReservations {
			if err := releaseReservation(ctx, tx, e.branchID, e.variantID, e.quantity); err != nil {
				return err
			}
			// Phase 8 (Grocery/FMCG): a reservation that expires here (the
			// cashier walked away, never explicitly removed the line) needs
			// the same batch-quantity restoration DeleteLine already does —
			// otherwise an expired-away cart permanently "leaks" batch
			// stock that stock_levels itself correctly got back. Same
			// (order, variant, quantity) matching DeleteLine's own reverse
			// lookup uses, since stock_reservations has no direct FK to the
			// line it was created for; a no-op if no matching line exists
			// (already checked out or deleted through a different path).
			var lineID string
			err := tx.QueryRow(ctx, `
				SELECT id FROM sales_order_lines
				WHERE sales_order_id = $1 AND variant_id = $2 AND quantity = $3
				ORDER BY created_at DESC LIMIT 1`, e.salesOrderID, e.variantID, e.quantity,
			).Scan(&lineID)
			if err != nil && err != pgx.ErrNoRows {
				return err
			}
			if err == nil {
				if err := inventory.ReleaseLineBatchAllocations(ctx, tx, lineID); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE stock_reservations SET status = 'expired' WHERE id = $1`, e.id); err != nil {
				return err
			}
		}
		return nil
	})
}
