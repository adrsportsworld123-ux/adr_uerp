package inventory

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/db"
	"erp-core-go/internal/notifications"
)

// RunLowStockSweeper is the roadmap's "low-stock alerts" item — periodic,
// not event-driven off every stock-mutating call site (checkout, manual
// adjustment, branch transfer completion each move stock independently;
// hooking all three would scatter this concern across three packages for
// no real benefit at pilot-store scale). Mirrors
// internal/sales.RunExpirySweeper's per-tenant WithTenant loop exactly —
// same reasoning: the sweeper runs as the same RLS-restricted app_user
// role as every request, so it sweeps tenants one at a time.
//
// Dedups via stock_levels.low_stock_alerted_at: set the moment a row
// crosses at/below reorder_point, cleared the moment it recovers above —
// so a continuously-low variant is alerted exactly once per "episode,"
// not once per tick.
func RunLowStockSweeper(ctx context.Context, database *db.DB, notify *notifications.Handler, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweepLowStockOnce(ctx, database, notify)
		}
	}
}

func sweepLowStockOnce(ctx context.Context, database *db.DB, notify *notifications.Handler) {
	rows, err := database.Pool.Query(ctx, `SELECT id FROM merchants WHERE status = 'active'`)
	if err != nil {
		log.Printf("low-stock sweeper: list merchants: %v", err)
		return
	}
	var merchantIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			log.Printf("low-stock sweeper: scan merchant id: %v", err)
			return
		}
		merchantIDs = append(merchantIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("low-stock sweeper: list merchants: %v", err)
		return
	}

	for _, merchantID := range merchantIDs {
		if err := sweepLowStockTenant(ctx, database, notify, merchantID); err != nil {
			log.Printf("low-stock sweeper: merchant %s: %v", merchantID, err)
		}
	}
}

func sweepLowStockTenant(ctx context.Context, database *db.DB, notify *notifications.Handler, merchantID string) error {
	return database.WithTenant(ctx, merchantID, func(ctx context.Context, tx pgx.Tx) error {
		// Recovered rows: clear the marker so a future dip alerts again.
		if _, err := tx.Exec(ctx, `
			UPDATE stock_levels SET low_stock_alerted_at = NULL
			WHERE low_stock_alerted_at IS NOT NULL AND on_hand > reorder_point`); err != nil {
			return err
		}

		rows, err := tx.Query(ctx, `
			SELECT sl.id, sl.on_hand::text, sl.reorder_point::text, b.name, p.name, pv.sku
			FROM stock_levels sl
			JOIN branches b ON b.id = sl.branch_id
			JOIN product_variants pv ON pv.id = sl.variant_id
			JOIN products p ON p.id = pv.product_id
			WHERE sl.reorder_point > 0 AND sl.on_hand <= sl.reorder_point AND sl.low_stock_alerted_at IS NULL`)
		if err != nil {
			return err
		}
		type lowStock struct {
			id, onHand, reorderPoint, branchName, productName, sku string
		}
		var breaches []lowStock
		for rows.Next() {
			var l lowStock
			if err := rows.Scan(&l.id, &l.onHand, &l.reorderPoint, &l.branchName, &l.productName, &l.sku); err != nil {
				rows.Close()
				return err
			}
			breaches = append(breaches, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(breaches) == 0 {
			return nil
		}

		recipients, err := fetchManagerEmails(ctx, tx)
		if err != nil {
			return err
		}

		for _, l := range breaches {
			body := fmt.Sprintf("%s (SKU %s) at %s is low on stock: %s on hand, reorder point is %s.",
				l.productName, l.sku, l.branchName, l.onHand, l.reorderPoint)
			for _, email := range recipients {
				notify.DispatchEmail(ctx, tx, "low_stock", email, fmt.Sprintf("Low stock: %s", l.productName), body, "stock_levels", l.id)
			}
			if _, err := tx.Exec(ctx, `UPDATE stock_levels SET low_stock_alerted_at = now() WHERE id = $1`, l.id); err != nil {
				return err
			}
		}
		return nil
	})
}

// fetchManagerEmails returns the current tenant's Branch Manager/Merchant
// Admin users with a usable email — this package's low-stock alert
// recipients. internal/purchase's bill-reminder sweeper has its own copy
// of this same small query rather than importing this package for it —
// a purchase -> inventory dependency for a generic user-role lookup would
// be a stranger coupling than duplicating ~10 lines of SQL.
func fetchManagerEmails(ctx context.Context, tx pgx.Tx) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT u.email
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id
		JOIN roles r ON r.id = ur.role_id
		WHERE r.name IN ('Branch Manager', 'Merchant Admin')
		  AND u.status = 'active' AND u.email IS NOT NULL AND u.email != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var emails []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		emails = append(emails, e)
	}
	return emails, rows.Err()
}
