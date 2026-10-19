package reportbuilder

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/softdelete"
	"erp-core-go/internal/subscriptions"
)

const previewTimeout = 15 * time.Second

type Handler struct {
	DB     *db.DB
	Runner *Runner
}

func horizonFor(ctx context.Context) time.Time {
	if e, ok := subscriptions.FromContext(ctx); ok {
		return e.RetentionHorizon(time.Now())
	}
	return time.Time{}
}

// writeErr maps the compiler's and the database's refusals.
func writeErr(w http.ResponseWriter, err error) {
	var re *RetentionError
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, ErrInvalid):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REPORT", strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "))
	case errors.As(err, &re):
		httpx.ErrorWithDetails(w, http.StatusForbidden, "RETENTION_LIMIT",
			"your plan's reports reach back to "+re.Earliest.Format("2 Jan 2006"), map[string]any{"earliest": re.Earliest.Format("2006-01-02")})
	case errors.As(err, &pgErr) && pgErr.Code == "57014":
		httpx.Error(w, http.StatusUnprocessableEntity, "REPORT_TOO_HEAVY",
			"this report took too long to run here: narrow the dates or add filters, or export it, which runs in the background")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "not found")
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not run the report")
	}
}

type fieldInfo struct {
	Field
	Aggs []string `json:"aggs"`
	Ops  []string `json:"ops"`
}

// Datasets: GET /report-builder/datasets — the semantic layer: what can be
// reported on, and how.
func (h *Handler) Datasets(w http.ResponseWriter, r *http.Request) {
	out := make([]map[string]any, 0, len(Datasets))
	for _, d := range Datasets {
		fs := make([]fieldInfo, len(d.Fields))
		for i, f := range d.Fields {
			fs[i] = fieldInfo{Field: f, Aggs: f.Aggs(), Ops: OpsFor(f.Type)}
		}
		out = append(out, map[string]any{"key": d.Key, "label": d.Label, "description": d.Description, "has_date_range": d.HasDateRange, "fields": fs})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"datasets": out, "limits": map[string]any{
		"max_date_span_days": MaxDateSpanDays, "preview_rows": PreviewRows, "export_rows": ExportRows, "max_columns": maxFields, "max_filters": MaxFilters}})
}

func (h *Handler) preview(w http.ResponseWriter, r *http.Request, spec Spec) {
	claims, _ := authn.FromContext(r.Context())
	c, err := Compile(spec, horizonFor(r.Context()), PreviewRows)
	if err != nil {
		writeErr(w, err)
		return
	}
	var res Result
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		res, err = Execute(ctx, tx, c, PreviewRows, previewTimeout)
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// Query: POST /report-builder/query {spec} — reports.build: run an unsaved
// report (first 1,000 rows).
func (h *Handler) Query(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Spec Spec `json:"spec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	h.preview(w, r, req.Spec)
}

// --------------------------------------------------------------- templates

type Template struct {
	ID          string    `json:"template_id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	Spec        Spec      `json:"spec"`
	Shared      bool      `json:"shared"`
	CreatedBy   string    `json:"created_by"`
	CreatedName string    `json:"created_by_name"`
	Mine        bool      `json:"mine"`
	UpdatedAt   time.Time `json:"updated_at"`
	// LibraryKey: saved from a standard report (library.go), not built.
	LibraryKey *string `json:"library_key"`
}

const templateSelect = `SELECT t.id, t.name, t.description, t.spec, t.shared, t.created_by, u.name, t.created_by::text = $1, t.updated_at,
	       t.library_key
	FROM report_templates t JOIN users u ON u.id = t.created_by`

func scanTemplate(row pgx.Row) (Template, error) {
	var t Template
	var spec []byte
	if err := row.Scan(&t.ID, &t.Name, &t.Description, &spec, &t.Shared, &t.CreatedBy, &t.CreatedName, &t.Mine, &t.UpdatedAt, &t.LibraryKey); err != nil {
		return Template{}, err
	}
	return t, json.Unmarshal(spec, &t.Spec)
}

// visible: the caller's own templates and every shared one.
func getTemplate(ctx context.Context, tx pgx.Tx, id, userID string) (Template, error) {
	return scanTemplate(tx.QueryRow(ctx, templateSelect+` WHERE t.id::text = $2 AND (t.created_by::text = $1 OR t.shared)`, userID, id))
}

// ListTemplates: GET /report-builder/templates — mine and shared.
func (h *Handler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	claims, _ := authn.FromContext(r.Context())
	out := []Template{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, templateSelect+` WHERE t.created_by::text = $1 OR t.shared ORDER BY lower(t.name)`, claims.UserID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanTemplate(rows)
			if err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"templates": out})
}

func (h *Handler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	claims, _ := authn.FromContext(r.Context())
	var t Template
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		t, err = getTemplate(ctx, tx, chi.URLParam(r, "id"), claims.UserID)
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, t)
}

type templateRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Spec        Spec   `json:"spec"`
	Shared      bool   `json:"shared"`
}

func (q *templateRequest) validate() error {
	q.Name = strings.TrimSpace(q.Name)
	if q.Name == "" || len(q.Name) > 120 {
		return invalid("a name of up to 120 characters is required")
	}
	// A template must compile (dates may be left for whoever runs it).
	probe := q.Spec
	if ds, ok := FindDataset(probe.Dataset); ok && ds.HasDateRange && (probe.DateFrom == "" || probe.DateTo == "") {
		probe.DateFrom, probe.DateTo = "2026-01-01", "2026-01-31"
	}
	_, err := Compile(probe, time.Time{}, PreviewRows)
	return err
}

// CreateTemplate: POST /report-builder/templates — reports.build.
func (h *Handler) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	claims, _ := authn.FromContext(r.Context())
	var req templateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if err := req.validate(); err != nil {
		writeErr(w, err)
		return
	}
	spec, _ := json.Marshal(req.Spec)
	var t Template
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO report_templates (merchant_id, name, description, spec, shared, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1, NULLIF($2, ''), $3, $4, $5) RETURNING id`,
			req.Name, strings.TrimSpace(req.Description), spec, req.Shared, claims.UserID).Scan(&id); err != nil {
			return err
		}
		var err error
		if t, err = getTemplate(ctx, tx, id, claims.UserID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "report_templates", EntityID: id, Action: "create", PerformedBy: claims.UserID, After: t})
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, t)
}

// UpdateTemplate: PUT /report-builder/templates/{id} — reports.build, own templates only.
func (h *Handler) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	claims, _ := authn.FromContext(r.Context())
	var req templateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if err := req.validate(); err != nil {
		writeErr(w, err)
		return
	}
	spec, _ := json.Marshal(req.Spec)
	var t Template
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := getTemplate(ctx, tx, chi.URLParam(r, "id"), claims.UserID)
		if err != nil {
			return err
		}
		if !before.Mine {
			return errNotOwner
		}
		if _, err := tx.Exec(ctx, `UPDATE report_templates SET name = $2, description = NULLIF($3, ''), spec = $4, shared = $5, updated_at = now()
			WHERE id = $1`, before.ID, req.Name, strings.TrimSpace(req.Description), spec, req.Shared); err != nil {
			return err
		}
		if t, err = getTemplate(ctx, tx, before.ID, claims.UserID); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "report_templates", EntityID: t.ID, Action: "update", PerformedBy: claims.UserID, Before: before, After: t})
	})
	if errors.Is(err, errNotOwner) {
		httpx.Error(w, http.StatusForbidden, "NOT_OWNER", "only the person who made a shared template can change it; duplicate it to make your own")
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, t)
}

var errNotOwner = errors.New("not the owner")

// DeleteTemplate: DELETE /report-builder/templates/{id} — own templates only.
func (h *Handler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	claims, _ := authn.FromContext(r.Context())
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		t, err := getTemplate(ctx, tx, chi.URLParam(r, "id"), claims.UserID)
		if err != nil {
			return err
		}
		if !t.Mine {
			return errNotOwner
		}
		return softdelete.Delete(ctx, tx, "report_templates", t.ID, claims.UserID, "")
	})
	if errors.Is(err, errNotOwner) {
		httpx.Error(w, http.StatusForbidden, "NOT_OWNER", "only the person who made a template can delete it")
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DuplicateTemplate: POST /report-builder/templates/{id}/duplicate —
// reports.build: a private copy the caller owns.
func (h *Handler) DuplicateTemplate(w http.ResponseWriter, r *http.Request) {
	claims, _ := authn.FromContext(r.Context())
	var t Template
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		src, err := getTemplate(ctx, tx, chi.URLParam(r, "id"), claims.UserID)
		if err != nil {
			return err
		}
		spec, _ := json.Marshal(src.Spec)
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO report_templates (merchant_id, name, description, spec, shared, created_by)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, false, $4) RETURNING id`,
			"Copy of "+src.Name, src.Description, spec, claims.UserID).Scan(&id); err != nil {
			return err
		}
		t, err = getTemplate(ctx, tx, id, claims.UserID)
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, t)
}

// RunTemplate: POST /report-builder/templates/{id}/run {date_from?, date_to?}
// — anyone who can see the template: the first 1,000 rows, inside their
// own branch scope (a shared template shows each person their own data).
func (h *Handler) RunTemplate(w http.ResponseWriter, r *http.Request) {
	claims, _ := authn.FromContext(r.Context())
	var req struct {
		DateFrom string `json:"date_from"`
		DateTo   string `json:"date_to"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	var t Template
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		t, err = getTemplate(ctx, tx, chi.URLParam(r, "id"), claims.UserID)
		return err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if req.DateFrom != "" {
		t.Spec.DateFrom = req.DateFrom
	}
	if req.DateTo != "" {
		t.Spec.DateTo = req.DateTo
	}
	h.preview(w, r, t.Spec)
}
