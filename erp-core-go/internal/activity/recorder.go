// Package activity records every POS and web-admin action as one
// activity_logs row per API request — who, from which client and
// terminal, from what IP, which route, the outcome and the (redacted)
// request body — subject to the merchant's own audit_settings. See
// migrations/051_activity_log.sql for how this relates to the always-on
// entity audit trail in internal/audit.
//
// Rows are written off the request path by a small worker pool, so a
// checkout doesn't wait on its own log line. When the queue is full the
// request writes its row itself instead of dropping it: an audit log
// that silently loses entries under load is worse than a slow one.
package activity

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/reqmeta"
)

const (
	queueSize   = 4096
	workers     = 2
	settingsTTL = 30 * time.Second
)

type entry struct {
	tenantID, userID, branchID, terminalID string
	client, method, route, path, module    string
	entityID                               string
	status, durationMS                     int
	ip, userAgent, requestID               string
	body                                   []byte // redacted JSON, nil when not captured
	at                                     time.Time
}

// Recorder owns the write queue and the per-merchant settings cache.
type Recorder struct {
	DB *db.DB

	queue    chan entry
	wg       sync.WaitGroup
	once     sync.Once
	settings sync.Map // tenant id → cachedSettings
}

type cachedSettings struct {
	s       Settings
	expires time.Time
}

func NewRecorder(database *db.DB) *Recorder {
	rc := &Recorder{DB: database, queue: make(chan entry, queueSize)}
	for i := 0; i < workers; i++ {
		rc.wg.Add(1)
		go func() {
			defer rc.wg.Done()
			for e := range rc.queue {
				rc.write(e)
			}
		}()
	}
	return rc
}

// Close stops accepting rows and waits for the queue to drain. Call it
// after the HTTP server has shut down.
func (rc *Recorder) Close() {
	rc.once.Do(func() { close(rc.queue) })
	rc.wg.Wait()
}

func (rc *Recorder) write(e entry) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := rc.DB.WithTenant(ctx, e.tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO activity_logs (merchant_id, occurred_at, user_id, branch_id, terminal_id, client, method, route, path,
			    module, entity_id, status_code, duration_ms, ip, user_agent, request_id, request_body)
			VALUES (current_setting('app.tenant_id')::uuid, $1, NULLIF($2,'')::uuid, NULLIF($3,'')::uuid, NULLIF($4,'')::uuid,
			    $5, $6, $7, $8, $9, NULLIF($10,''), $11, $12, NULLIF($13,''), NULLIF($14,''), NULLIF($15,''), $16::jsonb)`,
			e.at, e.userID, uuidOrEmpty(e.branchID), uuidOrEmpty(e.terminalID), e.client, e.method, e.route, e.path,
			e.module, e.entityID, e.status, e.durationMS, e.ip, truncate(e.userAgent, 400), e.requestID, nullableJSON(e.body))
		return err
	})
	if err != nil {
		log.Printf("activity: write %s %s for %s: %v", e.method, e.route, e.tenantID, err)
	}
}

// Middleware records the request once the handler has answered. Mount it
// right after authn.RequireAuth, so refusals by the middleware that
// follows (branch scope, plan limits, permissions) are recorded too; on
// the login routes, which have no claims, the handler reports the
// merchant and user through reqmeta instead.
func (rc *Recorder) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var body []byte
		var bodyBytes int
		var truncated bool
		if isWrite(r.Method) {
			body, bodyBytes, truncated = peekBody(r)
		}
		ctx, actor := reqmeta.WithActor(r.Context())
		r = r.WithContext(ctx)
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(ww, r)

		tenantID, userID, branchID := "", "", ""
		if claims, ok := authn.FromContext(r.Context()); ok {
			tenantID, userID, branchID = claims.TenantID, claims.UserID, claims.BranchID
		} else {
			tenantID, userID = actor.Get()
		}
		if tenantID == "" {
			return // a platform token, or a login for a merchant that doesn't exist
		}
		s := rc.settingsFor(r.Context(), tenantID)
		route := routePattern(r)
		module := moduleOf(route)
		if !s.Enabled || (!isWrite(r.Method) && !s.LogReads) || s.disabled(module) {
			return
		}
		if branchID == "" {
			branchID = r.URL.Query().Get("branch_id")
		}
		e := entry{
			tenantID: tenantID, userID: userID, branchID: branchID,
			terminalID: r.Header.Get("X-Terminal-Id"),
			client:     clientOf(r),
			method:     r.Method, route: route, path: r.URL.Path, module: module,
			entityID:   chi.URLParam(r, "id"),
			status:     statusOf(ww),
			durationMS: int(time.Since(start).Milliseconds()),
			ip:         clientIP(r), userAgent: r.UserAgent(),
			requestID: middleware.GetReqID(r.Context()),
			at:        start,
		}
		if s.CaptureBody && isWrite(r.Method) {
			e.body = redactedBody(body, bodyBytes, truncated, r.Header.Get("Content-Type"))
		}
		select {
		case rc.queue <- e:
		default:
			rc.write(e) // queue full: slower, never lossy
		}
	})
}

func isWrite(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

func statusOf(ww middleware.WrapResponseWriter) int {
	if s := ww.Status(); s != 0 {
		return s
	}
	return http.StatusOK // the handler wrote a body without an explicit header
}

// routePattern: "/api/v1/sales/orders/{id}/checkout" → "/sales/orders/{id}/checkout".
func routePattern(r *http.Request) string {
	p := r.URL.Path
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		if rp := rctx.RoutePattern(); rp != "" {
			p = rp
		}
	}
	return strings.TrimPrefix(p, "/api/v1")
}

func moduleOf(route string) string {
	seg := strings.SplitN(strings.TrimPrefix(route, "/"), "/", 2)[0]
	if seg == "" {
		return "other"
	}
	return seg
}

// clientOf: the POS and the web admin each name themselves in X-Client;
// anything else (scripts, integrations) is "api".
func clientOf(r *http.Request) string {
	switch c := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Client"))); c {
	case "pos", "web":
		return c
	}
	return "api"
}

// clientIP: chi's RealIP middleware has already replaced RemoteAddr with
// X-Real-IP / X-Forwarded-For when a proxy sent one.
func clientIP(r *http.Request) string {
	ip := r.RemoteAddr
	if strings.HasPrefix(ip, "[") {
		if i := strings.Index(ip, "]"); i > 0 {
			return ip[1:i]
		}
	}
	if i := strings.LastIndex(ip, ":"); i > 0 && strings.Count(ip, ":") == 1 {
		return ip[:i]
	}
	return ip
}

// ClientIP is clientIP for other packages (attendance records the IP a
// punch came from the same way).
func ClientIP(r *http.Request) string { return clientIP(r) }

func uuidOrEmpty(s string) string {
	if len(s) != 36 || strings.Count(s, "-") != 4 {
		return ""
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func nullableJSON(b []byte) *string {
	if b == nil {
		return nil
	}
	s := string(b)
	return &s
}

// Client is clientOf for other packages: "pos", "web" or "api".
func Client(r *http.Request) string { return clientOf(r) }
