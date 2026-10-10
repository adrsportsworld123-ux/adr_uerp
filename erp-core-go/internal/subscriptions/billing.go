package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// Provider takes a payment for an invoice. A real payment gateway is a
// separate vendor decision (phased_roadmap.md Phase 16: "interface plus
// stub first", like the GSP); every billing path goes through this.
type Provider interface {
	Name() string
	// Charge collects inv's amount and returns the provider's reference.
	Charge(ctx context.Context, inv Invoice) (string, error)
}

// StubProvider "collects" every payment at once, with no network call, and
// returns a recognisable reference.
type StubProvider struct{}

func (StubProvider) Name() string { return "stub" }

func (StubProvider) Charge(_ context.Context, inv Invoice) (string, error) {
	return "stub_" + inv.InvoiceNumber + "_" + strconv.FormatInt(time.Now().Unix(), 10), nil
}

type Invoice struct {
	ID            string     `json:"invoice_id"`
	InvoiceNumber string     `json:"invoice_number"`
	Kind          string     `json:"kind"`
	PlanCode      string     `json:"plan_code"`
	PeriodStart   time.Time  `json:"period_start"`
	PeriodEnd     time.Time  `json:"period_end"`
	POSCount      int        `json:"pos_count"`
	UnitPrice     string     `json:"unit_price"`
	Amount        string     `json:"amount"`
	Status        string     `json:"status"`
	DueAt         time.Time  `json:"due_at"`
	PaidAt        *time.Time `json:"paid_at"`
	Provider      *string    `json:"provider"`
	ProviderRef   *string    `json:"provider_ref"`
	CreatedAt     time.Time  `json:"created_at"`
}

const invoiceColumns = `id, invoice_number, kind, plan_code, period_start, period_end, pos_count, unit_price::text, amount::text,
	status, due_at, paid_at, provider, provider_ref, created_at`

func scanInvoice(row pgx.Row) (Invoice, error) {
	var i Invoice
	err := row.Scan(&i.ID, &i.InvoiceNumber, &i.Kind, &i.PlanCode, &i.PeriodStart, &i.PeriodEnd, &i.POSCount, &i.UnitPrice, &i.Amount,
		&i.Status, &i.DueAt, &i.PaidAt, &i.Provider, &i.ProviderRef, &i.CreatedAt)
	return i, err
}

func ListInvoices(ctx context.Context, tx pgx.Tx, limit int) ([]Invoice, error) {
	rows, err := tx.Query(ctx, `SELECT `+invoiceColumns+` FROM billing_invoices ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invoice{}
	for rows.Next() {
		i, err := scanInvoice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func GetInvoice(ctx context.Context, tx pgx.Tx, id string) (Invoice, error) {
	return scanInvoice(tx.QueryRow(ctx, `SELECT `+invoiceColumns+` FROM billing_invoices WHERE id::text = $1`, id))
}

// billablePOS: a plan is priced per POS: active tills, at least one.
func billablePOS(ctx context.Context, tx pgx.Tx) (int, error) {
	n, err := Count(ctx, tx, LimitPOSTerminals, "")
	if n < 1 {
		n = 1
	}
	return int(n), err
}

// CreateInvoice adds a due invoice for one month of plan from start.
func CreateInvoice(ctx context.Context, tx pgx.Tx, kind string, plan Plan, start time.Time) (Invoice, error) {
	pos, err := billablePOS(ctx, tx)
	if err != nil {
		return Invoice{}, err
	}
	var seq int
	if err := tx.QueryRow(ctx, `SELECT count(*) + 1 FROM billing_invoices`).Scan(&seq); err != nil {
		return Invoice{}, err
	}
	number := fmt.Sprintf("SUB-%s-%04d", start.In(IST).Format("200601"), seq)
	return scanInvoice(tx.QueryRow(ctx, `
		INSERT INTO billing_invoices (merchant_id, invoice_number, kind, plan_code, period_start, period_end, pos_count, unit_price, amount, due_at)
		VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6::int, $7::numeric, $7::numeric * $6::int, $8)
		RETURNING `+invoiceColumns, number, kind, plan.Code, start, start.AddDate(0, 1, 0), pos, plan.PricePerPOSMonth, time.Now()))
}

var (
	ErrSamePlan       = errors.New("already on this plan")
	ErrPlanInactive   = errors.New("that plan isn't offered")
	ErrInvoiceNotDue  = errors.New("the invoice isn't due")
	ErrNothingToRenew = errors.New("nothing to renew")
)

// ChangeResult says what a plan change did.
type ChangeResult struct {
	Outcome string   `json:"outcome"` // "switched" | "trial_started" | "invoice_due"
	Invoice *Invoice `json:"invoice,omitempty"`
}

// ChangePlan, from the merchant's own subscription page:
//   - to a free plan: at once, no expiry, open plan-change invoices voided;
//   - to a paid plan, first time on a paid plan: that plan's free trial;
//   - otherwise: a plan-change invoice; the plan switches when it's paid.
//
// Nothing is deactivated by a downgrade (see Limit).
func ChangePlan(ctx context.Context, tx pgx.Tx, e Entitlement, target Plan, userID string) (ChangeResult, error) {
	if !target.Active {
		return ChangeResult{}, ErrPlanInactive
	}
	if target.Code == e.Plan.Code && !e.ReadOnly && e.Status != StatusGrace {
		return ChangeResult{}, ErrSamePlan
	}
	if _, err := tx.Exec(ctx, `UPDATE billing_invoices SET status = 'void' WHERE status = 'due' AND kind = 'plan_change'`); err != nil {
		return ChangeResult{}, err
	}
	if target.Free() {
		_, err := tx.Exec(ctx, `
			INSERT INTO subscriptions (merchant_id, plan_code, status, updated_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1, 'active', $2)
			ON CONFLICT (merchant_id) DO UPDATE SET plan_code = $1, status = 'active', trial_ends_at = NULL,
				current_period_end = NULL, grace_ends_at = NULL, read_only_since = NULL, updated_by = $2, updated_at = now()`,
			target.Code, userID)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE billing_invoices SET status = 'void' WHERE status = 'due'`)
		}
		return ChangeResult{Outcome: "switched"}, syncTier(ctx, tx, target.Code, err)
	}
	if !e.Stored.TrialUsed && e.Plan.Free() && !e.ReadOnly && target.TrialDays > 0 {
		_, err := tx.Exec(ctx, `
			INSERT INTO subscriptions (merchant_id, plan_code, status, trial_ends_at, trial_used, updated_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1, 'trial', now() + make_interval(days => $2), true, $3)
			ON CONFLICT (merchant_id) DO UPDATE SET plan_code = $1, status = 'trial', trial_ends_at = now() + make_interval(days => $2),
				trial_used = true, current_period_end = NULL, grace_ends_at = NULL, read_only_since = NULL, updated_by = $3, updated_at = now()`,
			target.Code, target.TrialDays, userID)
		return ChangeResult{Outcome: "trial_started"}, syncTier(ctx, tx, target.Code, err)
	}
	inv, err := CreateInvoice(ctx, tx, "plan_change", target, time.Now())
	if err != nil {
		return ChangeResult{}, err
	}
	return ChangeResult{Outcome: "invoice_due", Invoice: &inv}, nil
}

// syncTier keeps merchants.subscription_tier (Phase 0's column, read by
// older code paths) in step with the subscription.
func syncTier(ctx context.Context, tx pgx.Tx, code string, err error) error {
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE merchants SET subscription_tier = $1, updated_at = now() WHERE id = current_setting('app.tenant_id')::uuid
		AND $1 IN ('freemium','starter','professional','enterprise')`, code)
	return err
}

// MarkPaid records a payment and applies it: a plan change switches the
// plan for a month from now; a renewal extends the period by a month from
// where it ended (or from now, once expired). Paid invoices replay as-is.
func MarkPaid(ctx context.Context, tx pgx.Tx, e Entitlement, inv Invoice, provider, ref, paidBy string) (Invoice, error) {
	if inv.Status == "paid" {
		return inv, nil
	}
	if inv.Status != "due" {
		return Invoice{}, ErrInvoiceNotDue
	}
	paid, err := scanInvoice(tx.QueryRow(ctx, `
		UPDATE billing_invoices SET status = 'paid', paid_at = now(), provider = $2, provider_ref = $3, paid_by = NULLIF($4, '')::uuid
		WHERE id = $1 AND status = 'due' RETURNING `+invoiceColumns, inv.ID, provider, ref, paidBy))
	if err != nil {
		return Invoice{}, err
	}
	// A renewal paid before the account went read-only runs to the end of
	// the invoice's own period (no gap, no overlap); anything else runs a
	// month from now.
	end := time.Now().AddDate(0, 1, 0)
	if inv.Kind == "renewal" && !e.ReadOnly {
		end = inv.PeriodEnd
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO subscriptions (merchant_id, plan_code, status, current_period_end, updated_by)
		VALUES (current_setting('app.tenant_id')::uuid, $1, 'active', $2, NULLIF($3, '')::uuid)
		ON CONFLICT (merchant_id) DO UPDATE SET plan_code = $1, status = 'active', current_period_end = $2, trial_ends_at = NULL,
			grace_ends_at = NULL, read_only_since = NULL, updated_by = NULLIF($3, '')::uuid, updated_at = now()`,
		inv.PlanCode, end, paidBy); err != nil {
		return Invoice{}, err
	}
	return paid, syncTier(ctx, tx, inv.PlanCode, nil)
}
