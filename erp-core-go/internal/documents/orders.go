package documents

import (
	"strings"

	"erp-core-go/internal/taxes"
)

// The two "before the transaction" documents: a purchase order to a
// supplier and a B2B quotation to a customer. Neither is a tax document,
// so neither is ever titled as one.

// POLine is one purchase_order_lines row.
type POLine struct {
	Name, SKU, Quantity, Received, UnitCost, Total string
}

type POInput struct {
	Number, Status, Date, ExpectedDate, CreatedBy string
	Supplier                                      Supplier
	Lines                                         []POLine
	Subtotal                                      string
	Notes, CloseReason                            string
}

// PurchaseOrder: anything not yet issued or no longer live carries its
// status as a stamp, so a draft or cancelled PO can't be mistaken for
// an order the supplier should fill.
func PurchaseOrder(in POInput) Document {
	d := Document{
		Title:  "PURCHASE ORDER",
		Number: in.Number,
		Date:   in.Date,
		Party:  in.Supplier.party(),
		Columns: []Column{
			{Title: "#", Width: 5},
			{Title: "Item", Width: 47},
			{Title: "Ordered", Width: 11, Right: true},
			{Title: "Received", Width: 11, Right: true},
			{Title: "Unit cost", Width: 12, Right: true},
			{Title: "Amount", Width: 14, Right: true},
		},
	}
	if in.ExpectedDate != "" {
		d.Meta = append(d.Meta, [2]string{"Expected delivery", in.ExpectedDate})
	}
	if in.CreatedBy != "" {
		d.Meta = append(d.Meta, [2]string{"Raised by", in.CreatedBy})
	}
	d.Meta = append(d.Meta, [2]string{"Status", Label(in.Status)})
	switch in.Status {
	case "draft", "cancelled", "closed":
		d.Stamp = strings.ToUpper(Label(in.Status))
	}
	for i, l := range in.Lines {
		name := l.Name
		if l.SKU != "" {
			name += "\n" + l.SKU
		}
		d.Rows = append(d.Rows, Row{Cells: []string{itoa(i + 1), name, Qty(l.Quantity), Qty(l.Received), Amount(l.UnitCost), Amount(l.Total)}})
	}
	d.Totals = []Total{{Label: "Total (before GST)", Value: INR(in.Subtotal), Bold: true}}
	d.Notes = Lines(in.Notes)
	if in.CloseReason != "" {
		d.Notes = append(d.Notes, Label(in.Status)+": "+in.CloseReason)
	}
	d.Notes = append(d.Notes, "Please quote this PO number on your invoice and delivery challan.")
	return d
}

// QuotationLine is one quotation_lines row.
type QuotationLine struct {
	Name, SKU, Quantity, UnitPrice, Tax, Total string
	LineTax
}

// Customer is the counterparty block of a quotation.
type Customer struct {
	Name, CompanyName, GSTIN, Address, Phone, Email string
}

func (c Customer) party() *Party {
	name := c.CompanyName
	var lines []string
	if name == "" {
		name = c.Name
	} else if c.Name != "" && !strings.EqualFold(c.Name, name) {
		lines = append(lines, "Attn: "+c.Name)
	}
	if name == "" {
		name = c.Phone
	}
	lines = append(lines, Lines(c.Address)...)
	if c.GSTIN != "" {
		lines = append(lines, "GSTIN: "+c.GSTIN)
	}
	var contact []string
	if c.Phone != "" {
		contact = append(contact, "Ph: "+c.Phone)
	}
	if c.Email != "" {
		contact = append(contact, c.Email)
	}
	if len(contact) > 0 {
		lines = append(lines, strings.Join(contact, "  ·  "))
	}
	return &Party{Label: "Quotation for", Name: name, Lines: lines}
}

type QuotationInput struct {
	Number, Status, Date, ValidUntil, PreparedBy string
	Customer                                     Customer
	Lines                                        []QuotationLine
	Subtotal, Tax, Grand                         string
	Notes                                        string
	Breakup                                      *taxes.Breakup // nil prints one "GST" column
	HideSKU                                      bool           // the branch prints product names only (migrations/062)
}

// Quotation: a draft prints with a DRAFT stamp (it may still change), a
// rejected or expired quote with its status, so neither passes for a live
// offer. Sent, accepted and converted quotes print clean.
func Quotation(in QuotationInput) Document {
	d := Document{
		Title:  "QUOTATION",
		Number: in.Number,
		Date:   in.Date,
		Party:  in.Customer.party(),
		Meta:   [][2]string{{"Valid until", in.ValidUntil}},
	}
	d.Meta = append(d.Meta, supplyMeta(in.Breakup)...)
	if in.Breakup == nil {
		d.Columns = []Column{
			{Title: "#", Width: 5},
			{Title: "Item", Width: 44},
			{Title: "Qty", Width: 9, Right: true},
			{Title: "Rate", Width: 13, Right: true},
			{Title: "GST", Width: 12, Right: true},
			{Title: "Amount", Width: 14, Right: true},
		}
	} else {
		d.Columns = append([]Column{{Title: "#", Width: 5}, {Title: "Item", Width: 44}}, itemColumns(in.Breakup, false)...)
	}
	if in.PreparedBy != "" {
		d.Meta = append(d.Meta, [2]string{"Prepared by", in.PreparedBy})
	}
	switch in.Status {
	case "draft", "rejected", "expired":
		d.Stamp = strings.ToUpper(Label(in.Status))
	}
	for i, l := range in.Lines {
		sku := l.SKU
		if in.HideSKU {
			sku = ""
		}
		cells := []string{itoa(i + 1), itemName(l.Name, sku, l.HSN, in.Breakup)}
		if in.Breakup == nil {
			cells = append(cells, Qty(l.Quantity), Amount(l.UnitPrice), Amount(l.Tax), Amount(l.Total))
		} else {
			cells = append(cells, itemCells(in.Breakup, false, l.Quantity, l.UnitPrice, "", l.Tax, l.Total, l.LineTax)...)
		}
		d.Rows = append(d.Rows, Row{Cells: cells})
	}
	d.Totals = []Total{{Label: "Subtotal", Value: INR(in.Subtotal)}}
	d.Totals = append(d.Totals, taxTotals(in.Breakup, "GST", in.Tax)...)
	d.Totals = append(d.Totals, Total{Label: "Total", Value: INR(in.Grand), Bold: true})
	d.TaxTable = taxSummary(in.Breakup)
	d.Notes = Lines(in.Notes)
	d.Notes = append(d.Notes, "This is a quotation, not a tax invoice. Prices and availability are valid until "+in.ValidUntil+".")
	return d
}
