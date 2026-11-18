package printing

import (
	"strings"
	"testing"
)

func TestReceiptPreviousDue(t *testing.T) {
	base := ReceiptData{MerchantName: "Shop", OrderNumber: "S-0020", Subtotal: "1000.00", TaxTotal: "0.00", GrandTotal: "1000.00",
		Payments: []ReceiptPayment{{Method: "cash", Amount: "1000.00"}}}
	if strings.Contains(string(BuildReceipt(base)), "Previous due") {
		t.Fatal("a receipt without it prints it")
	}
	base.PreviousDue = &ReceiptPreviousDue{Outstanding: "800.00", Collected: "500.00", TotalReceived: "1500.00", BalanceAfter: "300.00",
		Against: []ReceiptPayment{{Method: "S-0012 (cash)", Amount: "500.00"}}}
	out := string(BuildReceipt(base))
	for _, want := range []string{"Previous due: 800.00", "Collected now: 500.00", "  S-0012 (cash): 500.00", "Total received: 1500.00", "Balance due: 300.00"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Kept on the bill, nothing collected: the outstanding and the balance only.
	base.PreviousDue = &ReceiptPreviousDue{Outstanding: "800.00", Collected: "0.00", TotalReceived: "1000.00", BalanceAfter: "800.00"}
	out = string(BuildReceipt(base))
	if strings.Contains(out, "Collected now") || !strings.Contains(out, "Balance due: 800.00") {
		t.Errorf("kept, not collected:\n%s", out)
	}
}
