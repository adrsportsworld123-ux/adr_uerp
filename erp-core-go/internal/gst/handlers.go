// Package gst implements Phase 4's GST Returns export sub-area
// (phased_roadmap.md; pos_frd_complete.md §4: "GSTR-1: Monthly outward
// supplies report," "GSTR-3B: Monthly summary with tax payment," "Export
// JSON for GST portal upload," "Reconciliation tool"). Sequenced ahead of
// this phase's other still-open sub-areas (E-Invoicing/E-Way Bill, Bank
// Reconciliation) because it's the one piece of Phase 4 that's genuinely
// complete on its own: the FRD's own wording is "export JSON for GST
// portal upload," i.e. a merchant uploads this file themselves — unlike
// e-invoicing (needs a live GSP call to a real Invoice Registration
// Portal to get a real IRN) or e-way bill (same), there is no vendor
// integration this needs and none this codebase can only stand in for.
//
// Every number in the responses below is derived from this codebase's own
// already-posted data (sales_order_lines' stored tax_amount, re-split by
// each line's own tax_slab rates; journal_lines for ITC and the
// reconciliation check) — nothing here is invented or estimated. What IS
// an honest limitation: the exact GSTN-published JSON schema field names
// (gstin/fp/b2b/ctin/inv/itm_det/pos/hsn_sc/uqc, etc., in gstr1_json and
// gstr3b_json below) are reproduced from public documentation at a
// structural level, not verified against a live GST portal schema
// validator or a real GSP in this environment — see each handler's
// `disclaimer` response field, and validate a real filing through the GST
// portal's own offline utility before relying on this for an actual
// return.
package gst

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB *db.DB
}

const schemaDisclaimer = "gstr1_json/gstr3b_json are structured per GSTN's publicly documented JSON schema at a structural level. " +
	"They have not been validated against the live GST portal or a GSP's schema validator in this environment — " +
	"validate a real filing through the GST portal's offline utility before relying on this for an actual return. " +
	"The underlying totals (summary/reconciliation) are computed directly from this merchant's own posted sales and " +
	"ledger data and carry no such caveat."

// parsePeriod turns "YYYY-MM" into the month's [start, end) date bounds
// (end exclusive) and the GSTN JSON schema's "MMYYYY" period string.
func parsePeriod(period string) (start, end time.Time, mmyyyy string, ok bool) {
	t, err := time.Parse("2006-01", period)
	if err != nil {
		return time.Time{}, time.Time{}, "", false
	}
	start = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	end = start.AddDate(0, 1, 0)
	mmyyyy = start.Format("012006")
	return start, end, mmyyyy, true
}

func periodParam(r *http.Request) (string, time.Time, time.Time, string, bool) {
	period := r.URL.Query().Get("period")
	start, end, mmyyyy, ok := parsePeriod(period)
	return period, start, end, mmyyyy, ok
}

// stateCode is a GSTIN's first two characters — the GST state code, and
// literally what the GSTN schema's "pos" (place of supply) field expects
// as a raw value, not a display name. No lookup table needed or risked.
func stateCode(gstin string) string {
	if len(gstin) < 2 {
		return ""
	}
	return gstin[:2]
}

func merchantGSTIN(ctx context.Context, tx pgx.Tx) (string, error) {
	var g string
	err := tx.QueryRow(ctx, `SELECT COALESCE(gstin,'') FROM merchants WHERE id = current_setting('app.tenant_id')::uuid`).Scan(&g)
	return g, err
}

// reconcileGSTPayable compares GSTR-1/3B's own computed outward-tax total
// against what was actually posted to the GST Payable account for the
// same period — the FRD's "Reconciliation tool," in the one form that's
// honestly buildable without inventing a comparison target: this
// system's own return-computation logic checked against its own ledger,
// not against the GST portal (which this environment can't reach).
func reconcileGSTPayable(ctx context.Context, tx pgx.Tx, start, end time.Time) (float64, error) {
	var total float64
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(jl.credit - jl.debit), 0)
		FROM journal_lines jl
		JOIN journal_entries je ON je.id = jl.journal_entry_id
		JOIN chart_of_accounts coa ON coa.id = jl.account_id
		WHERE coa.code = $1 AND je.status = 'posted' AND je.entry_date >= $2 AND je.entry_date < $3`,
		accounting.AccountGSTPayable, start, end,
	).Scan(&total)
	return total, err
}

func formatMoney(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func invalidPeriod(w http.ResponseWriter) {
	httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "period is required, format YYYY-MM")
}

func authRequired(w http.ResponseWriter, r *http.Request) (*authn.Claims, bool) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return nil, false
	}
	return claims, true
}
