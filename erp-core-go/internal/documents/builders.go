package documents

import (
	"strings"

	"erp-core-go/internal/taxes"
)

// The builders below turn each document type's plain data into a
// Document, so the sales and purchase packages only map their own loader
// output into these inputs and the layout decisions live in one place.

// SaleLine is one sales_order_lines row, money and quantities as the
// API's NUMERIC strings.
type SaleLine struct {
	Name, SKU, Quantity, UnitPrice, Discount, Tax, Total string
	LineTax                                              // GST split columns (used when Breakup splits)
}

type SaleInput struct {
	OrderNumber, Status, Date, Cashier string
	CustomerName, CustomerPhone        string
	CustomerGSTIN                      string
	Lines                              []SaleLine
	Breakup                            *taxes.Breakup // the sale's tax split; nil prints one "Tax" column
	Subtotal, Discount, Tax, Grand     string
	RoundOff, Payable                  string      // checkout waiver (signed) and the total after it; "" / "0.00" = none
	Payments                           [][2]string // method code, amount
}

// SaleReceipt: a finalized sale is a "TAX INVOICE" when the branch has a
// GSTIN on the letterhead, a plain "RECEIPT" otherwise; anything not
// finalized carries its status as a stamp so a voided sale's PDF can't
// pass for a valid one.
func SaleReceipt(lh Letterhead, in SaleInput) Document {
	title := "RECEIPT"
	if lh.GSTIN != "" {
		title = "TAX INVOICE"
	}
	d := Document{
		Title:  title,
		Number: in.OrderNumber,
		Date:   in.Date,
		Meta:   [][2]string{{"Cashier", in.Cashier}},
	}
	d.Meta = append(d.Meta, supplyMeta(in.Breakup)...)
	d.Columns = append([]Column{{Title: "#", Width: 5}, {Title: "Item", Width: 40}}, itemColumns(in.Breakup, true)...)
	if in.Status != "finalized" {
		d.Stamp = strings.ToUpper(Label(in.Status))
	}
	if in.CustomerName != "" {
		p := &Party{Label: "Bill to", Name: in.CustomerName}
		if in.CustomerGSTIN != "" {
			p.Lines = append(p.Lines, "GSTIN: "+in.CustomerGSTIN)
		}
		if in.CustomerPhone != "" {
			p.Lines = append(p.Lines, "Ph: "+in.CustomerPhone)
		}
		d.Party = p
	}
	for i, l := range in.Lines {
		detail := Qty(l.Quantity) + " × " + Amount(l.UnitPrice)
		if isNonZero(l.Discount) {
			detail += "  (−" + Amount(l.Discount) + ")"
		}
		cells := append([]string{itoa(i + 1), itemName(l.Name, l.SKU, l.HSN, in.Breakup)},
			itemCells(in.Breakup, true, l.Quantity, l.UnitPrice, l.Discount, l.Tax, l.Total, l.LineTax)...)
		d.Rows = append(d.Rows, Row{
			Cells:  cells,
			Title:  l.Name,
			Detail: detail,
			Amount: Amount(l.Total),
		})
	}
	d.Totals = append(d.Totals, Total{Label: "Subtotal", Value: INR(in.Subtotal)})
	if isNonZero(in.Discount) {
		d.Totals = append(d.Totals, Total{Label: "Discount", Value: "−" + INR(in.Discount)})
	}
	d.Totals = append(d.Totals, taxTotals(in.Breakup, "Tax", in.Tax)...)
	d.TaxTable = taxSummary(in.Breakup)
	if isNonZero(in.RoundOff) {
		d.Totals = append(d.Totals,
			Total{Label: "Total", Value: INR(in.Grand)},
			Total{Label: "Round off", Value: INR(in.RoundOff)},
			Total{Label: "Net payable", Value: INR(in.Payable), Bold: true})
	} else {
		d.Totals = append(d.Totals, Total{Label: "Total", Value: INR(in.Grand), Bold: true})
	}
	for _, p := range in.Payments {
		d.Payments = append(d.Payments, [2]string{Label(p[0]), INR(p[1])})
	}
	return d
}

// PurchaseLine is one GRN / purchase-return line.
type PurchaseLine struct {
	Name, SKU, BatchNo, Expiry, Quantity, UnitCost, Total string
}

// Supplier is the counterparty block of every purchase document.
type Supplier struct {
	Name, LegalName, GSTIN, Phone, Email string
}

func (s Supplier) party() *Party {
	p := &Party{Label: "Supplier", Name: s.Name}
	if s.LegalName != "" && !strings.EqualFold(s.LegalName, s.Name) {
		p.Lines = append(p.Lines, s.LegalName)
	}
	if s.GSTIN != "" {
		p.Lines = append(p.Lines, "GSTIN: "+s.GSTIN)
	}
	var c []string
	if s.Phone != "" {
		c = append(c, "Ph: "+s.Phone)
	}
	if s.Email != "" {
		c = append(c, s.Email)
	}
	if len(c) > 0 {
		p.Lines = append(p.Lines, strings.Join(c, "  ·  "))
	}
	return p
}

func purchaseColumns() []Column {
	return []Column{
		{Title: "#", Width: 5},
		{Title: "Item", Width: 44},
		{Title: "Batch / Expiry", Width: 17},
		{Title: "Qty", Width: 9, Right: true},
		{Title: "Unit cost", Width: 12, Right: true},
		{Title: "Amount", Width: 14, Right: true},
	}
}

func purchaseRows(lines []PurchaseLine) []Row {
	rows := make([]Row, 0, len(lines))
	for i, l := range lines {
		name := l.Name
		if l.SKU != "" {
			name += "\n" + l.SKU
		}
		batch := l.BatchNo
		if l.Expiry != "" {
			if batch != "" {
				batch += "\n"
			}
			batch += "Exp " + l.Expiry
		}
		rows = append(rows, Row{Cells: []string{itoa(i + 1), name, batch, Qty(l.Quantity), Amount(l.UnitCost), Amount(l.Total)}})
	}
	return rows
}

type GRNInput struct {
	Number, Status, Date, ReceivedBy string
	Supplier                         Supplier
	Lines                            []PurchaseLine
	Subtotal, Freight, Other, Grand  string
	Notes                            string
	PONumber                         string // the purchase order received against; "" for a direct GRN
}

func GRN(in GRNInput) Document {
	d := Document{
		Title:   "GOODS RECEIPT NOTE",
		Number:  in.Number,
		Date:    in.Date,
		Party:   in.Supplier.party(),
		Columns: purchaseColumns(),
		Rows:    purchaseRows(in.Lines),
		Meta:    [][2]string{{"Status", Label(in.Status)}},
	}
	if in.PONumber != "" {
		d.Meta = append(d.Meta, [2]string{"Against PO", in.PONumber})
	}
	if in.ReceivedBy != "" {
		d.Meta = append(d.Meta, [2]string{"Received by", in.ReceivedBy})
	}
	if in.Status != "completed" {
		d.Stamp = strings.ToUpper(Label(in.Status))
	}
	d.Totals = []Total{{Label: "Subtotal", Value: INR(in.Subtotal)}}
	if isNonZero(in.Freight) {
		d.Totals = append(d.Totals, Total{Label: "Freight", Value: INR(in.Freight)})
	}
	if isNonZero(in.Other) {
		d.Totals = append(d.Totals, Total{Label: "Other charges", Value: INR(in.Other)})
	}
	d.Totals = append(d.Totals, Total{Label: "Total", Value: INR(in.Grand), Bold: true})
	d.Notes = Lines(in.Notes)
	return d
}

type BillInput struct {
	Number, SupplierInvoice, Status, BillDate, DueDate, GRNNumber string
	Supplier                                                      Supplier
	Lines                                                         []PurchaseLine
	Subtotal, Tax, Freight, Other, RoundOff, Grand, Paid, Balance string
	Payments                                                      [][2]string // "method · date · ref", amount
	PONumber                                                      string      // the GRN's purchase order, if any
	TaxRows                                                       [][2]string // the supplier's tax, split (CGST / SGST / IGST); nil = one "GST" line
}

func PurchaseBill(in BillInput) Document {
	d := Document{
		Title:   "PURCHASE BILL",
		Number:  in.Number,
		Date:    in.BillDate,
		Party:   in.Supplier.party(),
		Columns: purchaseColumns(),
		Rows:    purchaseRows(in.Lines),
	}
	if in.SupplierInvoice != "" {
		d.Meta = append(d.Meta, [2]string{"Supplier invoice", in.SupplierInvoice})
	}
	if in.PONumber != "" {
		d.Meta = append(d.Meta, [2]string{"PO", in.PONumber})
	}
	if in.GRNNumber != "" {
		d.Meta = append(d.Meta, [2]string{"GRN", in.GRNNumber})
	}
	if in.DueDate != "" {
		d.Meta = append(d.Meta, [2]string{"Due date", in.DueDate})
	}
	d.Meta = append(d.Meta, [2]string{"Status", Label(in.Status)})

	d.Totals = []Total{{Label: "Subtotal", Value: INR(in.Subtotal)}}
	if isNonZero(in.Freight) {
		d.Totals = append(d.Totals, Total{Label: "Freight", Value: INR(in.Freight)})
	}
	if isNonZero(in.Other) {
		d.Totals = append(d.Totals, Total{Label: "Other charges", Value: INR(in.Other)})
	}
	if len(in.TaxRows) == 0 {
		d.Totals = append(d.Totals, Total{Label: "GST", Value: INR(in.Tax)})
	}
	for _, t := range in.TaxRows {
		d.Totals = append(d.Totals, Total{Label: t[0], Value: INR(t[1])})
	}
	if isNonZero(in.RoundOff) {
		d.Totals = append(d.Totals, Total{Label: "Round off", Value: INR(in.RoundOff)})
	}
	d.Totals = append(d.Totals,
		Total{Label: "Total", Value: INR(in.Grand), Bold: true},
		Total{Label: "Paid", Value: INR(in.Paid)},
		Total{Label: "Balance due", Value: INR(in.Balance), Bold: isNonZero(in.Balance)})
	for _, p := range in.Payments {
		d.Payments = append(d.Payments, [2]string{p[0], INR(p[1])})
	}
	return d
}

type ReturnInput struct {
	Number, Status, Date, Reason, GRNNumber, CreatedBy string
	Supplier                                           Supplier
	Lines                                              []PurchaseLine
	Grand                                              string
}

func PurchaseReturn(in ReturnInput) Document {
	d := Document{
		Title:   "PURCHASE RETURN",
		Number:  in.Number,
		Date:    in.Date,
		Party:   in.Supplier.party(),
		Columns: purchaseColumns(),
		Rows:    purchaseRows(in.Lines),
	}
	if in.GRNNumber != "" {
		d.Meta = append(d.Meta, [2]string{"Against GRN", in.GRNNumber})
	}
	if in.CreatedBy != "" {
		d.Meta = append(d.Meta, [2]string{"Raised by", in.CreatedBy})
	}
	if in.Status != "completed" {
		d.Stamp = strings.ToUpper(Label(in.Status))
	}
	d.Totals = []Total{{Label: "Return value", Value: INR(in.Grand), Bold: true}}
	if in.Reason != "" {
		d.Notes = []string{"Reason: " + in.Reason}
	}
	return d
}

// SampleDocument is what the print-settings preview renders.
func SampleDocument(lh Letterhead, kind Kind) Document {
	if kind == KindPurchase {
		return GRN(GRNInput{
			Number: "GRN-SAMPLE", Status: "completed", Date: "16 Oct 2026", ReceivedBy: "Store staff",
			Supplier: Supplier{Name: "Sample Supplier", GSTIN: "29ABCDE1234F1Z5", Phone: "98765 43210"},
			Lines: []PurchaseLine{
				{Name: "Cricket bat — English willow", SKU: "BAT-EW-01", Quantity: "10.000", UnitCost: "2450.00", Total: "24500.00"},
				{Name: "Leather ball (pack of 6)", SKU: "BALL-L6", BatchNo: "B2410", Expiry: "Oct 2028", Quantity: "5.000", UnitCost: "1180.00", Total: "5900.00"},
			},
			Subtotal: "30400.00", Freight: "350.00", Grand: "30750.00",
		})
	}
	return SaleReceipt(lh, SaleInput{
		OrderNumber: "SAMPLE-0001", Status: "finalized", Date: "16 Oct 2026, 11:42", Cashier: "Cashier",
		CustomerName: "Walk-in customer",
		Lines: []SaleLine{
			{Name: "Running shoes — size 9", SKU: "RUN-09", Quantity: "1.000", UnitPrice: "3499.00", Discount: "350.00", Tax: "377.88", Total: "3149.00"},
			{Name: "Sports socks (3 pairs)", SKU: "SOCK-3P", Quantity: "2.000", UnitPrice: "299.00", Discount: "0.00", Tax: "64.07", Total: "598.00"},
		},
		Subtotal: "4097.00", Discount: "350.00", Tax: "441.95", Grand: "3747.00",
		Payments: [][2]string{{"upi", "3747.00"}},
	})
}

func isNonZero(s string) bool {
	return strings.Trim(strings.TrimSpace(s), "-0.") != ""
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

// CreditNoteInput is a sales return as a GST credit note.
type CreditNoteInput struct {
	Number, Date, InvoiceNumber, InvoiceDate, Reason, RefundMethod, ProcessedBy string
	CustomerName, CustomerPhone, CustomerGSTIN                                  string
	Lines                                                                       []SaleLine // Total = the line's refund
	Taxable, Tax, RoundOff, Refund                                              string
	Breakup                                                                     *taxes.Breakup
}

// CreditNote: the goods given back with their taxable value and tax, split
// as the invoice charged it, against the invoice it reverses.
func CreditNote(lh Letterhead, in CreditNoteInput) Document {
	d := Document{
		Title:  "CREDIT NOTE",
		Number: in.Number,
		Date:   in.Date,
		Meta:   [][2]string{{"Against invoice", in.InvoiceNumber}, {"Invoice date", in.InvoiceDate}},
	}
	d.Meta = append(d.Meta, supplyMeta(in.Breakup)...)
	if in.ProcessedBy != "" {
		d.Meta = append(d.Meta, [2]string{"Processed by", in.ProcessedBy})
	}
	d.Columns = append([]Column{{Title: "#", Width: 5}, {Title: "Item", Width: 40}}, itemColumns(in.Breakup, false)...)
	if in.CustomerName != "" {
		p := &Party{Label: "Customer", Name: in.CustomerName}
		if in.CustomerGSTIN != "" {
			p.Lines = append(p.Lines, "GSTIN: "+in.CustomerGSTIN)
		}
		if in.CustomerPhone != "" {
			p.Lines = append(p.Lines, "Ph: "+in.CustomerPhone)
		}
		d.Party = p
	}
	for i, l := range in.Lines {
		cells := append([]string{itoa(i + 1), itemName(l.Name, l.SKU, l.HSN, in.Breakup)},
			itemCells(in.Breakup, false, l.Quantity, l.UnitPrice, "", l.Tax, l.Total, l.LineTax)...)
		d.Rows = append(d.Rows, Row{Cells: cells, Title: l.Name, Detail: Qty(l.Quantity) + " × " + Amount(l.UnitPrice), Amount: Amount(l.Total)})
	}
	d.Totals = append(d.Totals, Total{Label: "Taxable value", Value: INR(in.Taxable)})
	d.Totals = append(d.Totals, taxTotals(in.Breakup, "Tax", in.Tax)...)
	if isNonZero(in.RoundOff) {
		d.Totals = append(d.Totals, Total{Label: "Round off returned", Value: "−" + INR(in.RoundOff)})
	}
	d.Totals = append(d.Totals, Total{Label: "Refund", Value: INR(in.Refund), Bold: true})
	d.TaxTable = taxSummary(in.Breakup)
	if in.RefundMethod != "" {
		d.Payments = [][2]string{{"Refunded by " + Label(in.RefundMethod), INR(in.Refund)}}
	}
	if in.Reason != "" {
		d.Notes = []string{"Reason: " + in.Reason}
	}
	return d
}
