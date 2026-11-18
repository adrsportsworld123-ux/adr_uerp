package purchase

// The invoice reader's AI path (§3.90): photos and scanned PDFs, which
// carry no text to read. The model transcribes the invoice into the same
// printed rows the PDF-text path finds (rawRow) — cell by cell, as
// printed, no rules applied — and the supplier's template then turns
// them into lines exactly as it does for a typed PDF (buildLines). So the
// template decides the names, sizes, cost and brand either way, and the
// model is only ever asked to copy what is on the page.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"erp-core-go/internal/ai/llm"
)

type attachment struct {
	mediaType string
	data      []byte
}

func aiMediaType(t string) bool {
	switch t {
	case "image/jpeg", "image/png", "image/webp", "application/pdf":
		return true
	}
	return false
}

const aiSystemPrompt = `You transcribe supplier purchase invoices (Indian GST tax invoices) for a retail shop's purchase entry.
Copy exactly what is printed. Never guess, total, convert or correct anything. Reply with JSON only, no prose.`

// aiNumber accepts what a model may answer for a number: a number, a
// numeric string, or null.
type aiNumber string

func (n *aiNumber) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` {
		*n = ""
		return nil
	}
	var str string
	if json.Unmarshal(b, &str) == nil {
		*n = aiNumber(strings.TrimSpace(str))
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*n = aiNumber(strconv.FormatFloat(f, 'f', -1, 64))
	return nil
}

type aiInvoice struct {
	InvoiceNumber string   `json:"invoice_number"`
	InvoiceDate   string   `json:"invoice_date"`
	SupplierName  string   `json:"supplier_name"`
	SupplierGSTIN string   `json:"supplier_gstin"`
	BuyerGSTIN    string   `json:"buyer_gstin"`
	Total         aiNumber `json:"invoice_total"`
	TotalQuantity aiNumber `json:"total_quantity"`
	LinesTotal    aiNumber `json:"lines_total"`
	Lines         []struct {
		Item      string   `json:"item"`
		Variant   string   `json:"variant"`
		HSN       string   `json:"hsn"`
		Quantity  aiNumber `json:"quantity"`
		Unit      string   `json:"unit"`
		MRP       aiNumber `json:"mrp"`
		Rate      aiNumber `json:"rate"`
		TaxAmount aiNumber `json:"tax_amount"`
		GSTRate   aiNumber `json:"gst_rate"`
		LineTotal aiNumber `json:"line_total"`
	} `json:"lines"`
}

func aiPrompt(tpl invoiceTemplate) string {
	var b strings.Builder
	b.WriteString(`Transcribe this invoice (every page / photo given, in order) into this JSON:
{
  "invoice_number": "as printed",
  "invoice_date": "YYYY-MM-DD (the invoice date; Indian invoices print the day first)",
  "supplier_name": "the seller's name",
  "supplier_gstin": "the seller's GSTIN",
  "buyer_gstin": "the buyer's GSTIN, or empty",
  "invoice_total": the grand total payable as printed,
  "total_quantity": the quantity on the item table's total row, or null,
  "lines_total": the amount on the item table's total row, or null,
  "lines": [ one entry per item line of the item table, in printed order:
    {"item": "the item name, with all its wrapped rows joined",
     "variant": "the text in a batch / size / variant column, as printed, or empty",
     "hsn": "HSN code or empty",
     "quantity": number, "unit": "PCS / KG / ... as printed, or empty",
     "mrp": number or null, "rate": the printed rate number or null,
     "tax_amount": number or null, "gst_rate": the GST percent printed for the line, or null,
     "line_total": the line's printed total amount}
  ]
}
Rules: numbers without currency signs or thousands separators. Do not include the total row, taxes summary, bank details or anything outside the item table in "lines". If a value isn't printed, use null or "". If a photo is cut off, transcribe what is visible only.`)
	var heads []string
	for _, f := range tpl.Columns.fields() {
		if f.field == "serial" {
			continue
		}
		heads = append(heads, f.field+" = "+strings.Join(f.labels, " / "))
	}
	if tpl.Saved {
		fmt.Fprintf(&b, "\nThis supplier (%s) prints these column headings: %s.", tpl.Supplier, strings.Join(heads, "; "))
	}
	if tpl.Options.VariantUse == "size" {
		b.WriteString("\nA column headed like \"BATCH NO.\" may hold sizes or the MRP instead of a batch: copy it into \"variant\" exactly as printed.")
	}
	if tpl.Options.AIHints != "" {
		b.WriteString("\nAbout this supplier's invoices: " + tpl.Options.AIHints)
	}
	return b.String()
}

// jsonObject: the outermost {...} in a model's answer (which may come in a
// ```json fence or with a sentence around it).
func jsonObject(s string) (string, error) {
	i := strings.Index(s, "{")
	j := strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return "", errors.New("no JSON object in the answer")
	}
	return s[i : j+1], nil
}

// parseAIInvoice turns the model's answer into the printed rows.
func parseAIInvoice(answer string) (rawDoc, error) {
	doc := rawDoc{Columns: map[string]string{}}
	obj, err := jsonObject(answer)
	if err != nil {
		return doc, err
	}
	var inv aiInvoice
	if err := json.Unmarshal([]byte(obj), &inv); err != nil {
		return doc, fmt.Errorf("the answer isn't the invoice JSON: %w", err)
	}
	if len(inv.Lines) == 0 {
		return doc, errNoTable
	}
	doc.InvoiceNumber = strings.TrimSpace(inv.InvoiceNumber)
	doc.InvoiceDate = parseDocDate(inv.InvoiceDate)
	doc.SupplierName = strings.TrimSpace(inv.SupplierName)
	for _, g := range []string{inv.SupplierGSTIN, inv.BuyerGSTIN} {
		if g = strings.ToUpper(strings.ReplaceAll(g, " ", "")); gstinRE.MatchString(g) && !contains(doc.GSTINs, g) {
			doc.GSTINs = append(doc.GSTINs, g)
		}
	}
	if v, ok := firstNumber(string(inv.Total)); ok {
		doc.Total = money(v)
	}
	if v, ok := firstNumber(string(inv.TotalQuantity)); ok {
		doc.SubtotalQty = qtyText(v)
	}
	if v, ok := firstNumber(string(inv.LinesTotal)); ok {
		doc.SubtotalTotal = money(v)
	}
	for _, l := range inv.Lines {
		qty := string(l.Quantity)
		if l.Unit != "" && qty != "" {
			qty += " " + l.Unit
		}
		gst := string(l.GSTRate)
		if gst != "" && !strings.Contains(gst, "%") {
			gst += "%"
		}
		doc.Rows = append(doc.Rows, rawRow{
			Item: l.Item, Variant: l.Variant, HSN: l.HSN, Quantity: qty, MRP: string(l.MRP), Rate: string(l.Rate),
			Tax: string(l.TaxAmount), GSTRate: gst, Total: string(l.LineTotal),
		})
	}
	return doc, nil
}

// readWithAI sends the files to the configured model and reads its answer.
func (h *Handler) readWithAI(ctx context.Context, files []attachment, tpl invoiceTemplate) (rawDoc, error) {
	var att []llm.Attachment
	for _, f := range files {
		att = append(att, llm.Attachment{MediaType: f.mediaType, Data: f.data})
	}
	answer, err := h.Reader.ReadDocument(ctx, aiSystemPrompt, aiPrompt(tpl), att)
	if err != nil {
		return rawDoc{}, err
	}
	return parseAIInvoice(answer)
}
