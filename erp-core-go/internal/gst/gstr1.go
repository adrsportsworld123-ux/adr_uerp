package gst

import (
	"context"
	"net/http"
	"strconv"
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
	SupplyType    string  `json:"sply_ty"` // always "INTRA" — see this file's doc comment on why interstate B2C isn't distinguished
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
	B2CS   []gstr1B2CSEntry `json:"b2cs"`
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
// Named, deliberate gaps (see Notes in the response, not silently
// dropped): B2CL ("B2C Large" — interstate B2C invoices over ₹2.5 lakh)
// isn't split out from B2CS, and B2CS itself always reports
// sply_ty="INTRA" — both need a customer's own ship-to state, which this
// codebase has no field for on a B2C (no-GSTIN) customer; only B2B
// customers carry a GSTIN, and a GSTIN's own first two digits are what
// place-of-supply actually is for that side. doc_issue (document-series
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
		totalTaxable := b2bTaxable + b2cTaxable
		totalTax := b2bTax + b2cTax

		resp.GSTR1JSON = gstr1JSON{GSTIN: gstin, Period: mmyyyy, B2B: b2b, B2CS: b2cs}
		resp.GSTR1JSON.HSN.Data = hsn
		resp.Summary = gstr1Summary{
			Period: resp.Period, B2BPartyCount: len(b2b), B2BInvoiceCount: invoiceCount,
			B2BTaxableValue: formatMoney(b2bTaxable), B2CTaxableValue: formatMoney(b2cTaxable),
			TotalTaxableValue: formatMoney(totalTaxable), TotalTax: formatMoney(totalTax),
		}
		resp.Reconciliation = reconciliationInfo{
			ComputedGSTOutput: formatMoney(totalTax), LedgerGSTPayable: formatMoney(ledgerGSTPayable),
			Matches: within(totalTax, ledgerGSTPayable, 0.02),
		}
		resp.Disclaimer = schemaDisclaimer
		resp.Notes = []string{
			"B2CL (interstate B2C > Rs.2.5 lakh) is not split out — every B2C sale is reported under B2CS as intrastate; see this endpoint's doc comment.",
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
		SELECT so.id, so.order_number, so.finalized_at::date::text, so.grand_total::text, c.gstin,
		       COALESCE(ts.cgst_rate,0) + COALESCE(ts.sgst_rate,0) + COALESCE(ts.igst_rate,0) AS rate,
		       SUM(sol.unit_price*sol.quantity - sol.discount_amount) AS txval,
		       SUM((sol.unit_price*sol.quantity - sol.discount_amount) * COALESCE(ts.cgst_rate,0) / 100) AS cgst,
		       SUM((sol.unit_price*sol.quantity - sol.discount_amount) * COALESCE(ts.sgst_rate,0) / 100) AS sgst,
		       SUM((sol.unit_price*sol.quantity - sol.discount_amount) * COALESCE(ts.igst_rate,0) / 100) AS igst,
		       SUM((sol.unit_price*sol.quantity - sol.discount_amount) * COALESCE(ts.cess_rate,0) / 100) AS cess
		FROM sales_orders so
		JOIN customers c ON c.id = so.customer_id
		JOIN sales_order_lines sol ON sol.sales_order_id = so.id
		JOIN product_variants pv ON pv.id = sol.variant_id
		JOIN products p ON p.id = pv.product_id
		LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
		WHERE so.status = 'finalized' AND so.finalized_at >= $1 AND so.finalized_at < $2
		  AND c.gstin IS NOT NULL AND c.gstin != ''
		GROUP BY so.id, so.order_number, so.finalized_at, so.grand_total, c.gstin, ts.cgst_rate, ts.sgst_rate, ts.igst_rate
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
		var orderID, orderNumber, finalizedAt, grandTotal, custGSTIN string
		var rate, txval, cgst, sgst, igst, cess float64
		if err := rows.Scan(&orderID, &orderNumber, &finalizedAt, &grandTotal, &custGSTIN, &rate, &txval, &cgst, &sgst, &igst, &cess); err != nil {
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
				PlaceOfSupply: stateCode(custGSTIN), ReverseCharge: "N", InvoiceType: "R",
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

// loadB2CS consolidates every finalized, in-period order with no
// customer, or a customer with no GSTIN on file, by (branch, rate) —
// place of supply is the selling branch's own state (see this file's
// GSTR1 doc comment for why a B2C customer's own state can't be used).
func loadB2CS(ctx context.Context, tx pgx.Tx, start, end time.Time) ([]gstr1B2CSEntry, float64, float64, error) {
	rows, err := tx.Query(ctx, `
		SELECT COALESCE(b.gstin,''),
		       COALESCE(ts.cgst_rate,0) + COALESCE(ts.sgst_rate,0) + COALESCE(ts.igst_rate,0) AS rate,
		       SUM(sol.unit_price*sol.quantity - sol.discount_amount) AS txval,
		       SUM((sol.unit_price*sol.quantity - sol.discount_amount) * COALESCE(ts.cgst_rate,0) / 100) AS cgst,
		       SUM((sol.unit_price*sol.quantity - sol.discount_amount) * COALESCE(ts.sgst_rate,0) / 100) AS sgst,
		       SUM((sol.unit_price*sol.quantity - sol.discount_amount) * COALESCE(ts.igst_rate,0) / 100) AS igst,
		       SUM((sol.unit_price*sol.quantity - sol.discount_amount) * COALESCE(ts.cess_rate,0) / 100) AS cess
		FROM sales_order_lines sol
		JOIN sales_orders so ON so.id = sol.sales_order_id
		JOIN branches b ON b.id = so.branch_id
		JOIN product_variants pv ON pv.id = sol.variant_id
		JOIN products p ON p.id = pv.product_id
		LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
		LEFT JOIN customers c ON c.id = so.customer_id
		WHERE so.status = 'finalized' AND so.finalized_at >= $1 AND so.finalized_at < $2
		  AND (c.gstin IS NULL OR c.gstin = '')
		GROUP BY b.gstin, ts.cgst_rate, ts.sgst_rate, ts.igst_rate, ts.cess_rate
		ORDER BY b.gstin, rate`, start, end)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var entries []gstr1B2CSEntry
	var totalTaxable, totalTax float64
	for rows.Next() {
		var branchGSTIN string
		var rate, txval, cgst, sgst, igst, cess float64
		if err := rows.Scan(&branchGSTIN, &rate, &txval, &cgst, &sgst, &igst, &cess); err != nil {
			return nil, 0, 0, err
		}
		totalTaxable += txval
		totalTax += cgst + sgst + igst + cess
		entries = append(entries, gstr1B2CSEntry{
			SupplyType: "INTRA", PlaceOfSupply: stateCode(branchGSTIN), Type: "OE",
			TaxableValue: formatMoney(txval), Rate: rate,
			IGST: formatMoney(igst), CGST: formatMoney(cgst), SGST: formatMoney(sgst), Cess: formatMoney(cess),
		})
	}
	return entries, totalTaxable, totalTax, rows.Err()
}

// loadHSN summarizes every finalized, in-period order line (both B2B and
// B2C) by (HSN code, rate) — the return's HSN-wise summary section.
func loadHSN(ctx context.Context, tx pgx.Tx, start, end time.Time) ([]gstr1HSNEntry, error) {
	rows, err := tx.Query(ctx, `
		SELECT COALESCE(p.hsn_code,''), p.name,
		       COALESCE(ts.cgst_rate,0) + COALESCE(ts.sgst_rate,0) + COALESCE(ts.igst_rate,0) AS rate,
		       SUM(sol.quantity) AS qty,
		       SUM(sol.unit_price*sol.quantity - sol.discount_amount) AS txval,
		       SUM((sol.unit_price*sol.quantity - sol.discount_amount) * COALESCE(ts.cgst_rate,0) / 100) AS cgst,
		       SUM((sol.unit_price*sol.quantity - sol.discount_amount) * COALESCE(ts.sgst_rate,0) / 100) AS sgst,
		       SUM((sol.unit_price*sol.quantity - sol.discount_amount) * COALESCE(ts.igst_rate,0) / 100) AS igst,
		       SUM((sol.unit_price*sol.quantity - sol.discount_amount) * COALESCE(ts.cess_rate,0) / 100) AS cess
		FROM sales_order_lines sol
		JOIN sales_orders so ON so.id = sol.sales_order_id
		JOIN product_variants pv ON pv.id = sol.variant_id
		JOIN products p ON p.id = pv.product_id
		LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
		WHERE so.status = 'finalized' AND so.finalized_at >= $1 AND so.finalized_at < $2
		GROUP BY p.hsn_code, p.name, ts.cgst_rate, ts.sgst_rate, ts.igst_rate, ts.cess_rate
		ORDER BY p.hsn_code`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []gstr1HSNEntry
	num := 1
	for rows.Next() {
		var hsnCode, name string
		var rate, qty, txval, cgst, sgst, igst, cess float64
		if err := rows.Scan(&hsnCode, &name, &rate, &qty, &txval, &cgst, &sgst, &igst, &cess); err != nil {
			return nil, err
		}
		entries = append(entries, gstr1HSNEntry{
			Num: num, HSN: hsnCode, Description: name, UQC: "NOS", Quantity: strconv.FormatFloat(qty, 'f', 3, 64),
			TaxableValue: formatMoney(txval), Rate: rate,
			IGST: formatMoney(igst), CGST: formatMoney(cgst), SGST: formatMoney(sgst), Cess: formatMoney(cess),
		})
		num++
	}
	return entries, rows.Err()
}
