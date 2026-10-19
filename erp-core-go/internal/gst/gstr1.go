package gst

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/httpx"
)

type gstr1ItemDetail struct {
	TaxableValue string  `json:"txval"`
	Rate         float64 `json:"rt"`
	IGST         string  `json:"iamt"`
	CGST         string  `json:"camt"`
	SGST         string  `json:"samt"`
	Cess         string  `json:"csamt"`
}

type gstr1InvoiceItem struct {
	Num        int             `json:"num"`
	ItemDetail gstr1ItemDetail `json:"itm_det"`
}

type gstr1Invoice struct {
	InvoiceNumber string             `json:"inum"`
	InvoiceDate   string             `json:"idt"` // DD-MM-YYYY per GSTN convention
	Value         string             `json:"val"`
	PlaceOfSupply string             `json:"pos"`
	ReverseCharge string             `json:"rchrg"`   // always "N" — Reverse Charge Mechanism isn't modeled yet (a separate, not-yet-built FRD item)
	InvoiceType   string             `json:"inv_typ"` // always "R" (Regular) — credit/debit notes aren't modeled yet
	Items         []gstr1InvoiceItem `json:"itms"`
}

type gstr1B2BParty struct {
	CustomerGSTIN string         `json:"ctin"`
	Invoices      []gstr1Invoice `json:"inv"`
}

type gstr1B2CSEntry struct {
	SupplyType    string  `json:"sply_ty"` // INTRA / INTER, from the sale's own place of supply (migrations/060)
	PlaceOfSupply string  `json:"pos"`
	Type          string  `json:"typ"` // "OE" = Other than E-commerce; this codebase has no e-commerce channel yet
	TaxableValue  string  `json:"txval"`
	Rate          float64 `json:"rt"`
	IGST          string  `json:"iamt"`
	CGST          string  `json:"camt"`
	SGST          string  `json:"samt"`
	Cess          string  `json:"csamt"`
}

type gstr1HSNEntry struct {
	Num          int     `json:"num"`
	HSN          string  `json:"hsn_sc"`
	Description  string  `json:"desc"`
	UQC          string  `json:"uqc"`
	Quantity     string  `json:"qty"`
	TaxableValue string  `json:"txval"`
	Rate         float64 `json:"rt"`
	IGST         string  `json:"iamt"`
	CGST         string  `json:"camt"`
	SGST         string  `json:"samt"`
	Cess         string  `json:"csamt"`
}

type gstr1JSON struct {
	GSTIN  string           `json:"gstin"`
	Period string           `json:"fp"` // MMYYYY
	B2B    []gstr1B2BParty  `json:"b2b"`
	B2CL   []gstr1B2CLEntry `json:"b2cl"`
	B2CS   []gstr1B2CSEntry `json:"b2cs"`
	CDNR   []gstr1CDNRParty `json:"cdnr"`
	CDNUR  []gstr1Note      `json:"cdnur"`
	HSN    struct {
		Data []gstr1HSNEntry `json:"data"`
	} `json:"hsn"`
}

type gstr1Summary struct {
	Period            string `json:"period"`
	B2BPartyCount     int    `json:"b2b_party_count"`
	B2BInvoiceCount   int    `json:"b2b_invoice_count"`
	B2BTaxableValue   string `json:"b2b_taxable_value"`
	B2CTaxableValue   string `json:"b2c_taxable_value"`
	TotalTaxableValue string `json:"total_taxable_value"`
	TotalTax          string `json:"total_tax"`
	// Inter-state B2C invoices above the B2CL limit, and the period's
	// credit notes against B2B / B2CL invoices (B2CS is already net of
	// its own). Totals above are net of credit notes.
	B2CLInvoiceCount  int    `json:"b2cl_invoice_count"`
	B2CLTaxableValue  string `json:"b2cl_taxable_value"`
	CreditNoteCount   int    `json:"credit_note_count"`
	CreditNoteTaxable string `json:"credit_note_taxable_value"`
	CreditNoteTax     string `json:"credit_note_tax"`
}

type gstr1Response struct {
	Period         string             `json:"period"`
	GSTIN          string             `json:"gstin"`
	Summary        gstr1Summary       `json:"summary"`
	GSTR1JSON      gstr1JSON          `json:"gstr1_json"`
	Reconciliation reconciliationInfo `json:"reconciliation"`
	Disclaimer     string             `json:"disclaimer"`
	Notes          []string           `json:"notes"`
}

// GSTR1: GET /gst/gstr1?period=YYYY-MM — the monthly outward-supplies
// return: B2B (invoice-wise, one entry per customer GSTIN), B2CS (B2C
// small, consolidated by rate), and an HSN-wise summary.
//
// Every amount is the invoice's own: each line's taxable value, its GST
// rate as written (sales_order_lines.tax_rate) and its tax split by the
// sale's place of supply — CGST + SGST intra-state, IGST inter-state
// (migrations/060, gst_cgst / gst_sgst / gst_igst) — so the return adds
// up to exactly what the invoices printed.
//
// Inter-state B2C invoices above the limit are B2CL, and returns are
// credit notes (CDNR / CDNUR, B2CS net): gstr1_notes.go. A B2C sale's
// place of supply is the selling branch's state unless the sale named a
// delivery state (migrations/061).
//
// Named, deliberate gaps (see Notes in the response, not silently
// dropped): doc_issue (document-series
// summary) isn't built — this codebase's order_number isn't a GST-
// compliant sequential invoice series, there's no series concept to
// summarize. UQC (unit of measure) is always "NOS" (pieces) — products
// don't carry a unit-of-measure field yet, so a merchant selling by
// weight/volume would need that added before this figure is meaningful
// for them.
func (h *Handler) GSTR1(w http.ResponseWriter, r *http.Request) {
	claims, ok := authRequired(w, r)
	if !ok {
		return
	}
	_, start, end, mmyyyy, ok := periodParam(r)
	if !ok {
		invalidPeriod(w)
		return
	}

	var resp gstr1Response
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		gstin, err := merchantGSTIN(ctx, tx)
		if err != nil {
			return err
		}
		resp.GSTIN = gstin
		resp.Period = start.Format("2006-01")

		b2b, b2bTaxable, b2bTax, err := loadB2B(ctx, tx, start, end)
		if err != nil {
			return err
		}
		b2cs, b2cTaxable, b2cTax, err := loadB2CS(ctx, tx, start, end)
		if err != nil {
			return err
		}
		b2cl, b2clTaxable, b2clTax, err := loadB2CL(ctx, tx, start, end)
		if err != nil {
			return err
		}
		cdnr, cdnur, cnTaxable, cnTax, err := loadCreditNotes(ctx, tx, start, end)
		if err != nil {
			return err
		}
		hsn, err := loadHSN(ctx, tx, start, end)
		if err != nil {
			return err
		}
		ledgerGSTPayable, err := reconcileGSTPayable(ctx, tx, start, end)
		if err != nil {
			return err
		}

		invoiceCount := 0
		for _, party := range b2b {
			invoiceCount += len(party.Invoices)
		}
		b2clCount, noteCount := 0, len(cdnur)
		for _, e := range b2cl {
			b2clCount += len(e.Invoices)
		}
		for _, p := range cdnr {
			noteCount += len(p.Notes)
		}
		totalTaxable := b2bTaxable + b2cTaxable + b2clTaxable - cnTaxable
		totalTax := b2bTax + b2cTax + b2clTax - cnTax
		if b2cl == nil {
			b2cl = []gstr1B2CLEntry{}
		}
		if cdnr == nil {
			cdnr = []gstr1CDNRParty{}
		}
		if cdnur == nil {
			cdnur = []gstr1Note{}
		}

		resp.GSTR1JSON = gstr1JSON{GSTIN: gstin, Period: mmyyyy, B2B: b2b, B2CL: b2cl, B2CS: b2cs, CDNR: cdnr, CDNUR: cdnur}
		resp.GSTR1JSON.HSN.Data = hsn
		resp.Summary = gstr1Summary{
			Period: resp.Period, B2BPartyCount: len(b2b), B2BInvoiceCount: invoiceCount,
			B2BTaxableValue: formatMoney(b2bTaxable), B2CTaxableValue: formatMoney(b2cTaxable),
			TotalTaxableValue: formatMoney(totalTaxable), TotalTax: formatMoney(totalTax),
			B2CLInvoiceCount: b2clCount, B2CLTaxableValue: formatMoney(b2clTaxable),
			CreditNoteCount: noteCount, CreditNoteTaxable: formatMoney(cnTaxable), CreditNoteTax: formatMoney(cnTax),
		}
		resp.Reconciliation = reconciliationInfo{
			ComputedGSTOutput: formatMoney(totalTax), LedgerGSTPayable: formatMoney(ledgerGSTPayable),
			Matches: within(totalTax, ledgerGSTPayable, 0.02),
		}
		resp.Disclaimer = schemaDisclaimer
		resp.Notes = []string{
			"A B2C sale's place of supply is the selling branch's state unless the sale named a delivery state (Place of supply on the sale).",
			"Returns are reported as credit notes dated when they were made: CDNR for B2B invoices, CDNUR for B2CL invoices; B2CS is net of returns against B2C invoices.",
			"doc_issue (document series summary) is not included — this system's order numbers aren't a GST-compliant sequential invoice series.",
			"UQC is always NOS (pieces) — products don't carry a unit-of-measure field yet.",
		}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not compute GSTR-1")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// loadB2B groups every finalized, in-period order for a customer with a
// GSTIN on file into one invoice per order, itemized by tax rate within
// that invoice (a single invoice can mix rates across its lines).
// Returns the nested JSON structure plus running taxable-value/tax
// totals so the caller doesn't have to walk it again for the summary.
func loadB2B(ctx context.Context, tx pgx.Tx, start, end time.Time) ([]gstr1B2BParty, float64, float64, error) {
	rows, err := tx.Query(ctx, `
		SELECT so.id, so.order_number, so.finalized_at::date::text, (so.grand_total + so.round_off)::text, c.gstin, -- invoice value incl. round-off
		       COALESCE(so.place_of_supply, ''),
		       COALESCE(sol.tax_rate, 0) AS rate,
		       SUM(sol.taxable_value) AS txval,
		       SUM(gst_cgst(sol.tax_amount, sol.cess_amount, so.inter_state)) AS cgst,
		       SUM(gst_sgst(sol.tax_amount, sol.cess_amount, so.inter_state)) AS sgst,
		       SUM(gst_igst(sol.tax_amount, sol.cess_amount, so.inter_state)) AS igst,
		       SUM(sol.cess_amount) AS cess
		FROM sales_orders so
		JOIN customers c ON c.id = so.customer_id
		JOIN sales_order_lines sol ON sol.sales_order_id = so.id
		WHERE so.status IN `+soldStatuses+` AND so.finalized_at >= $1 AND so.finalized_at < $2
		  AND c.gstin IS NOT NULL AND c.gstin != ''
		GROUP BY so.id, so.order_number, so.finalized_at, so.grand_total, so.round_off, c.gstin, so.place_of_supply, sol.tax_rate
		ORDER BY c.gstin, so.finalized_at, rate`, start, end)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	type invoiceKey struct{ gstin, orderID string }
	invoiceIndex := map[invoiceKey]int{}
	partyIndex := map[string]int{}
	var parties []gstr1B2BParty
	var totalTaxable, totalTax float64

	for rows.Next() {
		var orderID, orderNumber, finalizedAt, grandTotal, custGSTIN, pos string
		var rate, txval, cgst, sgst, igst, cess float64
		if err := rows.Scan(&orderID, &orderNumber, &finalizedAt, &grandTotal, &custGSTIN, &pos, &rate, &txval, &cgst, &sgst, &igst, &cess); err != nil {
			return nil, 0, 0, err
		}
		totalTaxable += txval
		totalTax += cgst + sgst + igst + cess

		pIdx, ok := partyIndex[custGSTIN]
		if !ok {
			parties = append(parties, gstr1B2BParty{CustomerGSTIN: custGSTIN})
			pIdx = len(parties) - 1
			partyIndex[custGSTIN] = pIdx
		}

		key := invoiceKey{custGSTIN, orderID}
		iIdx, ok := invoiceIndex[key]
		if !ok {
			invDate, _ := time.Parse("2006-01-02", finalizedAt)
			parties[pIdx].Invoices = append(parties[pIdx].Invoices, gstr1Invoice{
				InvoiceNumber: orderNumber, InvoiceDate: invDate.Format("02-01-2006"), Value: grandTotal,
				PlaceOfSupply: firstNonEmpty(pos, stateCode(custGSTIN)), ReverseCharge: "N", InvoiceType: "R",
			})
			iIdx = len(parties[pIdx].Invoices) - 1
			invoiceIndex[key] = iIdx
		}
		inv := &parties[pIdx].Invoices[iIdx]
		inv.Items = append(inv.Items, gstr1InvoiceItem{
			Num: len(inv.Items) + 1,
			ItemDetail: gstr1ItemDetail{
				TaxableValue: formatMoney(txval), Rate: rate,
				IGST: formatMoney(igst), CGST: formatMoney(cgst), SGST: formatMoney(sgst), Cess: formatMoney(cess),
			},
		})
	}
	return parties, totalTaxable, totalTax, rows.Err()
}
