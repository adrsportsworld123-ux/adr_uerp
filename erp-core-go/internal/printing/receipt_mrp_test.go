package printing

import (
	"strings"
	"testing"
)

func receiptText(d ReceiptData) string {
	var b strings.Builder
	for _, c := range Decode(BuildReceipt(d)) {
		b.WriteString(c.Text)
	}
	return b.String()
}

// The MRP lines and the savings print only when the branch turns them on;
// the customer's name and mobile print whenever the sale has a customer.
func TestReceiptMRPDiscountAndMobile(t *testing.T) {
	d := ReceiptData{
		MerchantName: "Acme Sports", BranchName: "MG Road", OrderNumber: "SO-1", CashierName: "Ravi",
		CustomerName: "Kumar", CustomerPhone: "9876543210",
		Lines: []ReceiptLine{
			{ProductName: "Bat", Quantity: "1", UnitPrice: "899.00", LineTotal: "849.00", MRP: "999.00", MRPDiscount: "100.00"},
			{ProductName: "Ball", Quantity: "2", UnitPrice: "150.00", LineTotal: "300.00", MRP: "150.00", MRPDiscount: "0.00"},
		},
		Subtotal: "1199.00", DiscountTotal: "50.00", TaxTotal: "0.00", GrandTotal: "1149.00",
		MRPDiscountTotal: "100.00", TotalSavings: "150.00",
	}

	off := receiptText(d)
	for _, want := range []string{"Customer: Kumar", "Mobile: 9876543210"} {
		if !strings.Contains(off, want) {
			t.Errorf("missing %q:\n%s", want, off)
		}
	}
	for _, not := range []string{"MRP", "You saved"} {
		if strings.Contains(off, not) {
			t.Errorf("setting off, but printed %q:\n%s", not, off)
		}
	}

	d.ShowMRPDiscount = true
	on := receiptText(d)
	for _, want := range []string{"MRP 999.00, save 100.00", "MRP 150.00", "Discount on MRP: 100.00", "You saved: 150.00"} {
		if !strings.Contains(on, want) {
			t.Errorf("missing %q:\n%s", want, on)
		}
	}
	if strings.Contains(on, "MRP 150.00, save") {
		t.Errorf("a line sold at MRP shows no saving:\n%s", on)
	}

	// Nothing saved: no savings lines even with the setting on.
	d.MRPDiscountTotal, d.TotalSavings = "0.00", "0.00"
	if s := receiptText(d); strings.Contains(s, "You saved") {
		t.Errorf("nothing saved, but printed savings:\n%s", s)
	}
}
