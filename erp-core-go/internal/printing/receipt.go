package printing

import (
	"fmt"
	"strings"
)

type ReceiptLine struct {
	ProductName string
	SKU         string
	Quantity    string
	UnitPrice   string
	LineTotal   string
	// MRP per unit and the line's discount on it (migrations/081); printed
	// only when ReceiptData.ShowMRPDiscount. "" = none.
	MRP         string
	MRPDiscount string
}

type ReceiptPayment struct {
	Method string
	Amount string
}

// ReceiptTax is one printed tax line ("CGST @ 2.5%: 0.61").
type ReceiptTax struct {
	Label  string
	Amount string
}

type ReceiptData struct {
	MerchantName  string
	BranchName    string
	OrderNumber   string
	CashierName   string
	Lines         []ReceiptLine
	Subtotal      string
	DiscountTotal string
	TaxTotal      string
	GrandTotal    string
	// RoundOff: the checkout waiver, signed ("-0.75"); "" or "0.00" prints
	// nothing. PayableTotal is the total after it.
	RoundOff     string
	PayableTotal string
	Payments     []ReceiptPayment
	// Taxes replaces the single "Tax:" line with the split the branch
	// prints (CGST / SGST, IGST, or its own label; migrations/060); nil
	// keeps "Tax:". TaxSummary adds one line per rate when a bill mixes
	// rates. PlaceOfSupply ("33 – Tamil Nadu") prints under the cashier.
	Taxes         []ReceiptTax
	TaxSummary    []string
	PlaceOfSupply string
	// HideSKU prints each item's name only (branch_print_settings.show_sku,
	// migrations/062); a SKU that repeats the name is never printed.
	HideSKU bool
	// The branch's letterhead (branch_print_settings, internal/documents):
	// lines under the branch name, and the closing lines. Footer nil keeps
	// the original "Thank you, visit again!"; an empty, non-nil Footer
	// prints none.
	Header []string
	Footer []string
	// The customer, when the sale has one: name and mobile print under
	// the cashier.
	CustomerName  string
	CustomerPhone string
	// ShowMRPDiscount (branch_print_settings.show_mrp_discount,
	// migrations/081): each item's MRP, then "Discount on MRP" and "You
	// saved" under the total. false = the slip as before.
	ShowMRPDiscount  bool
	MRPDiscountTotal string
	TotalSavings     string
	// PreviousDue: the customer's earlier outstanding kept on this bill
	// (migrations/087); nil prints the slip as before.
	PreviousDue *ReceiptPreviousDue
}

// ReceiptPreviousDue: printed after the payments — the outstanding before,
// what was collected and against which sales, the total received, and
// what is still owed.
type ReceiptPreviousDue struct {
	Outstanding   string
	Collected     string
	Against       []ReceiptPayment // Method: the sale paid ("S-0012 (cash)")
	TotalReceived string
	BalanceAfter  string
}

const ruleLine = "--------------------------------"

// nonZero: an amount string that is set and not zero ("", "0", "0.00" are).
func nonZero(s string) bool {
	return strings.Trim(strings.TrimSpace(s), "0.") != ""
}

// BuildReceipt renders a finalized sale as an ESC/POS print job for a
// standard thermal receipt printer — the "printer integration" half of
// Phase 1's Barcode & Label Generation item. Consumes the same data
// GET /sales/orders/{id}/receipt already assembles (see
// internal/sales/receipt.go's loadReceiptData) rather than re-querying
// anything itself, so the JSON and printed receipt can never drift apart.
func BuildReceipt(d ReceiptData) []byte {
	b := New().
		AlignCenter().
		Bold(true).DoubleSize(true).Line(d.MerchantName).DoubleSize(false).Bold(false).
		Line(d.BranchName)
	for _, l := range d.Header {
		b.Line(l)
	}
	b.Line("Order " + d.OrderNumber).
		Line("Cashier: " + d.CashierName)
	if d.PlaceOfSupply != "" {
		b.Line("Place of supply: " + d.PlaceOfSupply)
	}
	if d.CustomerName != "" {
		b.Line("Customer: " + d.CustomerName)
	}
	if d.CustomerPhone != "" {
		b.Line("Mobile: " + d.CustomerPhone)
	}
	b.AlignLeft().
		Line(ruleLine)

	for _, l := range d.Lines {
		if d.HideSKU || l.SKU == "" || strings.EqualFold(strings.TrimSpace(l.SKU), strings.TrimSpace(l.ProductName)) {
			b.Line(l.ProductName)
		} else {
			b.Line(fmt.Sprintf("%s (%s)", l.ProductName, l.SKU))
		}
		b.Line(fmt.Sprintf("  %s x %s = %s", l.Quantity, l.UnitPrice, l.LineTotal))
		if d.ShowMRPDiscount && l.MRP != "" {
			if nonZero(l.MRPDiscount) {
				b.Line(fmt.Sprintf("  MRP %s, save %s", l.MRP, l.MRPDiscount))
			} else {
				b.Line("  MRP " + l.MRP)
			}
		}
	}

	b.Line(ruleLine)
	b.Line("Subtotal: " + d.Subtotal)
	if d.DiscountTotal != "" && d.DiscountTotal != "0.00" {
		b.Line("Discount: -" + d.DiscountTotal)
	}
	if len(d.Taxes) == 0 {
		b.Line("Tax: " + d.TaxTotal)
	}
	for _, t := range d.Taxes {
		b.Line(t.Label + ": " + t.Amount)
	}
	for _, l := range d.TaxSummary {
		b.Line("  " + l)
	}
	if d.RoundOff != "" && d.RoundOff != "0.00" {
		b.Line("Total: " + d.GrandTotal)
		b.Line("Round off: " + d.RoundOff)
		b.Bold(true).DoubleSize(true).Line("Net payable: " + d.PayableTotal).DoubleSize(false).Bold(false)
	} else {
		b.Bold(true).DoubleSize(true).Line("Total: " + d.GrandTotal).DoubleSize(false).Bold(false)
	}
	if d.ShowMRPDiscount && nonZero(d.TotalSavings) {
		if nonZero(d.MRPDiscountTotal) {
			b.Line("Discount on MRP: " + d.MRPDiscountTotal)
		}
		b.Bold(true).Line("You saved: " + d.TotalSavings).Bold(false)
	}
	b.Line(ruleLine)

	for _, p := range d.Payments {
		b.Line(fmt.Sprintf("%s: %s", p.Method, p.Amount))
	}
	if pd := d.PreviousDue; pd != nil {
		b.Line(ruleLine)
		b.Line("Previous due: " + pd.Outstanding)
		if nonZero(pd.Collected) {
			b.Line("Collected now: " + pd.Collected)
			for _, a := range pd.Against {
				b.Line(fmt.Sprintf("  %s: %s", a.Method, a.Amount))
			}
			b.Bold(true).Line("Total received: " + pd.TotalReceived).Bold(false)
		}
		b.Bold(true).Line("Balance due: " + pd.BalanceAfter).Bold(false)
	}

	footer := d.Footer
	if footer == nil {
		footer = []string{"Thank you, visit again!"}
	}
	b.FeedLines(1).AlignCenter()
	for _, l := range footer {
		b.Line(l)
	}
	b.FeedLines(3).Cut()
	return b.Bytes()
}
