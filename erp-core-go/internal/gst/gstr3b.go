package gst

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/httpx"
)

type taxBreakup struct {
	TaxableValue string `json:"txval"`
	IGST         string `json:"iamt"`
	CGST         string `json:"camt"`
	SGST         string `json:"samt"`
	Cess         string `json:"csamt"`
}

type itcAvlEntry struct {
	Type string `json:"ty"` // "IMPG"|"ISRC"|"ISD"|"OTH" per GSTN's schema — this codebase only ever produces "OTH" (see GSTR3B's doc comment)
	IGST string `json:"iamt"`
	CGST string `json:"camt"`
	SGST string `json:"samt"`
	Cess string `json:"csamt"`
}

type gstr3bJSON struct {
	GSTIN      string `json:"gstin"`
	RetPeriod  string `json:"ret_period"`
	SupDetails struct {
		OutwardTaxable taxBreakup `json:"osup_det"`
	} `json:"sup_details"`
	ITCEligible struct {
		Available []itcAvlEntry `json:"itc_avl"`
	} `json:"itc_elg"`
}

type gstr3bSummary struct {
	Period          string `json:"period"`
	OutwardTaxable  string `json:"outward_taxable_value"`
	CGST            string `json:"cgst"`
	SGST            string `json:"sgst"`
	IGST            string `json:"igst"`
	Cess            string `json:"cess"`
	TotalOutwardTax string `json:"total_outward_tax"`
	ITCAvailable    string `json:"itc_available"`
	NetTaxPayable   string `json:"net_tax_payable"`
}

type reconciliationInfo struct {
	ComputedGSTOutput string `json:"computed_gst_output"`
	LedgerGSTPayable  string `json:"ledger_gst_payable"`
	Matches           bool   `json:"matches"`
}

type gstr3bResponse struct {
	Period         string             `json:"period"`
	GSTIN          string             `json:"gstin"`
	Summary        gstr3bSummary      `json:"summary"`
	GSTR3BJSON     gstr3bJSON         `json:"gstr3b_json"`
	Reconciliation reconciliationInfo `json:"reconciliation"`
	Disclaimer     string             `json:"disclaimer"`
}

// GSTR3B: GET /gst/gstr3b?period=YYYY-MM — the monthly summary return.
//
// Table 3.1's outward-taxable-supply row is the only one this codebase
// can populate with real data: "zero rated" (exports/SEZ) and "nil
// rated/exempted" both need a tax-exemption concept this codebase doesn't
// model yet (pos_frd_complete.md §4's own separate, not-yet-built "Tax
// Exemptions" item), "inward supplies liable to reverse charge" needs the
// also-not-yet-built Reverse Charge Mechanism item, and "non-GST outward
// supplies" has no real category to source from — all four report as
// zero rather than an invented figure. ITC available reports everything
// under type "OTH" (regular purchases) since this codebase's Purchase
// Management doesn't distinguish imports (IMPG) or input service
// distributor credits (ISD) from ordinary domestic purchases — there's
// only ever been one kind of purchase GST Input posting
// (internal/purchase/bills.go's CreateBill).
func (h *Handler) GSTR3B(w http.ResponseWriter, r *http.Request) {
	claims, ok := authRequired(w, r)
	if !ok {
		return
	}
	_, start, end, mmyyyy, ok := periodParam(r)
	if !ok {
		invalidPeriod(w)
		return
	}

	var resp gstr3bResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		gstin, err := merchantGSTIN(ctx, tx)
		if err != nil {
			return err
		}
		resp.GSTIN = gstin

		var taxable, cgst, sgst, igst, cess float64
		if err := tx.QueryRow(ctx, `
			SELECT
			  COALESCE(SUM(sol.unit_price*sol.quantity - sol.discount_amount), 0),
			  COALESCE(SUM((sol.unit_price*sol.quantity - sol.discount_amount) * COALESCE(ts.cgst_rate,0) / 100), 0),
			  COALESCE(SUM((sol.unit_price*sol.quantity - sol.discount_amount) * COALESCE(ts.sgst_rate,0) / 100), 0),
			  COALESCE(SUM((sol.unit_price*sol.quantity - sol.discount_amount) * COALESCE(ts.igst_rate,0) / 100), 0),
			  COALESCE(SUM((sol.unit_price*sol.quantity - sol.discount_amount) * COALESCE(ts.cess_rate,0) / 100), 0)
			FROM sales_order_lines sol
			JOIN sales_orders so ON so.id = sol.sales_order_id
			JOIN product_variants pv ON pv.id = sol.variant_id
			JOIN products p ON p.id = pv.product_id
			LEFT JOIN tax_slabs ts ON ts.id = p.tax_slab_id
			WHERE so.status = 'finalized' AND so.finalized_at >= $1 AND so.finalized_at < $2`,
			start, end,
		).Scan(&taxable, &cgst, &sgst, &igst, &cess); err != nil {
			return err
		}

		var itcAvailable float64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(jl.debit - jl.credit), 0)
			FROM journal_lines jl
			JOIN journal_entries je ON je.id = jl.journal_entry_id
			JOIN chart_of_accounts coa ON coa.id = jl.account_id
			WHERE coa.code = $1 AND je.status = 'posted' AND je.entry_date >= $2 AND je.entry_date < $3`,
			accounting.AccountGSTInput, start, end,
		).Scan(&itcAvailable); err != nil {
			return err
		}

		ledgerGSTPayable, err := reconcileGSTPayable(ctx, tx, start, end)
		if err != nil {
			return err
		}

		totalOutwardTax := cgst + sgst + igst + cess
		resp.Period = start.Format("2006-01")
		resp.Summary = gstr3bSummary{
			Period: resp.Period, OutwardTaxable: formatMoney(taxable),
			CGST: formatMoney(cgst), SGST: formatMoney(sgst), IGST: formatMoney(igst), Cess: formatMoney(cess),
			TotalOutwardTax: formatMoney(totalOutwardTax), ITCAvailable: formatMoney(itcAvailable),
			NetTaxPayable: formatMoney(totalOutwardTax - itcAvailable),
		}
		resp.GSTR3BJSON = gstr3bJSON{GSTIN: gstin, RetPeriod: mmyyyy}
		resp.GSTR3BJSON.SupDetails.OutwardTaxable = taxBreakup{
			TaxableValue: formatMoney(taxable), IGST: formatMoney(igst), CGST: formatMoney(cgst), SGST: formatMoney(sgst), Cess: formatMoney(cess),
		}
		// internal/purchase's own GST Input posting (CreateBill) records
		// only one lump tax_total per bill, never split by CGST/SGST/IGST
		// the way sales tax IS split (via each line's own tax_slab rates)
		// — there is no real CGST/SGST/IGST breakdown of ITC to report.
		// Split it 50/50 CGST/SGST (0 IGST) as the honest, stated
		// assumption that every purchase is intrastate, consistent with
		// this codebase's existing tax model overall (IGST is never
		// actually populated anywhere else either — the seeded tax slab
		// only carries cgst_rate/sgst_rate).
		resp.GSTR3BJSON.ITCEligible.Available = []itcAvlEntry{
			{Type: "OTH", IGST: "0.00", CGST: formatMoney(itcAvailable / 2), SGST: formatMoney(itcAvailable / 2), Cess: "0.00"},
		}
		resp.Reconciliation = reconciliationInfo{
			ComputedGSTOutput: formatMoney(totalOutwardTax), LedgerGSTPayable: formatMoney(ledgerGSTPayable),
			Matches: within(totalOutwardTax, ledgerGSTPayable, 0.02),
		}
		resp.Disclaimer = schemaDisclaimer
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not compute GSTR-3B")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func within(a, b, epsilon float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= epsilon
}
