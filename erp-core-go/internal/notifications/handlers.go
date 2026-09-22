package notifications

import (
	"context"
	"log"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB       *db.DB
	Provider Provider
	// PhoneChannel resolves DispatchPhone's logical "send to a phone
	// number" calls to a concrete channel — "sms" or "whatsapp" — without
	// callers (internal/sales, internal/inventory, internal/purchase)
	// needing to know or care which one a merchant/deployment has
	// configured. Defaults to "sms" if unset (see internal/config).
	PhoneChannel string
}

// DispatchEmail and DispatchPhone are fire-and-forget by design (no
// returned error) — a receipt notification, a low-stock alert, or a bill
// reminder must never fail the real operation that triggered it (a
// checkout, a stock sweep, an accounting sweep), the same "best-effort,
// logged not fatal" principle internal/search's reindex-on-price-change
// hook already established. Both must be called with tx already inside a
// db.WithTenant block, same as every other tenant-scoped write in this
// codebase — the audit row and whatever triggered it commit or roll back
// together.

func (h *Handler) DispatchEmail(ctx context.Context, tx pgx.Tx, category, recipient, subject, body, referenceType, referenceID string) {
	h.dispatch(ctx, tx, string(ChannelEmail), category, recipient, subject, body, referenceType, referenceID)
}

func (h *Handler) DispatchPhone(ctx context.Context, tx pgx.Tx, category, recipient, body, referenceType, referenceID string) {
	channel := h.PhoneChannel
	if channel == "" {
		channel = string(ChannelSMS)
	}
	h.dispatch(ctx, tx, channel, category, recipient, "", body, referenceType, referenceID)
}

func (h *Handler) dispatch(ctx context.Context, tx pgx.Tx, channel, category, recipient, subject, body, referenceType, referenceID string) {
	if recipient == "" {
		return // no contact info for this channel — not an error, just nothing to send
	}

	err := h.Provider.Send(ctx, Message{Channel: Channel(channel), Recipient: recipient, Subject: subject, Body: body})
	status := "sent"
	var errMsg *string
	if err != nil {
		status = "failed"
		m := err.Error()
		errMsg = &m
		log.Printf("notifications: %s/%s to %s failed: %v", channel, category, recipient, err)
	}

	var subj, refType, refID *string
	if subject != "" {
		subj = &subject
	}
	if referenceType != "" {
		refType = &referenceType
	}
	if referenceID != "" {
		refID = &referenceID
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO notifications (id, merchant_id, channel, category, recipient, subject, body, reference_type, reference_id, status, error_message)
		VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		channel, category, recipient, subj, body, refType, refID, status, errMsg,
	); err != nil {
		log.Printf("notifications: could not log dispatch to %s: %v", recipient, err)
	}
}

// ---------------------------------------------------------------------
// GET /notifications?category=&channel=&status=&page=&limit= — audit log
// of every notification this merchant's tenant has sent, gated by
// notifications.view since bodies/recipients are real customer PII.
// ---------------------------------------------------------------------

type notificationResponse struct {
	NotificationID string  `json:"notification_id"`
	Channel        string  `json:"channel"`
	Category       string  `json:"category"`
	Recipient      string  `json:"recipient"`
	Subject        *string `json:"subject"`
	Body           string  `json:"body"`
	ReferenceType  *string `json:"reference_type"`
	ReferenceID    *string `json:"reference_id"`
	Status         string  `json:"status"`
	ErrorMessage   *string `json:"error_message"`
	CreatedAt      string  `json:"created_at"`
}

func (h *Handler) ListNotifications(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	category := q.Get("category")
	channel := q.Get("channel")
	status := q.Get("status")

	limit := 50
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 && l <= 200 {
		limit = l
	}
	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}
	offset := (page - 1) * limit

	list := []notificationResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, channel, category, recipient, subject, body, reference_type, reference_id::text, status, error_message, created_at::text
			FROM notifications
			WHERE ($1 = '' OR category = $1)
			  AND ($2 = '' OR channel = $2)
			  AND ($3 = '' OR status = $3)
			ORDER BY created_at DESC
			LIMIT $4 OFFSET $5`,
			category, channel, status, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var n notificationResponse
			if err := rows.Scan(&n.NotificationID, &n.Channel, &n.Category, &n.Recipient, &n.Subject, &n.Body,
				&n.ReferenceType, &n.ReferenceID, &n.Status, &n.ErrorMessage, &n.CreatedAt); err != nil {
				return err
			}
			list = append(list, n)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list notifications")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"notifications": list, "page": page, "limit": limit})
}
