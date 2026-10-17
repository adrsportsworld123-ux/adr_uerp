package activity

import (
	"context"
	"encoding/json"
	"io"
	"log"
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

// Settings is one merchant's audit_settings row (or its defaults).
type Settings struct {
	Enabled         bool     `json:"activity_log_enabled"`
	LogReads        bool     `json:"log_read_requests"`
	CaptureBody     bool     `json:"capture_request_body"`
	DisabledModules []string `json:"disabled_modules"`
	UpdatedAt       *string  `json:"updated_at"`
	UpdatedBy       *string  `json:"updated_by"`
	// EntityAuditTrail is informational: the hash-chained trail of record
	// changes is statutory and always on (migrations/051).
	EntityAuditTrail string `json:"entity_audit_trail"`
}

func defaultSettings() Settings {
	return Settings{Enabled: true, CaptureBody: true, DisabledModules: []string{}, EntityAuditTrail: "always_on"}
}

func (s Settings) disabled(module string) bool {
	for _, m := range s.DisabledModules {
		if m == module {
			return true
		}
	}
	return false
}

func loadSettings(ctx context.Context, tx pgx.Tx) (Settings, error) {
	s := defaultSettings()
	err := tx.QueryRow(ctx, `
		SELECT activity_log_enabled, log_read_requests, capture_request_body, disabled_modules, updated_at::text, updated_by::text
		FROM audit_settings WHERE merchant_id = current_setting('app.tenant_id')::uuid`,
	).Scan(&s.Enabled, &s.LogReads, &s.CaptureBody, &s.DisabledModules, &s.UpdatedAt, &s.UpdatedBy)
	if err == pgx.ErrNoRows {
		return defaultSettings(), nil
	}
	if s.DisabledModules == nil {
		s.DisabledModules = []string{}
	}
	return s, err
}

// settingsFor is the cached read the middleware makes on every request.
// If the settings can't be read the request is logged with the defaults:
// failing towards recording is the safe direction.
func (rc *Recorder) settingsFor(ctx context.Context, tenantID string) Settings {
	if c, ok := rc.settings.Load(tenantID); ok && time.Now().Before(c.(cachedSettings).expires) {
		return c.(cachedSettings).s
	}
	var s Settings
	err := rc.DB.WithTenant(context.WithoutCancel(ctx), tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		s, err = loadSettings(ctx, tx)
		return err
	})
	if err != nil {
		log.Printf("activity: load settings for %s: %v", tenantID, err)
		return defaultSettings()
	}
	rc.settings.Store(tenantID, cachedSettings{s: s, expires: time.Now().Add(settingsTTL)})
	return s
}

// GetSettings: GET /audit/settings (audit.view)
func (rc *Recorder) GetSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var s Settings
	err := rc.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		s, err = loadSettings(ctx, tx)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load audit settings")
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

type settingsRequest struct {
	Enabled         *bool    `json:"activity_log_enabled"`
	LogReads        *bool    `json:"log_read_requests"`
	CaptureBody     *bool    `json:"capture_request_body"`
	DisabledModules []string `json:"disabled_modules"`
}

var moduleName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)

// UpdateSettings: PUT /audit/settings (audit.configure). Omitted fields
// keep their value. The change is written to the always-on audit trail —
// including switching the activity log off — and takes effect on this
// instance at once (others within settingsTTL).
func (rc *Recorder) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req settingsRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.DisabledModules != nil {
		seen := map[string]bool{}
		clean := []string{}
		for _, m := range req.DisabledModules {
			m = strings.ToLower(strings.TrimSpace(m))
			if !moduleName.MatchString(m) {
				httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "disabled_modules has an invalid module name: "+m)
				return
			}
			if m == "audit" {
				// Changing what is audited must itself stay audited.
				httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "the audit module can't be excluded")
				return
			}
			if !seen[m] {
				seen[m] = true
				clean = append(clean, m)
			}
		}
		req.DisabledModules = clean
	}

	var saved Settings
	err := rc.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := loadSettings(ctx, tx)
		if err != nil {
			return err
		}
		next := before
		if req.Enabled != nil {
			next.Enabled = *req.Enabled
		}
		if req.LogReads != nil {
			next.LogReads = *req.LogReads
		}
		if req.CaptureBody != nil {
			next.CaptureBody = *req.CaptureBody
		}
		if req.DisabledModules != nil {
			next.DisabledModules = req.DisabledModules
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_settings (merchant_id, activity_log_enabled, log_read_requests, capture_request_body, disabled_modules, updated_by, updated_at)
			VALUES (current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, now())
			ON CONFLICT (merchant_id) DO UPDATE SET
			    activity_log_enabled = EXCLUDED.activity_log_enabled, log_read_requests = EXCLUDED.log_read_requests,
			    capture_request_body = EXCLUDED.capture_request_body, disabled_modules = EXCLUDED.disabled_modules,
			    updated_by = EXCLUDED.updated_by, updated_at = now()`,
			next.Enabled, next.LogReads, next.CaptureBody, next.DisabledModules, claims.UserID); err != nil {
			return err
		}
		if saved, err = loadSettings(ctx, tx); err != nil {
			return err
		}
		return audit.Log(ctx, tx, audit.Entry{EntityType: "audit_settings", EntityID: claims.TenantID, Action: "update",
			PerformedBy: claims.UserID, Before: before, After: saved})
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not save audit settings")
		return
	}
	rc.settings.Delete(claims.TenantID)
	httpx.JSON(w, http.StatusOK, saved)
}

type activityRow struct {
	ID           int64           `json:"id"`
	OccurredAt   string          `json:"occurred_at"`
	UserID       *string         `json:"user_id"`
	UserName     *string         `json:"user_name"`
	BranchID     *string         `json:"branch_id"`
	BranchName   *string         `json:"branch_name"`
	TerminalID   *string         `json:"terminal_id"`
	TerminalName *string         `json:"terminal_name"`
	Client       string          `json:"client"`
	Method       string          `json:"method"`
	Route        string          `json:"route"`
	Path         string          `json:"path"`
	Module       string          `json:"module"`
	EntityID     *string         `json:"entity_id"`
	StatusCode   int             `json:"status_code"`
	DurationMS   int             `json:"duration_ms"`
	IP           *string         `json:"ip"`
	UserAgent    *string         `json:"user_agent"`
	RequestID    *string         `json:"request_id"`
	RequestBody  json.RawMessage `json:"request_body"`
}

// ListActivity: GET /audit/activity?user_id=&module=&client=&outcome=success|failure&entity_id=&q=&from=&to=&page=&limit=
// (audit.view). Newest first; from/to are dates (YYYY-MM-DD, to inclusive).
func (rc *Recorder) ListActivity(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	limit := 50
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 && l <= 200 {
		limit = l
	}
	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}
	outcome := q.Get("outcome")
	if outcome != "" && outcome != "success" && outcome != "failure" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "outcome must be success or failure")
		return
	}
	for _, d := range []string{q.Get("from"), q.Get("to")} {
		if d != "" {
			if _, err := time.Parse("2006-01-02", d); err != nil {
				httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "from and to must be dates (YYYY-MM-DD)")
				return
			}
		}
	}

	rows := []activityRow{}
	var total int
	err := rc.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		const where = `
			WHERE ($1 = '' OR a.user_id::text = $1)
			  AND ($2 = '' OR a.module = $2)
			  AND ($3 = '' OR a.client = $3)
			  AND ($4 = '' OR ($4 = 'success' AND a.status_code < 400) OR ($4 = 'failure' AND a.status_code >= 400))
			  AND ($5 = '' OR a.entity_id = $5)
			  AND ($6 = '' OR a.path ILIKE '%' || $6 || '%' OR a.route ILIKE '%' || $6 || '%' OR a.ip = $6)
			  AND ($7 = '' OR a.occurred_at >= $7::date)
			  AND ($8 = '' OR a.occurred_at < $8::date + 1)`
		args := []any{q.Get("user_id"), q.Get("module"), q.Get("client"), outcome, q.Get("entity_id"),
			strings.TrimSpace(q.Get("q")), q.Get("from"), q.Get("to")}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM activity_logs a`+where, args...).Scan(&total); err != nil {
			return err
		}
		rs, err := tx.Query(ctx, `
			SELECT a.id, a.occurred_at::text, a.user_id::text, u.name, a.branch_id::text, b.name, a.terminal_id::text, t.name,
			       a.client, a.method, a.route, a.path, a.module, a.entity_id, a.status_code, a.duration_ms,
			       a.ip, a.user_agent, a.request_id, a.request_body::text
			FROM activity_logs a
			LEFT JOIN users u ON u.id = a.user_id
			LEFT JOIN branches b ON b.id = a.branch_id
			LEFT JOIN pos_terminals t ON t.id = a.terminal_id`+where+`
			ORDER BY a.occurred_at DESC, a.id DESC
			LIMIT $9 OFFSET $10`, append(args, limit, (page-1)*limit)...)
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var a activityRow
			var body *string
			if err := rs.Scan(&a.ID, &a.OccurredAt, &a.UserID, &a.UserName, &a.BranchID, &a.BranchName, &a.TerminalID, &a.TerminalName,
				&a.Client, &a.Method, &a.Route, &a.Path, &a.Module, &a.EntityID, &a.StatusCode, &a.DurationMS,
				&a.IP, &a.UserAgent, &a.RequestID, &body); err != nil {
				return err
			}
			if body != nil {
				a.RequestBody = json.RawMessage(*body)
			}
			rows = append(rows, a)
		}
		return rs.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load the activity log")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"activity": rows, "total": total, "page": page, "limit": limit})
}

// ListModules: GET /audit/modules (audit.view) — the module names seen in
// the last 90 days, for the settings screen's exclusion picker and the log's filter.
func (rc *Recorder) ListModules(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	modules := []string{}
	err := rc.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rs, err := tx.Query(ctx, `SELECT DISTINCT module FROM activity_logs WHERE occurred_at > now() - interval '90 days' ORDER BY module`)
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var m string
			if err := rs.Scan(&m); err != nil {
				return err
			}
			modules = append(modules, m)
		}
		return rs.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list modules")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"modules": modules})
}
