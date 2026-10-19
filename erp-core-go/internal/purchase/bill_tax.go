package purchase

import (
	"context"
	"errors"
	"math"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/taxes"
)

// A purchase bill's tax is the supplier invoice's: CGST + SGST from a
// supplier in the branch's state, IGST from one in another
// (migrations/060). The amounts are stored on the bill, always adding up
// to tax_total; GSTR-3B reads them as input-tax credit by head.

var errTaxSplitMismatch = errors.New("cgst + sgst + igst + cess must equal tax_total")

// resolveBillTax settles the request's split inside the bill's
// transaction. Amounts given must add up to tax_total (or set it, when
// tax_total is 0); else inter_state, when given, says how tax_total
// splits; else the supplier's GSTIN state against the branch's decides
// (intra-state when either is unknown).
func resolveBillTax(ctx context.Context, tx pgx.Tx, req *createBillRequest, branchID, supplierID string) (taxes.Amounts, bool, error) {
	given := req.CGSTAmount != nil || req.SGSTAmount != nil || req.IGSTAmount != nil || req.CessAmount != nil
	if given {
		a := taxes.Amounts{CGST: deref0(req.CGSTAmount), SGST: deref0(req.SGSTAmount), IGST: deref0(req.IGSTAmount), Cess: deref0(req.CessAmount)}
		sum := round2(a.CGST + a.SGST + a.IGST + a.Cess)
		if req.TaxTotal == 0 {
			req.TaxTotal = sum
		} else if math.Abs(sum-req.TaxTotal) > 0.004 {
			return a, false, errTaxSplitMismatch
		}
		return a, a.IGST > 0, nil
	}
	if req.InterState != nil {
		return taxes.SplitTotal(req.TaxTotal, 0, *req.InterState), *req.InterState, nil
	}
	var supplierState, branchState string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(gst_state_of(s.gstin), ''), COALESCE(app_branch_state($1::uuid), '')
		FROM suppliers s WHERE s.id::text = $2`, branchID, supplierID).Scan(&supplierState, &branchState); err != nil {
		return taxes.Amounts{}, false, err
	}
	inter := supplierState != "" && branchState != "" && supplierState != branchState
	return taxes.SplitTotal(req.TaxTotal, 0, inter), inter, nil
}

func deref0(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

// billTaxRows: the bill's tax as its branch prints it.
func billTaxRows(s taxes.Settings, cgst, sgst, igst, cess, total string) []taxes.Row {
	if !s.Split() {
		return []taxes.Row{{Label: s.TaxLabel, Amount: total}}
	}
	var rows []taxes.Row
	if !taxes.IsZero(cgst) || !taxes.IsZero(sgst) || taxes.IsZero(igst) {
		rows = append(rows, taxes.Row{Label: "CGST", Amount: cgst}, taxes.Row{Label: "SGST", Amount: sgst})
	}
	if !taxes.IsZero(igst) {
		rows = append(rows, taxes.Row{Label: "IGST", Amount: igst})
	}
	if !taxes.IsZero(cess) {
		rows = append(rows, taxes.Row{Label: "Cess", Amount: cess})
	}
	return rows
}

// fillBillTax adds the split to a bill response.
func fillBillTax(ctx context.Context, tx pgx.Tx, resp *billResponse) error {
	var branchID string
	if err := tx.QueryRow(ctx, `
		SELECT branch_id::text, cgst_amount::text, sgst_amount::text, igst_amount::text, cess_amount::text, inter_state
		FROM purchase_bills WHERE id::text = $1`, resp.BillID,
	).Scan(&branchID, &resp.CGSTAmount, &resp.SGSTAmount, &resp.IGSTAmount, &resp.CessAmount, &resp.InterState); err != nil {
		return err
	}
	s, err := taxes.Load(ctx, tx, branchID)
	if err != nil {
		return err
	}
	resp.TaxRows = billTaxRows(s, resp.CGSTAmount, resp.SGSTAmount, resp.IGSTAmount, resp.CessAmount, resp.TaxTotal)
	return nil
}
