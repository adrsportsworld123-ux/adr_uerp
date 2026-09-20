package sales

import (
	"context"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
)

// postSaleJournal books the standard sale entry: Dr each payment method's
// clearing/cash account (debitLines, built by the caller from however it
// represents payments), Cr Sales Revenue for the discounted subtotal, Cr
// GST Payable for tax. Zero-amount Cr lines are omitted rather than posted
// as no-op lines — a zero-tax sale (no applicable GST) shouldn't leave a
// ₹0.00 line cluttering the ledger.
func postSaleJournal(ctx context.Context, tx pgx.Tx, branchID, orderID, performedBy string, debitLines []accounting.JournalLine, subtotal, discountTotal, taxTotal float64) error {
	lines := append([]accounting.JournalLine{}, debitLines...)
	if netRevenue := subtotal - discountTotal; netRevenue > 0 {
		lines = append(lines, accounting.JournalLine{AccountCode: accounting.AccountSalesRevenue, Credit: netRevenue})
	}
	if taxTotal > 0 {
		lines = append(lines, accounting.JournalLine{AccountCode: accounting.AccountGSTPayable, Credit: taxTotal})
	}
	_, err := accounting.PostJournalEntry(ctx, tx, branchID, "sale", orderID, "POS sale", performedBy, lines)
	return err
}

// postSaleVoidJournal reverses a previously-posted sale entry — same
// amounts, debits and credits swapped, so the two entries net to zero
// rather than deleting the original (an immutable audit trail, per FRD
// §3's "Bill Modification... Audit Trail: All changes logged").
func postSaleVoidJournal(ctx context.Context, tx pgx.Tx, branchID, orderID, performedBy string, creditLines []accounting.JournalLine, subtotal, discountTotal, taxTotal float64) error {
	lines := append([]accounting.JournalLine{}, creditLines...)
	if netRevenue := subtotal - discountTotal; netRevenue > 0 {
		lines = append(lines, accounting.JournalLine{AccountCode: accounting.AccountSalesRevenue, Debit: netRevenue})
	}
	if taxTotal > 0 {
		lines = append(lines, accounting.JournalLine{AccountCode: accounting.AccountGSTPayable, Debit: taxTotal})
	}
	_, err := accounting.PostJournalEntry(ctx, tx, branchID, "sale_void", orderID, "Sale void reversal", performedBy, lines)
	return err
}
