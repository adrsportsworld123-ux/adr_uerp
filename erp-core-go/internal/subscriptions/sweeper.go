package subscriptions

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/db"
)

// RunSweeper persists lifecycle transitions (Effective already enforces
// them on every read) and issues one renewal invoice when a paid period
// ends. Same per-merchant WithTenant loop as the other sweepers.
func RunSweeper(ctx context.Context, database *db.DB, interval time.Duration) {
	SweepOnce(ctx, database)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			SweepOnce(ctx, database)
		}
	}
}

func SweepOnce(ctx context.Context, database *db.DB) {
	rows, err := database.Pool.Query(ctx, `SELECT id FROM merchants WHERE status = 'active'`)
	if err != nil {
		log.Printf("subscription sweeper: list merchants: %v", err)
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		changed := false
		err := database.WithTenant(ctx, id, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			changed, err = sweepMerchant(ctx, tx, time.Now())
			return err
		})
		if err != nil {
			log.Printf("subscription sweeper: merchant %s: %v", id, err)
		}
		if changed {
			Invalidate(id)
		}
	}
}

func sweepMerchant(ctx context.Context, tx pgx.Tx, now time.Time) (bool, error) {
	s, found, err := loadStored(ctx, tx)
	if err != nil || !found {
		return false, err
	}
	plan, err := LoadPlan(ctx, tx, s.PlanCode)
	if err != nil {
		return false, err
	}
	e := Effective(plan, s, now)
	// A paid period (not a trial) that has run out gets its renewal
	// invoice, once (unique per period start).
	if s.Status == StatusActive && s.CurrentPeriodEnd != nil && !now.Before(*s.CurrentPeriodEnd) && !plan.Free() {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM billing_invoices WHERE kind = 'renewal' AND status <> 'void'
			AND period_start = $1)`, *s.CurrentPeriodEnd).Scan(&exists); err != nil {
			return false, err
		}
		if !exists {
			if _, err := CreateInvoice(ctx, tx, "renewal", plan, *s.CurrentPeriodEnd); err != nil {
				return false, err
			}
		}
	}
	if e.Status == s.Status && ptrEq(e.GraceEndsAt, s.GraceEndsAt) && ptrEq(e.ReadOnlySince, s.ReadOnlySince) {
		return false, nil
	}
	_, err = tx.Exec(ctx, `UPDATE subscriptions SET status = $1, grace_ends_at = $2, read_only_since = $3, updated_at = now()`,
		e.Status, e.GraceEndsAt, e.ReadOnlySince)
	return err == nil, err
}

func ptrEq(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
