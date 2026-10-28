package expenses

// Importing a list of expenses never recorded (§3.77): a CSV (from the
// template, or a spreadsheet with the same headings), previewed row by row,
// then booked all together or not at all. Each row: date, head, amount,
// paid_by, and optionally description, vendor, reference and branch.
//
// paid_by is one of:
//   - cash
//   - bank — the default bank account (or Bank 1002 when there is none);
//     "bank: ICICI" or "bank: 2233" names one of the merchant's accounts
//   - a partner's name, or "partner: <name>" — it adds to their capital
//   - unpaid — Expenses Payable, to pay later from the expense list
//
// The same file can't be imported twice (its hash is the batch); a row
// that looks like one already recorded (same date, head and amount) is
// flagged.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

const maxImport = 1 << 20

var importColumns = []string{"date", "head", "amount", "paid_by", "description", "vendor", "reference", "branch"}

var importAliases = map[string]string{
	"expense_date": "date", "expense_head": "head", "category": "head", "type": "head", "expense": "head",
	"value": "amount", "amt": "amount", "paid_from": "paid_by", "payment": "paid_by", "paid": "paid_by", "mode": "paid_by",
	"details": "description", "narration": "description", "remarks": "description", "notes": "description",
	"party": "vendor", "paid_to": "vendor", "payee": "vendor", "bill_no": "reference", "ref": "reference", "invoice": "reference",
}

type importRequest struct {
	BranchID string `json:"branch_id"` // the default for rows with no branch
	CSV      string `json:"csv"`
}

type importRow struct {
	Row         int      `json:"row"`
	Date        string   `json:"date"`
	Head        string   `json:"head"`
	HeadID      string   `json:"head_id,omitempty"`
	Account     string   `json:"account,omitempty"` // "1300 Deposits & Advances"
	AccountType string   `json:"account_type,omitempty"`
	Amount      string   `json:"amount"`
	PaidBy      string   `json:"paid_by"`       // cash | bank | partner | unpaid
	PaidByLabel string   `json:"paid_by_label"` // "Bank – ICICI ••2233", "Partner Ravi"
	Branch      string   `json:"branch,omitempty"`
	Description string   `json:"description"`
	Vendor      string   `json:"vendor"`
	Reference   string   `json:"reference"`
	Warnings    []string `json:"warnings"`
	Errors      []string `json:"errors"`
	in          Input
}

type importPreview struct {
	Rows      []*importRow      `json:"rows"`
	Errors    []string          `json:"errors"`
	Ready     bool              `json:"ready"`
	Count     int               `json:"count"`
	Total     string            `json:"total"`
	ByHead    map[string]string `json:"by_head"`
	ByPayer   map[string]string `json:"by_payer"`
	ToAssets  string            `json:"to_assets"`   // posted to asset accounts (deposits, fixtures)
	ToExpense string            `json:"to_expenses"` // posted to expense accounts (profit and loss)
	Batch     string            `json:"batch"`
}

var (
	spaceRun   = regexp.MustCompile(`\s+`)
	numberJunk = regexp.MustCompile(`[₹,\s]|(?i)rs\.?|(?i)inr`)
)

func norm(s string) string {
	return strings.ToLower(spaceRun.ReplaceAllString(strings.TrimSpace(s), " "))
}

func parseAmount(s string) (float64, bool) {
	s = numberJunk.ReplaceAllString(strings.TrimSpace(s), "")
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

// parseDate accepts 2026-04-15, 15/04/2026, 15-04-2026 and 15.04.2026.
func parseDate(s string) (string, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02", "02/01/2006", "2/1/2006", "02-01-2006", "2-1-2006", "02.01.2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02"), true
		}
	}
	return "", false
}

type lookups struct {
	heads    map[string]head
	headList []string
	branches map[string]string // name or code → id
	brName   map[string]string
	banks    []struct{ id, label, match string }
	defBank  string
	partners map[string]struct{ id, name, status string }
	today    string
}

func loadLookups(ctx context.Context, tx pgx.Tx, today string) (*lookups, error) {
	lk := &lookups{heads: map[string]head{}, branches: map[string]string{}, brName: map[string]string{},
		partners: map[string]struct{ id, name, status string }{}, today: today}
	heads, err := loadHeads(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, h := range heads {
		if h.Status == "active" {
			lk.heads[norm(h.Name)] = h
			lk.headList = append(lk.headList, h.Name)
		}
	}
	rows, err := tx.Query(ctx, `SELECT id::text, name, COALESCE(code, '') FROM branches WHERE status = 'active' AND app_branch_visible(id)`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, name, code string
		if err := rows.Scan(&id, &name, &code); err != nil {
			rows.Close()
			return nil, err
		}
		lk.branches[norm(name)], lk.brName[id] = id, name
		if code != "" {
			lk.branches[norm(code)] = id
		}
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT id::text, bank_name || ' ••' || right(account_number, 4), lower(bank_name || ' ' || account_name || ' ' || account_number), is_default
		FROM bank_accounts WHERE status = 'active' ORDER BY is_default DESC, created_at`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var b struct{ id, label, match string }
		var def bool
		if err := rows.Scan(&b.id, &b.label, &b.match, &def); err != nil {
			rows.Close()
			return nil, err
		}
		if def && lk.defBank == "" {
			lk.defBank = b.id
		}
		lk.banks = append(lk.banks, b)
	}
	rows.Close()
	if lk.defBank == "" && len(lk.banks) == 1 {
		lk.defBank = lk.banks[0].id
	}
	rows, err = tx.Query(ctx, `SELECT id::text, name, status FROM partners`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p struct{ id, name, status string }
		if err := rows.Scan(&p.id, &p.name, &p.status); err != nil {
			return nil, err
		}
		lk.partners[norm(p.name)] = p
	}
	return lk, rows.Err()
}

func (lk *lookups) bankLabel(id string) string {
	for _, b := range lk.banks {
		if b.id == id {
			return "Bank – " + b.label
		}
	}
	return "Bank"
}

func parseCSV(text string) ([]map[string]string, []int, []string) {
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	recs, err := r.ReadAll()
	if err != nil {
		return nil, nil, []string{"the file isn't valid CSV: " + err.Error()}
	}
	if len(recs) < 2 {
		return nil, nil, []string{"the file has no expense rows"}
	}
	header := make([]string, len(recs[0]))
	seen := map[string]bool{}
	for i, h := range recs[0] {
		k := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))), " ", "_")
		if a, ok := importAliases[k]; ok {
			k = a
		}
		header[i], seen[k] = k, true
	}
	var missing []string
	for _, c := range []string{"date", "head", "amount", "paid_by"} {
		if !seen[c] {
			missing = append(missing, "the file has no "+c+" column — download the template for the headings")
		}
	}
	if missing != nil {
		return nil, nil, missing
	}
	var rows []map[string]string
	var nums []int
	for i, rec := range recs[1:] {
		row, blank := map[string]string{}, true
		for j, v := range rec {
			if j < len(header) {
				row[header[j]] = strings.TrimSpace(v)
				if strings.TrimSpace(v) != "" {
					blank = false
				}
			}
		}
		if !blank {
			rows, nums = append(rows, row), append(nums, i+2)
		}
	}
	return rows, nums, nil
}

func resolve(ctx context.Context, tx pgx.Tx, req importRequest, today string) (*importPreview, error) {
	p := &importPreview{Rows: []*importRow{}, Errors: []string{}, ByHead: map[string]string{}, ByPayer: map[string]string{}}
	sum := sha256.Sum256([]byte(req.CSV))
	p.Batch = "file:" + hex.EncodeToString(sum[:8])
	if len(req.CSV) > maxImport {
		p.Errors = append(p.Errors, "the file is larger than 1 MB — split it")
		return p, nil
	}
	var done bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM expenses WHERE import_batch = $1)`, p.Batch).Scan(&done); err != nil {
		return nil, err
	}
	if done {
		p.Errors = append(p.Errors, "this file has already been imported")
	}
	rows, nums, errs := parseCSV(req.CSV)
	p.Errors = append(p.Errors, errs...)
	if rows == nil {
		return p, nil
	}
	lk, err := loadLookups(ctx, tx, today)
	if err != nil {
		return nil, err
	}
	var total, assets, expensesTotal float64
	byHead, byPayer := map[string]float64{}, map[string]float64{}
	for i, c := range rows {
		row := &importRow{Row: nums[i], Head: c["head"], PaidBy: "", Description: c["description"], Vendor: c["vendor"], Reference: c["reference"],
			Warnings: []string{}, Errors: []string{}}
		p.Rows = append(p.Rows, row)
		bad := func(f string, a ...any) { row.Errors = append(row.Errors, fmt.Sprintf(f, a...)) }
		in := Input{Description: c["description"], Vendor: c["vendor"], Reference: c["reference"], BranchID: req.BranchID, batch: p.Batch}

		if d, ok := parseDate(c["date"]); !ok {
			bad("date %q isn't a date (use 2026-04-15 or 15/04/2026)", c["date"])
		} else if d > today {
			bad("the date is in the future")
		} else {
			row.Date, in.Date = d, d
		}
		if h, ok := lk.heads[norm(c["head"])]; ok {
			row.Head, row.HeadID, row.Account, row.AccountType = h.Name, h.HeadID, h.AccountCode+" "+h.AccountName, h.AccountType
			in.HeadID = h.HeadID
		} else {
			bad("unknown expense head %q — use one of: %s (or add it under Expense Heads)", c["head"], strings.Join(lk.headList, ", "))
		}
		if v, ok := parseAmount(c["amount"]); !ok || v <= 0 {
			bad("amount %q must be a number above 0", c["amount"])
		} else {
			in.Amount = v
			row.Amount = fmt.Sprintf("%.2f", v)
		}
		if b := c["branch"]; b != "" {
			if id, ok := lk.branches[norm(b)]; ok {
				in.BranchID = id
			} else {
				bad("unknown branch %q", b)
			}
		}
		if in.BranchID == "" {
			bad("choose the branch (or put one in the branch column)")
		} else {
			row.Branch = lk.brName[in.BranchID]
		}

		raw := strings.TrimSpace(c["paid_by"])
		kind, rest, _ := strings.Cut(raw, ":")
		kind, rest = norm(kind), strings.TrimSpace(rest)
		switch {
		case kind == "cash":
			in.PaidBy, row.PaidByLabel = "cash", "Cash"
		case kind == "bank":
			in.PaidBy = "bank"
			if rest == "" {
				in.BankAccountID = lk.defBank
			} else {
				var hits []string
				for _, b := range lk.banks {
					if strings.Contains(b.match, strings.ToLower(rest)) {
						hits = append(hits, b.id)
					}
				}
				switch len(hits) {
				case 1:
					in.BankAccountID = hits[0]
				case 0:
					bad("no bank account matches %q", rest)
				default:
					bad("more than one bank account matches %q — use more of its name or its last digits", rest)
				}
			}
			row.PaidByLabel = lk.bankLabel(in.BankAccountID)
		case kind == "unpaid" || kind == "credit" || kind == "payable" || kind == "due":
			in.PaidBy, row.PaidByLabel = "unpaid", "Unpaid (Expenses Payable)"
		default:
			name := raw
			if kind == "partner" {
				name = rest
			}
			if pt, ok := lk.partners[norm(name)]; ok {
				if pt.status != "active" {
					bad("partner %s has retired", pt.name)
				}
				in.PaidBy, in.PartnerID, row.PaidByLabel = "partner", pt.id, "Partner "+pt.name
			} else if raw == "" {
				bad("paid_by is empty — cash, bank, a partner's name or unpaid")
			} else {
				bad("paid_by %q isn't cash, bank, unpaid or a partner's name (add the partner under Partners first)", raw)
			}
		}
		row.PaidBy = in.PaidBy

		if len(row.Errors) == 0 {
			var dup bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM expenses WHERE expense_date = $1::date AND head_id::text = $2 AND amount = $3 AND status = 'posted')`,
				in.Date, in.HeadID, in.Amount).Scan(&dup); err != nil {
				return nil, err
			}
			if dup {
				row.Warnings = append(row.Warnings, "an expense with this date, head and amount is already recorded — check it isn't the same one")
			}
			total += in.Amount
			byHead[row.Head] += in.Amount
			byPayer[row.PaidByLabel] += in.Amount
			if row.AccountType == "asset" {
				assets += in.Amount
			} else {
				expensesTotal += in.Amount
			}
		}
		row.in = in
	}
	for k, v := range byHead {
		p.ByHead[k] = fmt.Sprintf("%.2f", v)
	}
	for k, v := range byPayer {
		p.ByPayer[k] = fmt.Sprintf("%.2f", v)
	}
	p.Count, p.Total, p.ToAssets, p.ToExpense = len(p.Rows), fmt.Sprintf("%.2f", total), fmt.Sprintf("%.2f", assets), fmt.Sprintf("%.2f", expensesTotal)
	p.Ready = len(p.Errors) == 0
	for _, r := range p.Rows {
		if len(r.Errors) > 0 {
			p.Ready = false
		}
	}
	return p, nil
}

func decode(w http.ResponseWriter, r *http.Request) (importRequest, bool) {
	var req importRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxImport+64<<10)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return req, false
	}
	return req, true
}

// PreviewImport: POST /expenses/import/preview (expenses.manage) — what
// the import would book, row by row; nothing is saved.
func (h *Handler) PreviewImport(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	req, ok := decode(w, r)
	if !ok {
		return
	}
	t := today(r, h, claims.TenantID)
	var p *importPreview
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		p, err = resolve(ctx, tx, req, t)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not read the file")
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

// CommitImport: POST /expenses/import/commit (expenses.manage) — books
// every row, or nothing (an error anywhere refuses with the preview).
func (h *Handler) CommitImport(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	req, ok := decode(w, r)
	if !ok {
		return
	}
	t := today(r, h, claims.TenantID)
	var p *importPreview
	var failRow string
	created := 0
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if p, err = resolve(ctx, tx, req, t); err != nil || !p.Ready {
			return err
		}
		for _, row := range p.Rows {
			if _, err := Create(ctx, tx, row.in, claims.UserID); err != nil {
				failRow = fmt.Sprintf("row %d", row.Row)
				return err
			}
			created++
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "expense_import", EntityID: claims.UserID, Action: "create", PerformedBy: claims.UserID,
			Reason: p.Batch, After: map[string]any{"rows": created, "total": p.Total, "to_assets": p.ToAssets, "to_expenses": p.ToExpense, "by_payer": p.ByPayer}})
	})
	switch {
	case err == nil && p != nil && !p.Ready:
		httpx.ErrorWithDetails(w, http.StatusBadRequest, "IMPORT_INVALID", "the file has errors — fix them and preview again", map[string]any{"preview": p})
	case err != nil && failRow != "":
		respond(w, fmt.Errorf("%w (%s)", err, failRow), "could not import the expenses — nothing was saved")
	case respond(w, err, "could not import the expenses — nothing was saved"):
	default:
		httpx.JSON(w, http.StatusCreated, map[string]any{"imported": created, "total": p.Total, "batch": p.Batch, "to_assets": p.ToAssets, "to_expenses": p.ToExpense})
	}
}

// ImportTemplate: GET /expenses/import/template — the headings and a few
// example rows.
func (h *Handler) ImportTemplate(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	buf.WriteString("\ufeff")
	cw := csv.NewWriter(&buf)
	_ = cw.Write(importColumns)
	for _, row := range [][]string{
		{"2026-04-01", "Shop Advance", "200000", "partner: Ravi", "Advance to landlord", "Sri Lakshmi Estates", "Agreement 12", ""},
		{"2026-04-05", "Interior", "185000", "bank", "Racks and counter", "Wood Works", "INV-2231", ""},
		{"2026-04-10", "Flex & Banner", "8500", "cash", "Opening banner", "Star Prints", "", ""},
		{"2026-04-30", "Shop Rent", "35000", "unpaid", "April rent", "Sri Lakshmi Estates", "", ""},
		{"2026-04-12", "Transport", "2400", "cash", "Goods from Chennai", "", "LR 5512", ""},
	} {
		_ = cw.Write(row)
	}
	cw.Flush()
	w.Header().Set("Content-Disposition", `attachment; filename="expenses-template.csv"`)
	httpx.Binary(w, http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
}
