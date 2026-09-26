package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/reports"
)

// NLP-BI: "ask a question, get a chart" against this merchant's own
// Reporting data — phased_roadmap.md Phase 6 item 3. The one hard
// constraint driving this whole file's design: the LLM NEVER generates
// SQL and never sees this tenant's raw transaction rows. It only
// classifies a natural-language question into one of a small, fixed set
// of report intents already implemented in internal/reports, plus
// extracts parameters that are then independently validated against real
// data — a branch is picked by NUMBER off a list built fresh from this
// tenant's own database (see classify's doc comment for why: a real,
// live Ollama model consistently mis-transcribed branch UUIDs, but picking
// a number 1..N off a short list is reliable), and an out-of-range number
// resolves to "no branch," never a guess. The Go backend calls the exact
// same tenant-scoped Build* functions GET /reports/* already uses — a
// local/self-hosted or third-party model only ever sees a question, a
// numbered branch-name list, and (in the second, optional call)
// already-aggregated numbers that endpoint would have returned to any
// authenticated user anyway.

// supportedIntents is the fixed whitelist the classifier must choose
// from. "unsupported" is always a valid choice too — the model saying "I
// don't know how to answer that" is the correct, safe answer for anything
// outside this list, not a reason to guess.
var supportedIntents = map[string]bool{
	"daily_sales":        true,
	"stock_summary":      true,
	"eod_cash":           true,
	"consolidated_sales": true,
	"consolidated_stock": true,
	"unsupported":        true,
}

type nlpbiClassification struct {
	Intent       string `json:"intent"`
	BranchIndex  int    `json:"branch_index"` // 1-based index into the branch list given in the prompt, 0 = none/all — see classify's doc comment for why this isn't a branch_id
	Date         string `json:"date"`
	ProductQuery string `json:"product_query"`
}

type chartPoint struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type chartSpec struct {
	Type   string       `json:"type"` // "bar" | "stat"
	Title  string       `json:"title"`
	Series []chartPoint `json:"series,omitempty"`
	Value  string       `json:"value,omitempty"` // only for type "stat"
}

type askRequest struct {
	Question string `json:"question"`
}

type askResponse struct {
	Question string      `json:"question"`
	Intent   string      `json:"intent"`
	Answer   string      `json:"answer"`
	Chart    chartSpec   `json:"chart"`
	Data     interface{} `json:"data"`
}

// requireLLM answers 503 LLM_UNAVAILABLE and returns false if no provider
// is configured — the same graceful-disable shape internal/search uses
// for a missing OpenSearchURL, so a deployment that hasn't set up an LLM
// yet doesn't fail to start, it just can't serve these two endpoints.
func (h *Handler) requireLLM(w http.ResponseWriter) bool {
	if h.LLM == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "LLM_UNAVAILABLE", "no LLM provider is configured (set LLM_PROVIDER=ollama or LLM_PROVIDER=anthropic)")
		return false
	}
	return true
}

// Ask: POST /ai/ask — body {"question": "..."}.
func (h *Handler) Ask(w http.ResponseWriter, r *http.Request) {
	if !h.requireLLM(w) {
		return
	}
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req askRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Question) == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "question is required")
		return
	}

	var resp askResponse
	resp.Question = req.Question
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		branches, err := loadBranchList(ctx, tx)
		if err != nil {
			return err
		}

		classification, rawErr := h.classify(ctx, req.Question, branches)
		if rawErr != nil {
			return &llmCallError{rawErr}
		}

		if !supportedIntents[classification.Intent] || classification.Intent == "unsupported" {
			return errUnsupportedIntent
		}

		// Resolve the branch in Go first, by a plain case-insensitive
		// substring match of a real branch name against the actual question
		// text — found live (against a real Ollama-hosted 3B model) that
		// branch_index itself is unreliable: it correctly resolved the 1st
		// and 2nd branches in a 3-branch list but consistently returned the
		// 2nd branch's index for the 3rd branch's name, every time, across
		// repeated identical requests. A deterministic string match on the
		// one thing we actually have — the merchant's own branch names — is
		// far more reliable than trusting a small model's list-position
		// counting, so it's tried first; branch_index is only a fallback for
		// phrasing that doesn't literally contain the branch name.
		branchID := resolveBranchByName(req.Question, branches)
		if branchID == "" && classification.BranchIndex >= 1 && classification.BranchIndex <= len(branches) {
			branchID = branches[classification.BranchIndex-1].ID
		}
		date := classification.Date
		if date == "" || !reports.IsValidDate(date) {
			date = time.Now().Format("2006-01-02")
		}

		resp.Intent = classification.Intent
		data, chart, buildErr := h.buildReport(ctx, tx, classification.Intent, branchID, date, classification.ProductQuery)
		if buildErr != nil {
			return buildErr
		}
		resp.Data = data
		resp.Chart = chart

		// The natural-language summary is best-effort narration over
		// already-computed, already-safe numbers — if this second call
		// fails (model down, malformed output), the request still
		// succeeds with real data, just without prose on top of it.
		if summary, sumErr := h.summarize(ctx, req.Question, classification.Intent, data); sumErr == nil {
			resp.Answer = summary
		}
		return nil
	})
	var llmErr *llmCallError
	switch {
	case err == nil:
		// fall through to success response below
	case err == errUnsupportedIntent:
		httpx.Error(w, http.StatusUnprocessableEntity, "AI_INTENT_UNRECOGNIZED",
			"this question isn't one of the report types I can answer yet (daily sales, stock summary, EOD cash, consolidated sales, consolidated stock)")
		return
	case err == errBranchRequired:
		httpx.Error(w, http.StatusUnprocessableEntity, "AI_BRANCH_REQUIRED", errBranchRequired.Error())
		return
	case errors.As(err, &llmErr):
		httpx.Error(w, http.StatusBadGateway, "LLM_ERROR", "could not get an answer from the configured LLM provider: "+llmErr.Error())
		return
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build the report for this question")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

var errUnsupportedIntent = &unsupportedIntentError{}

type unsupportedIntentError struct{}

func (e *unsupportedIntentError) Error() string { return "unsupported intent" }

// llmCallError distinguishes "the LLM provider call itself failed"
// (network error, provider down, bad API key) from a real database error
// building the report — the two need different HTTP status codes/error
// codes, and both can occur in the same WithTenant closure.
type llmCallError struct{ err error }

func (e *llmCallError) Error() string { return e.err.Error() }
func (e *llmCallError) Unwrap() error { return e.err }

type branchRef struct {
	ID   string
	Name string
}

// resolveBranchByName finds the one branch whose name appears verbatim
// (case-insensitively) in the question text. Returns "" on zero or
// multiple matches — never guesses between two branches that both happen
// to be named in the question, and never picks a branch nobody actually
// asked about.
func resolveBranchByName(question string, branches []branchRef) string {
	q := strings.ToLower(question)
	var matchID string
	matches := 0
	for _, b := range branches {
		if strings.Contains(q, strings.ToLower(b.Name)) {
			matches++
			matchID = b.ID
		}
	}
	if matches == 1 {
		return matchID
	}
	return ""
}

func loadBranchList(ctx context.Context, tx pgx.Tx) ([]branchRef, error) {
	rows, err := tx.Query(ctx, `SELECT id::text, name FROM branches ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []branchRef
	for rows.Next() {
		var b branchRef
		if err := rows.Scan(&b.ID, &b.Name); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// classify asks the LLM to pick a branch by NUMBER, never by copying a
// UUID: found live against a real Ollama-hosted 3B model, which — even
// given the exact branch id/name list — consistently returned the wrong
// id for "MG Road" (Koramangala's id, not MG Road's), every time, across
// repeated identical requests. Small local models are unreliable at
// transcribing long random strings; they're much more reliable picking a
// number 1..N off a short list. Go then indexes into the exact same
// ordered `branches` slice used to build the prompt, so branch_index=2
// always means "the second branch in this list," never a value that
// could reference the wrong tenant's data.
func (h *Handler) classify(ctx context.Context, question string, branches []branchRef) (nlpbiClassification, error) {
	var branchLines strings.Builder
	for i, b := range branches {
		branchLines.WriteString(strconv.Itoa(i+1) + ". " + b.Name + "\n")
	}

	system := `You are a strict intent classifier for a retail ERP's reporting API. You never invent data and never write SQL.
Given a merchant employee's question, respond with ONLY a single JSON object (no markdown, no prose, no code fences) matching exactly this shape:
{"intent": "<one of: daily_sales, stock_summary, eod_cash, consolidated_sales, consolidated_stock, unsupported>", "branch_index": <a NUMBER from the list below, or 0 if no specific branch is named or needed>, "date": "<YYYY-MM-DD, or empty string for today>", "product_query": "<a product name/keyword mentioned, or empty string>"}

Intent meanings:
- daily_sales: sales totals for ONE specific branch on one day (needs branch_index)
- stock_summary: current stock levels for ONE specific branch (needs branch_index)
- eod_cash: cash collected for ONE specific branch on one day (needs branch_index)
- consolidated_sales: sales totals across ALL branches on one day (no branch needed, branch_index=0)
- consolidated_stock: stock levels across ALL branches, optionally for one product (product_query optional, branch_index=0)
- unsupported: anything not covered by the above (e.g. questions about customers, employees, forecasts, prices, or anything not in this list) — do not guess an intent for these

Today's date is ` + time.Now().Format("2006-01-02") + `. Resolve relative dates ("yesterday", "today", "last Monday") to an absolute YYYY-MM-DD yourself.

This merchant's branches, numbered (branch_index must be one of these numbers, or 0):
` + branchLines.String() + `
Respond with the JSON object only.`

	raw, err := h.LLM.Complete(ctx, system, question)
	if err != nil {
		return nlpbiClassification{}, err
	}
	jsonStr, ok := extractJSONObject(raw)
	if !ok {
		return nlpbiClassification{Intent: "unsupported"}, nil
	}
	var c nlpbiClassification
	if err := json.Unmarshal([]byte(jsonStr), &c); err != nil {
		return nlpbiClassification{Intent: "unsupported"}, nil
	}
	return c, nil
}

// extractJSONObject pulls out the first balanced {...} substring, so a
// model that wraps its JSON in ```json fences or a sentence of preamble
// (small local models routinely do this despite instructions not to)
// still parses, rather than failing the whole request on a formatting
// quirk unrelated to whether the classification itself was reasonable.
func extractJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start == -1 {
		return "", false
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

func (h *Handler) buildReport(ctx context.Context, tx pgx.Tx, intent, branchID, date, productQuery string) (interface{}, chartSpec, error) {
	switch intent {
	case "daily_sales":
		if branchID == "" {
			return nil, chartSpec{}, errBranchRequired
		}
		data, err := reports.BuildDailySales(ctx, tx, branchID, date)
		if err != nil {
			return nil, chartSpec{}, err
		}
		var series []chartPoint
		for _, p := range data.ByPaymentMethod {
			series = append(series, chartPoint{Label: p.Method, Value: p.Amount})
		}
		return data, chartSpec{Type: "bar", Title: "Sales by payment method — " + date, Series: series}, nil

	case "stock_summary":
		if branchID == "" {
			return nil, chartSpec{}, errBranchRequired
		}
		lines, err := reports.BuildStockSummary(ctx, tx, branchID)
		if err != nil {
			return nil, chartSpec{}, err
		}
		series := stockLinesToChart(lines)
		return map[string]any{"branch_id": branchID, "lines": lines}, chartSpec{Type: "bar", Title: "Available stock by product", Series: series}, nil

	case "eod_cash":
		if branchID == "" {
			return nil, chartSpec{}, errBranchRequired
		}
		data, err := reports.BuildEODCash(ctx, tx, branchID, date)
		if err != nil {
			return nil, chartSpec{}, err
		}
		return data, chartSpec{Type: "stat", Title: "Cash collected — " + date, Value: data.CashTotal}, nil

	case "consolidated_sales":
		data, err := reports.BuildConsolidatedSales(ctx, tx, date)
		if err != nil {
			return nil, chartSpec{}, err
		}
		var series []chartPoint
		for _, b := range data.Branches {
			series = append(series, chartPoint{Label: b.BranchName, Value: b.GrandTotal})
		}
		return data, chartSpec{Type: "bar", Title: "Sales by branch — " + date, Series: series}, nil

	case "consolidated_stock":
		variantID := ""
		if productQuery != "" {
			variantID = resolveSingleVariant(ctx, tx, productQuery)
		}
		entries, err := reports.BuildConsolidatedStock(ctx, tx, variantID)
		if err != nil {
			return nil, chartSpec{}, err
		}
		var series []chartPoint
		for i, e := range entries {
			if i >= 20 {
				break
			}
			series = append(series, chartPoint{Label: e.Product + " (" + e.SKU + ")", Value: e.TotalOnHand})
		}
		return map[string]any{"entries": entries}, chartSpec{Type: "bar", Title: "Total stock on hand by product", Series: series}, nil

	default:
		return nil, chartSpec{}, errUnsupportedIntent
	}
}

var errBranchRequired = &branchRequiredError{}

type branchRequiredError struct{}

func (e *branchRequiredError) Error() string {
	return "this question needs a specific branch — mention which branch you mean"
}

func stockLinesToChart(lines []reports.StockSummaryLine) []chartPoint {
	var series []chartPoint
	for i, l := range lines {
		if i >= 20 {
			break
		}
		series = append(series, chartPoint{Label: l.Product, Value: l.Available})
	}
	return series
}

// resolveSingleVariant does a plain ILIKE lookup, never a guess: it only
// returns a variant_id when exactly one product name matches, so an
// ambiguous or unmatched product name falls back to "all products" (the
// same behavior GET /reports/consolidated-stock already has with no
// variant_id) rather than silently picking the wrong item.
func resolveSingleVariant(ctx context.Context, tx pgx.Tx, productQuery string) string {
	rows, err := tx.Query(ctx, `
		SELECT pv.id::text FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE p.name ILIKE '%' || $1 || '%' OR pv.sku ILIKE '%' || $1 || '%'
		LIMIT 2`, productQuery)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 1 {
		return ids[0]
	}
	return ""
}

func (h *Handler) summarize(ctx context.Context, question, intent string, data interface{}) (string, error) {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	system := `You are a retail merchant's reporting assistant. You are given a question, which report intent it maps to, and the already-computed report data (all numbers are real and final — you never recompute or second-guess them). Reply with a plain 1-3 sentence answer in plain English, no markdown, no JSON. If the data shows zero activity, say so plainly.`
	user := "Question: " + question + "\nIntent: " + intent + "\nData: " + string(dataJSON)
	text, err := h.LLM.Complete(ctx, system, user)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}
