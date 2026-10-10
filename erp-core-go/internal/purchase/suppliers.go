// Package purchase implements Phase 2's Purchase Management sub-area
// (phased_roadmap.md; pos_frd_complete.md §9): supplier management, direct
// GRN → Bill (no formal PO workflow — see migrations/006_purchase.sql's
// header comment for why), and purchase returns.
package purchase

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB *db.DB
}

type supplierRequest struct {
	Name         string  `json:"name"`
	LegalName    string  `json:"legal_name"`
	GSTIN        string  `json:"gstin"`
	ContactName  string  `json:"contact_name"`
	Email        string  `json:"email"`
	Phone        string  `json:"phone"`
	PaymentTerms string  `json:"payment_terms"`
	CreditLimit  float64 `json:"credit_limit"`
}

type supplierResponse struct {
	SupplierID   string `json:"supplier_id"`
	Name         string `json:"name"`
	LegalName    string `json:"legal_name"`
	GSTIN        string `json:"gstin"`
	ContactName  string `json:"contact_name"`
	Email        string `json:"email"`
	Phone        string `json:"phone"`
	PaymentTerms string `json:"payment_terms"`
	CreditLimit  string `json:"credit_limit"`
	Status       string `json:"status"`
}

// CreateSupplier: POST /suppliers
func (h *Handler) CreateSupplier(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req supplierRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name is required")
		return
	}

	var resp supplierResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO suppliers (id, merchant_id, name, legal_name, gstin, contact_name, email, phone, payment_terms, credit_limit)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id, name, COALESCE(legal_name,''), COALESCE(gstin,''), COALESCE(contact_name,''),
			          COALESCE(email,''), COALESCE(phone,''), COALESCE(payment_terms,''), credit_limit::text, status`,
			req.Name, req.LegalName, req.GSTIN, req.ContactName, req.Email, req.Phone, req.PaymentTerms, req.CreditLimit,
		).Scan(&resp.SupplierID, &resp.Name, &resp.LegalName, &resp.GSTIN, &resp.ContactName,
			&resp.Email, &resp.Phone, &resp.PaymentTerms, &resp.CreditLimit, &resp.Status)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create supplier")
		return
	}
	httpx.JSON(w, http.StatusCreated, resp)
}

// ListSuppliers: GET /suppliers?q=&status=&sort=name|gstin|credit_limit|updated&dir=&page=&limit=
// q matches name, legal name, GSTIN, contact, phone or email. Unpaged
// (every row) unless page/limit is given, which the GRN/bill supplier
// pickers rely on.
func (h *Handler) ListSuppliers(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	p := httpx.ParseListParams(r, map[string]string{
		"name": "name", "gstin": "gstin", "credit_limit": "credit_limit", "updated": "updated_at",
	}, "name")
	status := p.Status
	if status == "all" {
		status = ""
	}
	if status != "" && status != "active" && status != "inactive" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be all, active, or inactive")
		return
	}
	limit, offset := p.SQLLimit()

	suppliers := []supplierResponse{}
	total := 0
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		where := `
			WHERE ($1 = '' OR name ILIKE '%' || $1 || '%' OR legal_name ILIKE '%' || $1 || '%'
			       OR gstin ILIKE '%' || $1 || '%' OR contact_name ILIKE '%' || $1 || '%'
			       OR phone ILIKE '%' || $1 || '%' OR email ILIKE '%' || $1 || '%')
			  AND ($2 = '' OR status = $2)`
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM suppliers`+where, p.Q, status).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT id, name, COALESCE(legal_name,''), COALESCE(gstin,''), COALESCE(contact_name,''),
			       COALESCE(email,''), COALESCE(phone,''), COALESCE(payment_terms,''), credit_limit::text, status
			FROM suppliers`+where+` ORDER BY `+p.OrderBy+`, id LIMIT $3 OFFSET $4`, p.Q, status, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s supplierResponse
			if err := rows.Scan(&s.SupplierID, &s.Name, &s.LegalName, &s.GSTIN, &s.ContactName,
				&s.Email, &s.Phone, &s.PaymentTerms, &s.CreditLimit, &s.Status); err != nil {
				return err
			}
			suppliers = append(suppliers, s)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list suppliers")
		return
	}
	httpx.JSON(w, http.StatusOK, p.Envelope("suppliers", suppliers, total))
}

// UpdateSupplier: PATCH /suppliers/{id} — partial update (Phase 11): a
// field left out of the body is kept, "" clears an optional text field,
// and credit_limit may be set to any value >= 0, including 0. (Before
// Phase 11 an empty value meant "no change", so nothing could be cleared
// and a credit limit could never be brought back to 0; no client relied
// on that, since no edit screen existed.) Name can't be blanked.
type updateSupplierRequest struct {
	Name         *string  `json:"name"`
	LegalName    *string  `json:"legal_name"`
	GSTIN        *string  `json:"gstin"`
	ContactName  *string  `json:"contact_name"`
	Email        *string  `json:"email"`
	Phone        *string  `json:"phone"`
	PaymentTerms *string  `json:"payment_terms"`
	CreditLimit  *float64 `json:"credit_limit"`
	Status       *string  `json:"status"`
}

func trimmedPtr(p *string) *string {
	if p == nil {
		return nil
	}
	t := strings.TrimSpace(*p)
	return &t
}

func (h *Handler) UpdateSupplier(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	supplierID := chi.URLParam(r, "id")

	var req updateSupplierRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	req.Name, req.LegalName, req.GSTIN = trimmedPtr(req.Name), trimmedPtr(req.LegalName), trimmedPtr(req.GSTIN)
	req.ContactName, req.Email, req.Phone, req.PaymentTerms = trimmedPtr(req.ContactName), trimmedPtr(req.Email), trimmedPtr(req.Phone), trimmedPtr(req.PaymentTerms)
	if req.Name != nil && *req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name can't be empty")
		return
	}
	if req.CreditLimit != nil && *req.CreditLimit < 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "credit_limit can't be negative")
		return
	}
	if req.Status != nil && *req.Status != "active" && *req.Status != "inactive" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be active or inactive")
		return
	}

	var resp supplierResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		// For optional text: NULL param = keep; '' = clear (stored NULL).
		return tx.QueryRow(ctx, `
			UPDATE suppliers SET
			  name          = COALESCE($1, name),
			  legal_name    = CASE WHEN $2::text IS NULL THEN legal_name    ELSE NULLIF($2, '') END,
			  gstin         = CASE WHEN $3::text IS NULL THEN gstin         ELSE NULLIF($3, '') END,
			  contact_name  = CASE WHEN $4::text IS NULL THEN contact_name  ELSE NULLIF($4, '') END,
			  email         = CASE WHEN $5::text IS NULL THEN email         ELSE NULLIF($5, '') END,
			  phone         = CASE WHEN $6::text IS NULL THEN phone         ELSE NULLIF($6, '') END,
			  payment_terms = CASE WHEN $7::text IS NULL THEN payment_terms ELSE NULLIF($7, '') END,
			  credit_limit  = COALESCE($8::numeric, credit_limit),
			  status        = COALESCE($10, status),
			  updated_at    = now()
			WHERE id = $9
			RETURNING id, name, COALESCE(legal_name,''), COALESCE(gstin,''), COALESCE(contact_name,''),
			          COALESCE(email,''), COALESCE(phone,''), COALESCE(payment_terms,''), credit_limit::text, status`,
			req.Name, req.LegalName, req.GSTIN, req.ContactName, req.Email, req.Phone, req.PaymentTerms, req.CreditLimit, supplierID, req.Status,
		).Scan(&resp.SupplierID, &resp.Name, &resp.LegalName, &resp.GSTIN, &resp.ContactName,
			&resp.Email, &resp.Phone, &resp.PaymentTerms, &resp.CreditLimit, &resp.Status)
	})
	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "supplier not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update supplier")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
