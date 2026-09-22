package customers

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/db"
	"erp-core-go/internal/notifications"
)

// RunReceivableReminderSweeper closes the gap
// migrations/013_notifications.sql's header comment explicitly deferred:
// "customer-facing payment reminders stay deferred to Phase 4 with credit
// facility itself" — the FRD's actual pos_frd_complete.md §6 "Payment
// reminders (pre-due and overdue)" against a B2B customer's own
// outstanding balance, now that credit sales exist to have a balance at
// all. Mirrors internal/purchase.RunPaymentReminderSweeper almost
// exactly, but notifies the CUSTOMER (their own email/phone), not staff —
// the mirror image of that sweeper's payables-side reminder.
//
// Window is 7 days pre-due per the FRD's explicit number for this flow
// (§8: "Reminders: 7 days before, on due date, 3 days after, weekly") —
// simplified to "remind at most once per 24h while within the window or
// overdue" rather than that exact multi-stage cadence, the same
// simplification already applied to the supplier-bill reminder's own
// "3 days before or overdue" window.
func RunReceivableReminderSweeper(ctx context.Context, database *db.DB, notify *notifications.Handler, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweepReceivableRemindersOnce(ctx, database, notify)
		}
	}
}

func sweepReceivableRemindersOnce(ctx context.Context, database *db.DB, notify *notifications.Handler) {
	rows, err := database.Pool.Query(ctx, `SELECT id FROM merchants WHERE status = 'active'`)
	if err != nil {
		log.Printf("receivable-reminder sweeper: list merchants: %v", err)
		return
	}
	var merchantIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			log.Printf("receivable-reminder sweeper: scan merchant id: %v", err)
			return
		}
		merchantIDs = append(merchantIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("receivable-reminder sweeper: list merchants: %v", err)
		return
	}

	for _, merchantID := range merchantIDs {
		if err := sweepReceivableRemindersTenant(ctx, database, notify, merchantID); err != nil {
			log.Printf("receivable-reminder sweeper: merchant %s: %v", merchantID, err)
		}
	}
}

func sweepReceivableRemindersTenant(ctx context.Context, database *db.DB, notify *notifications.Handler, merchantID string) error {
	return database.WithTenant(ctx, merchantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT so.id, so.order_number, (so.credit_amount - so.credit_paid)::text, so.due_date::text,
			       so.due_date < CURRENT_DATE AS overdue, c.name, COALESCE(c.email,''), COALESCE(c.phone,'')
			FROM sales_orders so
			JOIN customers c ON c.id = so.customer_id
			WHERE so.status = 'finalized'
			  AND so.credit_amount > so.credit_paid
			  AND so.due_date IS NOT NULL
			  AND so.due_date <= CURRENT_DATE + INTERVAL '7 days'
			  AND (so.last_reminder_sent_at IS NULL OR so.last_reminder_sent_at < now() - INTERVAL '24 hours')`)
		if err != nil {
			return err
		}
		type dueInvoice struct {
			id, orderNumber, outstanding, dueDate, customerName, email, phone string
			overdue                                                           bool
		}
		var invoices []dueInvoice
		for rows.Next() {
			var inv dueInvoice
			if err := rows.Scan(&inv.id, &inv.orderNumber, &inv.outstanding, &inv.dueDate, &inv.overdue,
				&inv.customerName, &inv.email, &inv.phone); err != nil {
				rows.Close()
				return err
			}
			invoices = append(invoices, inv)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, inv := range invoices {
			word := "is due"
			if inv.overdue {
				word = "is OVERDUE"
			}
			subject := fmt.Sprintf("Invoice %s %s", inv.orderNumber, word)
			body := fmt.Sprintf("Hi %s, invoice %s: Rs.%s %s on %s. Please arrange payment at your earliest convenience.",
				inv.customerName, inv.orderNumber, inv.outstanding, word, inv.dueDate)
			notify.DispatchEmail(ctx, tx, "payment_reminder", inv.email, subject, body, "sales_order", inv.id)
			notify.DispatchPhone(ctx, tx, "payment_reminder", inv.phone, body, "sales_order", inv.id)
			if _, err := tx.Exec(ctx, `UPDATE sales_orders SET last_reminder_sent_at = now() WHERE id = $1`, inv.id); err != nil {
				return err
			}
		}
		return nil
	})
}
