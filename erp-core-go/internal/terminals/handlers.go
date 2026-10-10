// Package terminals manages POS terminals and pairs devices to them
// (Phase 12, phased_roadmap.md; FRD §1 hierarchy, §18 "Device binding for
// POS (registered devices only)"). See migrations/037_terminal_management.sql.
//
// pos_terminals has no RLS (migrations/003_hardening.sql — PIN login must
// resolve a terminal before any tenant context exists), so EVERY query in
// this package names merchant_id explicitly. Branch lookups still run
// inside WithTenant, where RLS applies as usual.
package terminals

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

// PairingCodeTTL bounds how long an issued code works. Short on purpose:
// it's read off an admin screen and typed into a device standing next to it.
const PairingCodeTTL = 15 * time.Minute

// pairingAlphabet has no 0/O/1/I/L, so a code read aloud or off a screen
// can't be mistyped into a different valid code. 8 characters from 31
// symbols is ~8.5e11 combinations, live for 15 minutes, single use.
const pairingAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
const pairingCodeLen = 8

var (
	errNotFound       = errors.New("not found")
	errBranchInactive = errors.New("branch inactive")
	errTermInactive   = errors.New("terminal inactive")
)

type Handler struct {
	DB *db.DB
}

type terminalResponse struct {
	TerminalID     string  `json:"terminal_id"`
	Name           string  `json:"name"`
	BranchID       string  `json:"branch_id"`
	BranchName     string  `json:"branch_name"`
	BranchStatus   string  `json:"branch_status"`
	Status         string  `json:"status"`
	Paired         bool    `json:"paired"`
	PairedAt       *string `json:"paired_at"`
	PairingPending bool    `json:"pairing_pending"`
	LastSyncedAt   *string `json:"last_synced_at"`
	CreatedAt      string  `json:"created_at"`
}

const terminalSelect = `
	SELECT t.id, t.name, t.branch_id, b.name, b.status, t.status,
	       COALESCE(t.device_fingerprint, '') <> '', t.paired_at::text,
	       t.pairing_code_hash IS NOT NULL AND t.pairing_expires_at > now(),
	       t.last_synced_at::text, t.created_at::text
	FROM pos_terminals t
	JOIN branches b ON b.id = t.branch_id`

func scanTerminal(row pgx.Row) (terminalResponse, error) {
	var t terminalResponse
	err := row.Scan(&t.TerminalID, &t.Name, &t.BranchID, &t.BranchName, &t.BranchStatus, &t.Status,
		&t.Paired, &t.PairedAt, &t.PairingPending, &t.LastSyncedAt, &t.CreatedAt)
	return t, err
}

func loadTerminal(ctx context.Context, tx pgx.Tx, merchantID, id string) (terminalResponse, error) {
	t, err := scanTerminal(tx.QueryRow(ctx, terminalSelect+`
		WHERE t.merchant_id = $1 AND t.id::text = $2 AND t.channel = 'pos'`, merchantID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, errNotFound
	}
	return t, err
}

// List: GET /pos-terminals?branch_id=&status=&q=
// Physical POS terminals only — each branch's virtual "online" terminal
// (migrations/026) is an implementation detail of the storefront channel.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	branchID, status, search := q.Get("branch_id"), q.Get("status"), strings.TrimSpace(q.Get("q"))
	if status == "all" {
		status = ""
	}
	list := []terminalResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, terminalSelect+`
			WHERE t.merchant_id = $1 AND t.channel = 'pos'
			  AND ($2 = '' OR t.branch_id::text = $2)
			  AND ($3 = '' OR t.status = $3)
			  AND ($4 = '' OR t.name ILIKE '%' || $4 || '%' OR b.name ILIKE '%' || $4 || '%')
			ORDER BY b.name, t.name`, claims.TenantID, branchID, status, search)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanTerminal(rows)
			if err != nil {
				return err
			}
			list = append(list, t)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list terminals")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"terminals": list, "total": len(list)})
}

// Create: POST /pos-terminals {branch_id, name} — terminal.manage.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req struct {
		BranchID string `json:"branch_id"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" || req.BranchID == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "branch_id and name are required")
		return
	}
	var resp terminalResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		// The branch must be this merchant's (RLS on branches) and active.
		var branchStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM branches WHERE id::text = $1`, req.BranchID).Scan(&branchStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errNotFound
			}
			return err
		}
		if branchStatus != "active" {
			return errBranchInactive
		}
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO pos_terminals (id, merchant_id, branch_id, name, channel, status)
			VALUES (gen_random_uuid(), $1, $2::uuid, $3, 'pos', 'active') RETURNING id`,
			claims.TenantID, req.BranchID, strings.TrimSpace(req.Name)).Scan(&id); err != nil {
			return err
		}
		var err error
		resp, err = loadTerminal(ctx, tx, claims.TenantID, id)
		return err
	})
	if respondErr(w, err, "could not create terminal") {
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// Update: PATCH /pos-terminals/{id} {name?, status?} — terminal.manage.
// Deactivating a terminal stops new PIN logins on it immediately (PIN
// login already refuses a non-active terminal); its sales history stays.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var req struct {
		Name   *string `json:"name"`
		Status *string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Name != nil {
		t := strings.TrimSpace(*req.Name)
		if t == "" {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name can't be empty")
			return
		}
		req.Name = &t
	}
	if req.Status != nil && *req.Status != "active" && *req.Status != "inactive" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be active or inactive")
		return
	}
	var resp terminalResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE pos_terminals SET name = COALESCE($3, name), status = COALESCE($4, status),
			       -- a deactivated terminal can't be paired, so drop any live code
			       pairing_code_hash = CASE WHEN $4 = 'inactive' THEN NULL ELSE pairing_code_hash END,
			       updated_at = now()
			WHERE merchant_id = $1 AND id::text = $2 AND channel = 'pos'`, claims.TenantID, id, req.Name, req.Status)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errNotFound
		}
		resp, err = loadTerminal(ctx, tx, claims.TenantID, id)
		return err
	})
	if respondErr(w, err, "could not update terminal") {
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// newPairingCode draws uniformly from pairingAlphabet: bytes at or above
// the largest multiple of the alphabet size are discarded (rejection
// sampling), so no symbol is more likely than another.
func newPairingCode() (string, error) {
	limit := 256 - 256%len(pairingAlphabet)
	out := make([]byte, 0, pairingCodeLen)
	buf := make([]byte, 16)
	for len(out) < pairingCodeLen {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, v := range buf {
			if int(v) < limit && len(out) < pairingCodeLen {
				out = append(out, pairingAlphabet[int(v)%len(pairingAlphabet)])
			}
		}
	}
	return string(out), nil
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}

// IssuePairingCode: POST /pos-terminals/{id}/pairing-code — terminal.manage.
// Returns the code ONCE; only its hash is stored. Issuing a new code
// replaces any earlier unused one for the terminal.
func (h *Handler) IssuePairingCode(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	code, err := newPairingCode()
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not generate a pairing code")
		return
	}
	expires := time.Now().Add(PairingCodeTTL).UTC()
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		t, err := loadTerminal(ctx, tx, claims.TenantID, id)
		if err != nil {
			return err
		}
		if t.Status != "active" {
			return errTermInactive
		}
		if t.BranchStatus != "active" {
			return errBranchInactive
		}
		_, err = tx.Exec(ctx, `
			UPDATE pos_terminals SET pairing_code_hash = $3, pairing_expires_at = $4, updated_at = now()
			WHERE merchant_id = $1 AND id::text = $2`, claims.TenantID, id, hashCode(code), expires)
		return err
	})
	if respondErr(w, err, "could not issue a pairing code") {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"code": code, "expires_at": expires.Format(time.RFC3339)})
}

// ResetDevice: POST /pos-terminals/{id}/reset-device — terminal.manage.
// Unbinds the device (lost/replaced tablet). The next device must pair
// with a fresh code; until then PIN login binds on first use, as before.
func (h *Handler) ResetDevice(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var resp terminalResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE pos_terminals SET device_fingerprint = NULL, paired_at = NULL, updated_at = now()
			WHERE merchant_id = $1 AND id::text = $2 AND channel = 'pos'`, claims.TenantID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errNotFound
		}
		resp, err = loadTerminal(ctx, tx, claims.TenantID, id)
		return err
	})
	if respondErr(w, err, "could not reset the device") {
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

type pairRequest struct {
	MerchantCode      string `json:"merchant_code"`
	PairingCode       string `json:"pairing_code"`
	DeviceFingerprint string `json:"device_fingerprint"`
}

// Pair: POST /pos-terminals/pair — UNAUTHENTICATED by design: a fresh
// device has no session yet. It proves itself with the merchant code plus
// a live, single-use pairing code an admin just issued. Every failure is
// the same 401 INVALID_PAIRING_CODE, so the endpoint can't be used to
// probe which merchants or codes exist.
func (h *Handler) Pair(w http.ResponseWriter, r *http.Request) {
	var req pairRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		strings.TrimSpace(req.MerchantCode) == "" || strings.TrimSpace(req.PairingCode) == "" || strings.TrimSpace(req.DeviceFingerprint) == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "merchant_code, pairing_code and device_fingerprint are required")
		return
	}
	ctx := r.Context()
	invalid := func() {
		httpx.Error(w, http.StatusUnauthorized, "INVALID_PAIRING_CODE", "that pairing code isn't valid — check the merchant code, or ask for a new code")
	}

	var merchantID, merchantName string
	err := h.DB.Pool.QueryRow(ctx, `
		SELECT id, COALESCE(trade_name, legal_name) FROM merchants WHERE code = $1 AND status = 'active'`,
		strings.TrimSpace(req.MerchantCode)).Scan(&merchantID, &merchantName)
	if errors.Is(err, pgx.ErrNoRows) {
		invalid()
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not pair")
		return
	}

	var t terminalResponse
	err = h.DB.WithTenant(ctx, merchantID, func(ctx context.Context, tx pgx.Tx) error {
		// Claim the code atomically: the UPDATE only matches a live code on
		// an active terminal in an active branch, and clears it, so the
		// same code can never pair two devices.
		var id string
		err := tx.QueryRow(ctx, `
			UPDATE pos_terminals t
			SET device_fingerprint = $3, paired_at = now(), pairing_code_hash = NULL,
			    pairing_expires_at = NULL, updated_at = now()
			FROM branches b
			WHERE t.merchant_id = $1 AND t.pairing_code_hash = $2 AND t.pairing_expires_at > now()
			  AND t.status = 'active' AND t.channel = 'pos'
			  AND b.id = t.branch_id AND b.status = 'active'
			RETURNING t.id`, merchantID, hashCode(req.PairingCode), strings.TrimSpace(req.DeviceFingerprint)).Scan(&id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errNotFound
			}
			return err
		}
		t, err = loadTerminal(ctx, tx, merchantID, id)
		return err
	})
	if errors.Is(err, errNotFound) {
		invalid()
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not pair")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"terminal_id":   t.TerminalID,
		"terminal_name": t.Name,
		"branch_id":     t.BranchID,
		"branch_name":   t.BranchName,
		"merchant_code": strings.TrimSpace(req.MerchantCode),
		"merchant_name": merchantName,
	})
}

// respondErr writes the shared error responses; reports whether it did.
func respondErr(w http.ResponseWriter, err error, internalMsg string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, errNotFound):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "terminal or branch not found")
	case errors.Is(err, errBranchInactive):
		httpx.Error(w, http.StatusConflict, "BRANCH_INACTIVE", "this branch is inactive — reactivate it first")
	case errors.Is(err, errTermInactive):
		httpx.Error(w, http.StatusConflict, "TERMINAL_INACTIVE", "this terminal is inactive — reactivate it first")
	default:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", internalMsg)
	}
	return true
}
