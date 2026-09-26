package audit

import (
	"context"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB *db.DB
}

type logEntry struct {
	ID          int64   `json:"id"`
	EntityType  string  `json:"entity_type"`
	EntityID    string  `json:"entity_id"`
	Action      string  `json:"action"`
	PerformedBy *string `json:"performed_by"`
	Before      *string `json:"before_value"`
	After       *string `json:"after_value"`
	Reason      string  `json:"reason"`
	CreatedAt   string  `json:"created_at"`
	Checksum    *string `json:"checksum"`
}

// ListLogs: GET /audit-logs?entity_type=&entity_id=&start=&end=&page=&limit=
// Gated by audit.view — see migrations/021_audit_trail_hardening.sql for
// why this is a Merchant-Admin-only read, not the usual open-to-any-
// authenticated-user shape most reports in this codebase have: rows here
// can contain other users' before/after field values (prices, credit
// limits, discount overrides), a real compliance/oversight surface, not
// a routine operational one.
func (h *Handler) ListLogs(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	entityType := q.Get("entity_type")
	entityID := q.Get("entity_id")
	start := q.Get("start")
	end := q.Get("end")

	limit := 50
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 && l <= 200 {
		limit = l
	}
	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}
	offset := (page - 1) * limit

	entries := []logEntry{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, entity_type, entity_id::text, action, performed_by::text, before_value::text, after_value::text,
			       COALESCE(reason,''), created_at::text, checksum
			FROM audit_logs
			WHERE ($1 = '' OR entity_type = $1)
			  AND ($2 = '' OR entity_id::text = $2)
			  AND ($3 = '' OR created_at >= $3::timestamptz)
			  AND ($4 = '' OR created_at < ($4::date + interval '1 day'))
			ORDER BY created_at DESC, id DESC
			LIMIT $5 OFFSET $6`,
			entityType, entityID, start, end, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e logEntry
			if err := rows.Scan(&e.ID, &e.EntityType, &e.EntityID, &e.Action, &e.PerformedBy, &e.Before, &e.After, &e.Reason, &e.CreatedAt, &e.Checksum); err != nil {
				return err
			}
			entries = append(entries, e)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list audit logs")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": entries, "page": page, "limit": limit})
}

// VerifyChain: GET /audit-logs/verify — gated by audit.view. Walks this
// tenant's whole chain and reports whether it's internally consistent;
// see Verify's own doc comment for exactly what that does and doesn't
// prove.
func (h *Handler) VerifyChain(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var result VerifyResult
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		result, err = Verify(ctx, tx)
		return err
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not verify audit chain")
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}
