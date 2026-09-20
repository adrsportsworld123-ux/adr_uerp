package printing

import "fmt"

type ReceiptLine struct {
	ProductName string
	SKU         string
	Quantity    string
	UnitPrice   string
	LineTotal   string
}

type ReceiptPayment struct {
	Method string
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
	Payments      []ReceiptPayment
}

const ruleLine = "--------------------------------"

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
		Line(d.BranchName).
		Line("Order " + d.OrderNumber).
		Line("Cashier: " + d.CashierName).
		AlignLeft().
		Line(ruleLine)

	for _, l := range d.Lines {
		b.Line(fmt.Sprintf("%s (%s)", l.ProductName, l.SKU))
		b.Line(fmt.Sprintf("  %s x %s = %s", l.Quantity, l.UnitPrice, l.LineTotal))
	}

	b.Line(ruleLine)
	b.Line("Subtotal: " + d.Subtotal)
	if d.DiscountTotal != "" && d.DiscountTotal != "0.00" {
		b.Line("Discount: -" + d.DiscountTotal)
	}
	b.Line("Tax: " + d.TaxTotal)
	b.Bold(true).DoubleSize(true).Line("Total: " + d.GrandTotal).DoubleSize(false).Bold(false)
	b.Line(ruleLine)

	for _, p := range d.Payments {
		b.Line(fmt.Sprintf("%s: %s", p.Method, p.Amount))
	}

	b.FeedLines(1).AlignCenter().Line("Thank you, visit again!").FeedLines(3).Cut()
	return b.Bytes()
}
