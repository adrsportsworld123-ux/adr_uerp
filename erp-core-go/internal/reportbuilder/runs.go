package reportbuilder

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/locale"
	"erp-core-go/internal/subscriptions"
)

// Exports run in the background (phased_roadmap.md Phase 16: "Heavy
// reports run as background jobs"): up to 100,000 rows and two minutes of
// database time, two at a time per API process. A run re-resolves the
// requester's branch scope and plan when it executes, so a scope or plan
// change made after it was queued still applies, and it fails if the
// requester has been deactivated.

const (
	exportTimeout = 2 * time.Minute
	maxConcurrent = 2
)

type Runner struct {
	DB  *db.DB
	sem chan struct{}
}

func NewRunner(database *db.DB) *Runner {
	return &Runner{DB: database, sem: make(chan struct{}, maxConcurrent)}
}

// Enqueue runs one queued run now (if a slot is free) in the background.
func (rn *Runner) Enqueue(merchantID, runID string) {
	go func() {
		rn.sem <- struct{}{}
		defer func() { <-rn.sem }()
		rn.execute(context.Background(), merchantID, runID)
	}()
}

// Run picks up runs left queued (an API restart, or a full runner) and
// re-queues ones stuck running for over ten minutes.
func (rn *Runner) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		rn.sweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (rn *Runner) sweep(ctx context.Context) {
	rows, err := rn.DB.Pool.Query(ctx, `SELECT id FROM merchants WHERE status = 'active'`)
	if err != nil {
		return
	}
	var merchants []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			merchants = append(merchants, id)
		}
	}
	rows.Close()
	for _, m := range merchants {
		var ids []string
		_ = rn.DB.WithTenant(ctx, m, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE report_runs SET status = 'queued' WHERE status = 'running' AND started_at < now() - interval '10 minutes'`); err != nil {
				return err
			}
			rs, err := tx.Query(ctx, `SELECT id FROM report_runs WHERE status = 'queued' AND created_at < now() - interval '30 seconds' ORDER BY created_at LIMIT 10`)
			if err != nil {
				return err
			}
			defer rs.Close()
			for rs.Next() {
				var id string
				if rs.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			return rs.Err()
		})
		for _, id := range ids {
			rn.Enqueue(m, id)
		}
	}
}

func (rn *Runner) execute(ctx context.Context, merchantID, runID string) {
	var spec Spec
	var name, format, userID string
	var libraryKey *string
	claimed := false
	err := rn.DB.WithTenant(ctx, merchantID, func(ctx context.Context, tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(ctx, `UPDATE report_runs SET status = 'running', started_at = now() WHERE id = $1 AND status = 'queued'
			RETURNING spec, name, format, requested_by, library_key`, runID).Scan(&raw, &name, &format, &userID, &libraryKey)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // someone else has it, or it's done
		}
		if err != nil {
			return err
		}
		claimed = true
		return json.Unmarshal(raw, &spec)
	})
	if err != nil || !claimed {
		return
	}
	fail := func(msg string) {
		_ = rn.DB.WithTenant(ctx, merchantID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE report_runs SET status = 'failed', error = $2, finished_at = now() WHERE id = $1`, runID, msg)
			return err
		})
	}

	// The requester's scope and plan, as of now.
	var scope db.BranchScope
	var active bool
	err = rn.DB.WithTenant(ctx, merchantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT status = 'active' FROM users WHERE id::text = $1`, userID).Scan(&active); err != nil {
			return err
		}
		var err error
		scope, err = authn.ResolveScope(ctx, tx, userID, "")
		return err
	})
	if err != nil || !active {
		fail("the person who asked for this report can no longer run reports")
		return
	}
	ent, err := subscriptions.For(ctx, rn.DB, merchantID)
	if err != nil {
		fail("could not check the plan")
		return
	}
	// A standard report (library.go) runs on every plan; a built one needs
	// the builder.
	if libraryKey == nil && !ent.Plan.Has(subscriptions.FeatureCustomReportBuilder) {
		fail("the custom report builder isn't part of the current plan")
		return
	}
	spec.clock = locale.MerchantLocation(ctx, rn.DB, merchantID)
	c, err := Compile(spec, ent.RetentionHorizon(time.Now()), ExportRows)
	if err != nil {
		fail(strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "))
		return
	}
	var res Result
	err = rn.DB.WithTenant(db.WithBranchScope(ctx, scope), merchantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		res, err = Execute(ctx, tx, c, ExportRows, exportTimeout)
		return err
	})
	if err != nil {
		log.Printf("report run %s: %v", runID, err)
		fail("the report couldn't be produced: narrow the dates or add filters")
		return
	}
	var out []byte
	if format == "xlsx" {
		out, err = XLSX(res, name)
	} else {
		out, err = CSV(res)
	}
	if err != nil {
		fail("could not write the file")
		return
	}
	_ = rn.DB.WithTenant(ctx, merchantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE report_runs SET status = 'done', row_count = $2, truncated = $3, result = $4, finished_at = now() WHERE id = $1`,
			runID, len(res.Rows), res.Truncated, out)
		return err
	})
}

type Run struct {
	ID         string     `json:"run_id"`
	TemplateID *string    `json:"template_id"`
	Name       string     `json:"name"`
	Format     string     `json:"format"`
	Status     string     `json:"status"`
	RowCount   *int       `json:"row_count"`
	Truncated  bool       `json:"truncated"`
	Error      *string    `json:"error"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
	LibraryKey *string    `json:"library_key"`
	DateFrom   *string    `json:"date_from"`
	DateTo     *string    `json:"date_to"`
	Period     *string    `json:"period"`
}

const runColumns = `id, template_id, name, format, status, row_count, truncated, error, created_at, finished_at, library_key,
	NULLIF(spec->>'date_from', ''), NULLIF(spec->>'date_to', ''), NULLIF(spec->>'period', '')`

func scanRun(row pgx.Row) (Run, error) {
	var x Run
	err := row.Scan(&x.ID, &x.TemplateID, &x.Name, &x.Format, &x.Status, &x.RowCount, &x.Truncated, &x.Error, &x.CreatedAt, &x.FinishedAt,
		&x.LibraryKey, &x.DateFrom, &x.DateTo, &x.Period)
	return x, err
}

// StartRun: POST /report-builder/runs {template_id? | spec, name?, format,
// date_from?, date_to?} — 202 with the queued run. A saved template may be
// exported by anyone who can see it; an unsaved spec needs reports.build.
func (h *Handler) StartRun(w http.ResponseWriter, r *http.Request) {
	claims, _ := authn.FromContext(r.Context())
	var req struct {
		TemplateID string `json:"template_id"`
		Spec       *Spec  `json:"spec"`
		Name       string `json:"name"`
		Format     string `json:"format"`
		DateFrom   string `json:"date_from"`
		DateTo     string `json:"date_to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Format != "csv" && req.Format != "xlsx" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "format must be csv or xlsx")
		return
	}
	var run Run
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var spec Spec
		var templateID *string
		name := strings.TrimSpace(req.Name)
		if req.TemplateID != "" {
			t, err := getTemplate(ctx, tx, req.TemplateID, claims.UserID)
			if err != nil {
				return err
			}
			spec, templateID = t.Spec, &t.ID
			if name == "" {
				name = t.Name
			}
		} else {
			if req.Spec == nil {
				return invalid("template_id or spec is required")
			}
			codes, err := authn.FetchPermissions(ctx, tx, claims.UserID)
			if err != nil {
				return err
			}
			if !authn.HasPermission(codes, "reports.build") {
				return errNoBuild
			}
			spec = *req.Spec
		}
		if req.DateFrom != "" {
			spec.DateFrom = req.DateFrom
		}
		if req.DateTo != "" {
			spec.DateTo = req.DateTo
		}
		if name == "" {
			name = "Report"
		}
		if _, err := Compile(spec, horizonFor(r.Context()), ExportRows); err != nil {
			return err
		}
		raw, _ := json.Marshal(spec)
		var err error
		run, err = scanRun(tx.QueryRow(ctx, `INSERT INTO report_runs (merchant_id, template_id, name, spec, format, requested_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5) RETURNING `+runColumns,
			templateID, name, raw, req.Format, claims.UserID))
		return err
	})
	if errors.Is(err, errNoBuild) {
		httpx.Error(w, http.StatusForbidden, "FORBIDDEN", "you don't have the reports.build permission")
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	h.Runner.Enqueue(claims.TenantID, run.ID)
	httpx.JSON(w, http.StatusAccepted, run)
}

var errNoBuild = errors.New("reports.build required")

// ListRuns: GET /report-builder/runs — the caller's last 50 exports.
func (h *Handler) ListRuns(w http.ResponseWriter, r *http.Request) {
	claims, _ := authn.FromContext(r.Context())
	out := []Run{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+runColumns+` FROM report_runs WHERE requested_by::text = $1 ORDER BY created_at DESC LIMIT 50`, claims.UserID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			x, err := scanRun(rows)
			if err != nil {
				return err
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"runs": out})
}

// GetRun: GET /report-builder/runs/{id} — the caller's own run.
func (h *Handler) GetRun(w http.ResponseWriter, r *http.Request) {
	claims, _ := authn.FromContext(r.Context())
	var x Run
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		x, err = scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM report_runs WHERE id::text = $1 AND requested_by::text = $2`,
			chi.URLParam(r, "id"), claims.UserID))
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, x)
}

// Download: GET /report-builder/runs/{id}/download — the file, to the
// person who asked for it only.
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	claims, _ := authn.FromContext(r.Context())
	var name, format, status string
	var data []byte
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name, format, status, result FROM report_runs WHERE id::text = $1 AND requested_by::text = $2`,
			chi.URLParam(r, "id"), claims.UserID).Scan(&name, &format, &status, &data)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if status != "done" {
		httpx.Error(w, http.StatusConflict, "RUN_NOT_READY", "the report is still "+status)
		return
	}
	ct := "text/csv; charset=utf-8"
	if format == "xlsx" {
		ct = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	file := strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r == '/' || r < 32 {
			return '_'
		}
		return r
	}, name) + "." + format
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`"`)
	httpx.Binary(w, http.StatusOK, ct, data)
}
