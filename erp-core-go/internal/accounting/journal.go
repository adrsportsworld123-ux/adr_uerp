// Package accounting implements Phase 2's Ledger & Accounting sub-area
// (phased_roadmap.md; pos_frd_complete.md §8): a chart of accounts,
// double-entry journaling, party ledgers, and Day Book/Cash Book. Other
// packages (sales, purchase, inventory) call PostJournalEntry as part of
// their own WithTenant transactions, so a sale/bill/adjustment and its
// journal entry commit or roll back together — never one without the
// other.
package accounting

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/db"
)

type Handler struct {
	DB *db.DB
}

// JournalLine is one side of a double-entry posting. Exactly one of
// Debit/Credit should be non-zero — PostJournalEntry doesn't enforce that
// per-line (the DB CHECK constraint does), but does enforce that the whole
// entry's debits and credits balance before writing anything.
type JournalLine struct {
	AccountCode string
	Debit       float64
	Credit      float64
	PartyType   string // "" | "customer" | "supplier"
	PartyID     string // "" if this line isn't tied to a specific party
}

// Account codes are stable identifiers looked up from chart_of_accounts by
// code, not hardcoded UUIDs — migrations/007_accounting.sql seeds these
// exact codes for the one demo merchant; a real merchant-onboarding flow
// needs to seed the same set for every new merchant.
const (
	AccountCash          = "1001"
	AccountBank          = "1002"
	AccountCardClearing  = "1003"
	AccountUPIClearing   = "1004"
	AccountGSTInput      = "1005"
	AccountReceivable    = "1100"
	AccountInventory     = "1200"
	AccountGSTPayable    = "2001"
	AccountPayable       = "2002"
	AccountSalesRevenue  = "4001"
	AccountInventoryLoss = "5001"
)

// AccountCodeForPaymentMethod maps a sales/bill payment method to the
// clearing/cash account it lands in. Falls back to Cash for any method not
// listed — documented, not silent: a genuinely new payment method should
// get its own clearing account in migrations/007_accounting.sql and a case
// here, not just fall through unnoticed.
func AccountCodeForPaymentMethod(method string) string {
	switch method {
	case "cash":
		return AccountCash
	case "card":
		return AccountCardClearing
	case "upi":
		return AccountUPIClearing
	case "bank_transfer", "cheque":
		return AccountBank
	default:
		return AccountCash
	}
}

// PostJournalEntry writes one balanced double-entry journal entry and its
// lines inside the caller's transaction. sourceType/sourceID identify what
// caused this posting (e.g. "sale"/sales_orders.id) for traceability —
// journal_entries.source_type has a CHECK constraint listing the valid
// values (migrations/007_accounting.sql).
func PostJournalEntry(ctx context.Context, tx pgx.Tx, branchID, sourceType, sourceID, description, performedBy string, lines []JournalLine) (entryNumber string, err error) {
	var totalDebit, totalCredit float64
	for _, l := range lines {
		totalDebit += l.Debit
		totalCredit += l.Credit
	}
	if math.Abs(totalDebit-totalCredit) > 0.01 {
		return "", fmt.Errorf("accounting: unbalanced journal entry (debit=%.2f credit=%.2f) for %s %s", totalDebit, totalCredit, sourceType, sourceID)
	}

	entryNumber = fmt.Sprintf("JE-%d", time.Now().UnixNano())
	var branchArg, sourceArg, performedByArg *string
	if branchID != "" {
		branchArg = &branchID
	}
	if sourceID != "" {
		sourceArg = &sourceID
	}
	if performedBy != "" {
		performedByArg = &performedBy
	}

	var entryID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO journal_entries (id, merchant_id, branch_id, entry_number, source_type, source_id, description, created_by)
		VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6)
		RETURNING id`,
		branchArg, entryNumber, sourceType, sourceArg, description, performedByArg,
	).Scan(&entryID); err != nil {
		return "", fmt.Errorf("accounting: insert journal entry: %w", err)
	}

	for _, l := range lines {
		var accountID string
		if err := tx.QueryRow(ctx, `SELECT id FROM chart_of_accounts WHERE code = $1`, l.AccountCode).Scan(&accountID); err != nil {
			return "", fmt.Errorf("accounting: look up account %s: %w", l.AccountCode, err)
		}
		var partyType, partyID *string
		if l.PartyType != "" {
			partyType = &l.PartyType
		}
		if l.PartyID != "" {
			partyID = &l.PartyID
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO journal_lines (id, journal_entry_id, account_id, debit, credit, party_type, party_id)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6)`,
			entryID, accountID, l.Debit, l.Credit, partyType, partyID); err != nil {
			return "", fmt.Errorf("accounting: insert journal line for %s: %w", l.AccountCode, err)
		}
	}
	return entryNumber, nil
}
