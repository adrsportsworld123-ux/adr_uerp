package gst

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// GSTR-1 sections built from the sale's own place of supply
// (migrations/060-061) and its returns:
//
//   - Invoices are every sale issued in the period, including one later
//     returned in part or in full (it was still an invoice); a voided sale
//     never was.
//   - A return is a credit note, dated when it was made: against a B2B
//     invoice it is reported in CDNR (per customer GSTIN); against a B2CL
//     invoice in CDNUR; against any other B2C invoice it reduces B2CS, which
//     the return reports net.
//   - B2CL: an inter-state B2C invoice above b2clThreshold, invoice-wise.
//     Every other B2C supply is consolidated in B2CS by place of supply,
//     supply type and rate.
//   - The HSN summary is net of the period's returns.

// soldStatuses are the sales that were invoiced.
const soldStatuses = `('finalized', 'partially_refunded', 'refunded')`

// b2clThreshold: an inter-state B2C invoice above this value is reported
// invoice-wise (B2CL). ₹1,00,000 since 1 August 2024 (CGST Notification
// 12/2024; it was ₹2,50,000 before).
const b2clThreshold = 100000.0

// isB2CL is the SQL test for a B2CL invoice (so = the sale, c = its customer).
var isB2CL = `(so.inter_state AND (c.gstin IS NULL OR c.gstin = '') AND so.grand_total + so.round_off > ` +
	strconv.FormatFloat(b2clThreshold, 'f', 0, 64) + `)`

// returnLineCols: a return line's figures, each credit note line split as
// its invoice was (cess = its share of the invoice line's).
const returnLineFrom = `
	FROM sales_return_lines rl
	JOIN sales_returns r ON r.id = rl.sales_return_id
	JOIN sales_orders so ON so.id = r.sales_order_id
	JOIN sales_order_lines sol ON sol.id = rl.sales_order_line_id
	LEFT JOIN customers c ON c.id = so.customer_id
	CROSS JOIN LATERAL (SELECT COALESCE(round(rl.tax_amount * sol.cess_amount / NULLIF(sol.tax_amount, 0), 2), 0) AS cess) x`

type gstr1B2CLInvoice struct {
	InvoiceNumber string             `json:"inum"`
	InvoiceDate   string             `json:"idt"`
	Value         string             `json:"val"`
	Items         []gstr1InvoiceItem `json:"itms"`
}

type gstr1B2CLEntry struct {
	PlaceOfSupply string             `json:"pos"`
	Invoices      []gstr1B2CLInvoice `json:"inv"`
}

type gstr1Note struct {
	NoteType      string             `json:"ntty"` // "C" = credit note
	NoteNumber    string             `json:"nt_num"`
	NoteDate      string             `json:"nt_dt"`
	Value         string             `json:"val"`
	PlaceOfSupply string             `json:"pos"`
	ReverseCharge string             `json:"rchrg,omitempty"`
	InvoiceType   string             `json:"inv_typ,omitempty"`
	Type          string             `json:"typ,omitempty"` // CDNUR: "B2CL"
	Items         []gstr1InvoiceItem `json:"itms"`
}

type gstr1CDNRParty struct {
	CustomerGSTIN string      `json:"ctin"`
	Notes         []gstr1Note `json:"nt"`
}

func ddmmyyyy(isoDate string) string {
	d, err := time.Parse("2006-01-02", isoDate)
	if err != nil {
		return isoDate
	}
	return d.Format("02-01-2006")
}

func item(n int, txval, rate, igst, cgst, sgst, cess float64) gstr1InvoiceItem {
	return gstr1InvoiceItem{Num: n, ItemDetail: gstr1ItemDetail{TaxableValue: formatMoney(txval), Rate: rate,
		IGST: formatMoney(igst), CGST: formatMoney(cgst), SGST: formatMoney(sgst), Cess: formatMoney(cess)}}
}

// loadB2CS: B2C supplies other than B2CL, by place of supply, supply type
// and rate, net of the period's credit notes against them.
func loadB2CS(ctx context.Context, tx pgx.Tx, start, end time.Time) ([]gstr1B2CSEntry, float64, float64, error) {
	rows, err := tx.Query(ctx, `
		WITH lines AS (
			SELECT COALESCE(so.place_of_supply, gst_state_of(b.gstin), '') AS pos, so.inter_state AS inter, COALESCE(sol.tax_rate, 0) AS rate,
			       sol.taxable_value AS txval, gst_cgst(sol.tax_amount, sol.cess_amount, so.inter_state) AS cgst,
			       gst_sgst(sol.tax_amount, sol.cess_amount, so.inter_state) AS sgst,
			       gst_igst(sol.tax_amount, sol.cess_amount, so.inter_state) AS igst, sol.cess_amount AS cess
			FROM sales_order_lines sol
			JOIN sales_orders so ON so.id = sol.sales_order_id
			JOIN branches b ON b.id = so.branch_id
			LEFT JOIN customers c ON c.id = so.customer_id
			WHERE so.status IN `+soldStatuses+` AND so.finalized_at >= $1 AND so.finalized_at < $2
			  AND (c.gstin IS NULL OR c.gstin = '') AND NOT `+isB2CL+`
			UNION ALL
			SELECT COALESCE(so.place_of_supply, gst_state_of(b.gstin), ''), so.inter_state, COALESCE(sol.tax_rate, 0),
			       -rl.taxable_amount, -gst_cgst(rl.tax_amount, x.cess, so.inter_state), -gst_sgst(rl.tax_amount, x.cess, so.inter_state),
			       -gst_igst(rl.tax_amount, x.cess, so.inter_state), -x.cess
			`+returnLineFrom+`
			JOIN branches b ON b.id = so.branch_id
			WHERE r.created_at >= $1 AND r.created_at < $2
			  AND (c.gstin IS NULL OR c.gstin = '') AND NOT `+isB2CL+`
		)
		SELECT pos, inter, rate, SUM(txval), SUM(cgst), SUM(sgst), SUM(igst), SUM(cess)
		FROM lines GROUP BY pos, inter, rate
		HAVING SUM(txval) <> 0 OR SUM(cgst + sgst + igst + cess) <> 0
		ORDER BY pos, inter, rate`, start, end)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	var entries []gstr1B2CSEntry
	var totalTaxable, totalTax float64
	for rows.Next() {
		var pos string
		var inter bool
		var rate, txval, cgst, sgst, igst, cess float64
		if err := rows.Scan(&pos, &inter, &rate, &txval, &cgst, &sgst, &igst, &cess); err != nil {
			return nil, 0, 0, err
		}
		totalTaxable += txval
		totalTax += cgst + sgst + igst + cess
		supplyType := "INTRA"
		if inter {
			supplyType = "INTER"
		}
		entries = append(entries, gstr1B2CSEntry{
			SupplyType: supplyType, PlaceOfSupply: pos, Type: "OE", TaxableValue: formatMoney(txval), Rate: rate,
			IGST: formatMoney(igst), CGST: formatMoney(cgst), SGST: formatMoney(sgst), Cess: formatMoney(cess),
		})
	}
	return entries, totalTaxable, totalTax, rows.Err()
}

// loadB2CL: inter-state B2C invoices above b2clThreshold, by place of
// supply, itemized by rate.
func loadB2CL(ctx context.Context, tx pgx.Tx, start, end time.Time) ([]gstr1B2CLEntry, float64, float64, error) {
	rows, err := tx.Query(ctx, `
		SELECT COALESCE(so.place_of_supply, ''), so.id, so.order_number, so.finalized_at::date::text, (so.grand_total + so.round_off)::text,
		       COALESCE(sol.tax_rate, 0), SUM(sol.taxable_value), SUM(gst_igst(sol.tax_amount, sol.cess_amount, so.inter_state)), SUM(sol.cess_amount)
		FROM sales_orders so
		JOIN sales_order_lines sol ON sol.sales_order_id = so.id
		LEFT JOIN customers c ON c.id = so.customer_id
		WHERE so.status IN `+soldStatuses+` AND so.finalized_at >= $1 AND so.finalized_at < $2 AND `+isB2CL+`
		GROUP BY so.place_of_supply, so.id, so.order_number, so.finalized_at, so.grand_total, so.round_off, sol.tax_rate
		ORDER BY so.place_of_supply, so.finalized_at, sol.tax_rate`, start, end)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	var out []gstr1B2CLEntry
	posIdx := map[string]int{}
	invIdx := map[string][2]int{}
	var totalTaxable, totalTax float64
	for rows.Next() {
		var pos, id, number, date, value string
		var rate, txval, igst, cess float64
		if err := rows.Scan(&pos, &id, &number, &date, &value, &rate, &txval, &igst, &cess); err != nil {
			return nil, 0, 0, err
		}
		totalTaxable += txval
		totalTax += igst + cess
		p, ok := posIdx[pos]
		if !ok {
			out = append(out, gstr1B2CLEntry{PlaceOfSupply: pos})
			p = len(out) - 1
			posIdx[pos] = p
		}
		ix, ok := invIdx[id]
		if !ok {
			out[p].Invoices = append(out[p].Invoices, gstr1B2CLInvoice{InvoiceNumber: number, InvoiceDate: ddmmyyyy(date), Value: value})
			ix = [2]int{p, len(out[p].Invoices) - 1}
			invIdx[id] = ix
		}
		inv := &out[ix[0]].Invoices[ix[1]]
		inv.Items = append(inv.Items, item(len(inv.Items)+1, txval, rate, igst, 0, 0, cess))
	}
	return out, totalTaxable, totalTax, rows.Err()
}

// loadCreditNotes: the period's returns against B2B invoices (CDNR, by
// customer GSTIN) and against B2CL invoices (CDNUR), itemized by rate.
func loadCreditNotes(ctx context.Context, tx pgx.Tx, start, end time.Time) ([]gstr1CDNRParty, []gstr1Note, float64, float64, error) {
	rows, err := tx.Query(ctx, `
		SELECT COALESCE(c.gstin, ''), `+isB2CL+`, r.id, r.return_number, r.created_at::date::text,
		       (r.subtotal + r.tax_amount)::text, COALESCE(so.place_of_supply, ''),
		       COALESCE(sol.tax_rate, 0), SUM(rl.taxable_amount),
		       SUM(gst_igst(rl.tax_amount, x.cess, so.inter_state)), SUM(gst_cgst(rl.tax_amount, x.cess, so.inter_state)),
		       SUM(gst_sgst(rl.tax_amount, x.cess, so.inter_state)), SUM(x.cess)
		`+returnLineFrom+`
		WHERE r.created_at >= $1 AND r.created_at < $2
		  AND ((c.gstin IS NOT NULL AND c.gstin <> '') OR `+isB2CL+`)
		GROUP BY c.gstin, so.inter_state, so.grand_total, so.round_off, r.id, r.return_number, r.created_at, r.subtotal, r.tax_amount,
		         so.place_of_supply, sol.tax_rate
		ORDER BY c.gstin, r.created_at, r.id, sol.tax_rate`, start, end)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	defer rows.Close()
	var parties []gstr1CDNRParty
	var unregistered []gstr1Note
	partyIdx := map[string]int{}
	type at struct{ party, note int } // party -1 = CDNUR
	noteIdx := map[string]at{}
	var totalTaxable, totalTax float64
	for rows.Next() {
		var gstin, id, number, date, value, pos string
		var b2cl bool
		var rate, txval, igst, cgst, sgst, cess float64
		if err := rows.Scan(&gstin, &b2cl, &id, &number, &date, &value, &pos, &rate, &txval, &igst, &cgst, &sgst, &cess); err != nil {
			return nil, nil, 0, 0, err
		}
		totalTaxable += txval
		totalTax += igst + cgst + sgst + cess
		loc, ok := noteIdx[id]
		if !ok {
			note := gstr1Note{NoteType: "C", NoteNumber: number, NoteDate: ddmmyyyy(date), Value: value, PlaceOfSupply: pos}
			if gstin != "" {
				note.ReverseCharge, note.InvoiceType = "N", "R"
				p, ok := partyIdx[gstin]
				if !ok {
					parties = append(parties, gstr1CDNRParty{CustomerGSTIN: gstin})
					p = len(parties) - 1
					partyIdx[gstin] = p
				}
				parties[p].Notes = append(parties[p].Notes, note)
				loc = at{p, len(parties[p].Notes) - 1}
			} else {
				note.Type = "B2CL"
				unregistered = append(unregistered, note)
				loc = at{-1, len(unregistered) - 1}
			}
			noteIdx[id] = loc
		}
		var n *gstr1Note
		if loc.party >= 0 {
			n = &parties[loc.party].Notes[loc.note]
		} else {
			n = &unregistered[loc.note]
		}
		n.Items = append(n.Items, item(len(n.Items)+1, txval, rate, igst, cgst, sgst, cess))
	}
	return parties, unregistered, totalTaxable, totalTax, rows.Err()
}

// loadHSN: every invoiced line of the period by (HSN, rate), net of the
// period's returns.
func loadHSN(ctx context.Context, tx pgx.Tx, start, end time.Time) ([]gstr1HSNEntry, error) {
	rows, err := tx.Query(ctx, `
		WITH lines AS (
			SELECT sol.variant_id, COALESCE(sol.tax_rate, 0) AS rate, sol.quantity AS qty, sol.taxable_value AS txval,
			       gst_cgst(sol.tax_amount, sol.cess_amount, so.inter_state) AS cgst, gst_sgst(sol.tax_amount, sol.cess_amount, so.inter_state) AS sgst,
			       gst_igst(sol.tax_amount, sol.cess_amount, so.inter_state) AS igst, sol.cess_amount AS cess
			FROM sales_order_lines sol
			JOIN sales_orders so ON so.id = sol.sales_order_id
			WHERE so.status IN `+soldStatuses+` AND so.finalized_at >= $1 AND so.finalized_at < $2
			UNION ALL
			SELECT rl.variant_id, COALESCE(sol.tax_rate, 0), -rl.quantity, -rl.taxable_amount,
			       -gst_cgst(rl.tax_amount, x.cess, so.inter_state), -gst_sgst(rl.tax_amount, x.cess, so.inter_state),
			       -gst_igst(rl.tax_amount, x.cess, so.inter_state), -x.cess
			`+returnLineFrom+`
			WHERE r.created_at >= $1 AND r.created_at < $2
		)
		SELECT COALESCE(p.hsn_code, ''), p.name, l.rate, SUM(l.qty), SUM(l.txval), SUM(l.cgst), SUM(l.sgst), SUM(l.igst), SUM(l.cess)
		FROM lines l
		JOIN product_variants pv ON pv.id = l.variant_id
		JOIN products p ON p.id = pv.product_id
		GROUP BY p.hsn_code, p.name, l.rate
		HAVING SUM(l.qty) <> 0 OR SUM(l.txval) <> 0
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
