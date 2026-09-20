// Package purchase implements Phase 2's Purchase Management sub-area
// (phased_roadmap.md; pos_frd_complete.md §9): supplier management, direct
// GRN → Bill (no formal PO workflow — see migrations/006_purchase.sql's
// header comment for why), and purchase returns.
package purchase

import (
	"context"
	"encoding/json"
	"net/http"

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

// ListSuppliers: GET /suppliers
func (h *Handler) ListSuppliers(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}

	suppliers := []supplierResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, name, COALESCE(legal_name,''), COALESCE(gstin,''), COALESCE(contact_name,''),
			       COALESCE(email,''), COALESCE(phone,''), COALESCE(payment_terms,''), credit_limit::text, status
			FROM suppliers ORDER BY name`)
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
	httpx.JSON(w, http.StatusOK, map[string]any{"suppliers": suppliers})
}

// UpdateSupplier: PATCH /suppliers/{id} — partial update; a field left as
// its zero value in the request is treated as "no change" (COALESCE
// against NULLIF), same convention as PATCH .../lines using an explicit
// field rather than a merge-patch body.
func (h *Handler) UpdateSupplier(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	supplierID := chi.URLParam(r, "id")

	var req supplierRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}

	var resp supplierResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			UPDATE suppliers SET
			  name = COALESCE(NULLIF($1, ''), name),
			  legal_name = COALESCE(NULLIF($2, ''), legal_name),
			  gstin = COALESCE(NULLIF($3, ''), gstin),
			  contact_name = COALESCE(NULLIF($4, ''), contact_name),
			  email = COALESCE(NULLIF($5, ''), email),
			  phone = COALESCE(NULLIF($6, ''), phone),
			  payment_terms = COALESCE(NULLIF($7, ''), payment_terms),
			  credit_limit = CASE WHEN $8 > 0 THEN $8 ELSE credit_limit END,
			  updated_at = now()
			WHERE id = $9
			RETURNING id, name, COALESCE(legal_name,''), COALESCE(gstin,''), COALESCE(contact_name,''),
			          COALESCE(email,''), COALESCE(phone,''), COALESCE(payment_terms,''), credit_limit::text, status`,
			req.Name, req.LegalName, req.GSTIN, req.ContactName, req.Email, req.Phone, req.PaymentTerms, req.CreditLimit, supplierID,
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
