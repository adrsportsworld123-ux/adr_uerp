package quotations

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/db"
)

// RunExpirySweeper marks a quotation 'expired' once its valid_until date
// has passed, unless it already reached a terminal state (converted,
// rejected, or already expired) — an accepted-but-never-converted quote
// still expires, since prices/stock may have moved on by then. Same
// per-merchant-loop shape as internal/sales.RunExpirySweeper and
// internal/customers.RunReceivableReminderSweeper, for the same reason:
// this sweeper runs as the same RLS-restricted role as every request
// handler, so it can only see one tenant's rows at a time via
// db.WithTenant.
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
		log.Printf("quotation-expiry sweeper: list merchants: %v", err)
		return
	}
	var merchantIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			log.Printf("quotation-expiry sweeper: scan merchant id: %v", err)
			return
		}
		merchantIDs = append(merchantIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("quotation-expiry sweeper: list merchants: %v", err)
		return
	}

	for _, merchantID := range merchantIDs {
		if err := database.WithTenant(ctx, merchantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `
				UPDATE quotations SET status = 'expired', updated_at = now()
				WHERE status IN ('draft','sent','accepted') AND valid_until < current_date`)
			return err
		}); err != nil {
			log.Printf("quotation-expiry sweeper: merchant %s: %v", merchantID, err)
		}
	}
}
