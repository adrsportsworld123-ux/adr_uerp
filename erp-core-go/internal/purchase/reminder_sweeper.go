package purchase

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/db"
	"erp-core-go/internal/notifications"
)

// RunPaymentReminderSweeper is the roadmap's "payment reminders" item,
// substituted from customer-facing (which needs Phase 4's credit-facility
// data model — see migrations/013_notifications.sql's header comment) to
// supplier-bill-facing: reminds accounts staff of this merchant's own
// payables coming due or already overdue, using Phase 2's
// purchase_bills.due_date. Mirrors internal/sales.RunExpirySweeper and
// internal/inventory.RunLowStockSweeper's per-tenant WithTenant loop.
//
// Dedup is time-based, not state-based (unlike low-stock's alerted_at/
// on_hand comparison): last_reminder_sent_at just needs to be more than
// 24h old for a still-unpaid bill to remind again, since "due in 3 days"
// naturally becomes "due in 2 days," "1 day," "overdue" as real time
// passes — a fresh reminder each day is the correct behavior here, not a
// bug the low-stock episode-based dedup would need to guard against.
func RunPaymentReminderSweeper(ctx context.Context, database *db.DB, notify *notifications.Handler, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweepPaymentRemindersOnce(ctx, database, notify)
		}
	}
}

func sweepPaymentRemindersOnce(ctx context.Context, database *db.DB, notify *notifications.Handler) {
	rows, err := database.Pool.Query(ctx, `SELECT id FROM merchants WHERE status = 'active'`)
	if err != nil {
		log.Printf("payment-reminder sweeper: list merchants: %v", err)
		return
	}
	var merchantIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			log.Printf("payment-reminder sweeper: scan merchant id: %v", err)
			return
		}
		merchantIDs = append(merchantIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("payment-reminder sweeper: list merchants: %v", err)
		return
	}

	for _, merchantID := range merchantIDs {
		if err := sweepPaymentRemindersTenant(ctx, database, notify, merchantID); err != nil {
			log.Printf("payment-reminder sweeper: merchant %s: %v", merchantID, err)
		}
	}
}

func sweepPaymentRemindersTenant(ctx context.Context, database *db.DB, notify *notifications.Handler, merchantID string) error {
	return database.WithTenant(ctx, merchantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT pb.id, pb.bill_number, s.name,
			       (pb.grand_total - pb.amount_paid)::text, pb.due_date::text,
			       pb.due_date < CURRENT_DATE AS overdue
			FROM purchase_bills pb
			JOIN suppliers s ON s.id = pb.supplier_id
			WHERE pb.status IN ('unpaid', 'partially_paid')
			  AND pb.due_date IS NOT NULL
			  AND pb.due_date <= CURRENT_DATE + INTERVAL '3 days'
			  AND (pb.last_reminder_sent_at IS NULL OR pb.last_reminder_sent_at < now() - INTERVAL '24 hours')`)
		if err != nil {
			return err
		}
		type dueBill struct {
			id, billNumber, supplierName, amountDue, dueDate string
			overdue                                          bool
		}
		var bills []dueBill
		for rows.Next() {
			var b dueBill
			if err := rows.Scan(&b.id, &b.billNumber, &b.supplierName, &b.amountDue, &b.dueDate, &b.overdue); err != nil {
				rows.Close()
				return err
			}
			bills = append(bills, b)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(bills) == 0 {
			return nil
		}

		recipients, err := fetchManagerEmails(ctx, tx)
		if err != nil {
			return err
		}

		for _, b := range bills {
			status := "is due"
			if b.overdue {
				status = "is OVERDUE"
			}
			subject := fmt.Sprintf("Bill %s %s", b.billNumber, status)
			body := fmt.Sprintf("Bill %s from %s: Rs.%s %s on %s.", b.billNumber, b.supplierName, b.amountDue, status, b.dueDate)
			for _, email := range recipients {
				notify.DispatchEmail(ctx, tx, "payment_reminder", email, subject, body, "purchase_bill", b.id)
			}
			if _, err := tx.Exec(ctx, `UPDATE purchase_bills SET last_reminder_sent_at = now() WHERE id = $1`, b.id); err != nil {
				return err
			}
		}
		return nil
	})
}

// fetchManagerEmails — see internal/inventory/sweeper.go's identical
// helper for why this is duplicated rather than shared.
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
