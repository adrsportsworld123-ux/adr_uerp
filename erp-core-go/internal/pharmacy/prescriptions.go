// Package pharmacy implements Phase 8: Vertical Expansion — Pharmacy's
// "prescription linkage" item (phased_roadmap.md Phase 8). A prescription
// is a real record (doctor name/registration number, notes) a merchant
// attaches to a sale — see internal/sales' Checkout for the actual
// enforcement: any line whose variant carries a "Drug Schedule" attribute
// value other than "OTC" (Phase 8's earlier attribute-set mechanism, not
// new schema — see migrations/030_pharmacy.sql's header) requires the
// order to have one attached, refusing checkout otherwise. Drug scheduling
// classification itself, and "stricter expiry compliance"
// (categories.min_shelf_life_days), live in internal/catalog/attributes.go
// and internal/inventory/batches.go respectively — this package is just
// the prescription record itself.
package pharmacy

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB *db.DB
}

type prescriptionResponse struct {
	PrescriptionID string `json:"prescription_id"`
	CustomerID     string `json:"customer_id"`
	DoctorName     string `json:"doctor_name"`
	DoctorRegNo    string `json:"doctor_reg_no"`
	Notes          string `json:"notes"`
	CreatedAt      string `json:"created_at"`
}

type createPrescriptionRequest struct {
	CustomerID  string `json:"customer_id"`
	DoctorName  string `json:"doctor_name"`
	DoctorRegNo string `json:"doctor_reg_no"`
	Notes       string `json:"notes"`
}

// CreatePrescription: POST /prescriptions — open to any authenticated
// user, same tier as recording a sale itself; a prescription is evidence
// a cashier collected at the counter, not a privileged administrative
// action.
func (h *Handler) CreatePrescription(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createPrescriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CustomerID == "" || req.DoctorName == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "customer_id and doctor_name are required")
		return
	}

	var resp prescriptionResponse
	resp.CustomerID = req.CustomerID
	resp.DoctorName = req.DoctorName
	resp.DoctorRegNo = req.DoctorRegNo
	resp.Notes = req.Notes
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var customerExists string
		if err := tx.QueryRow(ctx, `SELECT id FROM customers WHERE id = $1`, req.CustomerID).Scan(&customerExists); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO prescriptions (id, merchant_id, customer_id, doctor_name, doctor_reg_no, notes, created_by)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5)
			RETURNING id, created_at::text`,
			req.CustomerID, req.DoctorName, req.DoctorRegNo, req.Notes, claims.UserID,
		).Scan(&resp.PrescriptionID, &resp.CreatedAt)
	})
	switch {
	case err == pgx.ErrNoRows:
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "customer not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create prescription")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}

// ListPrescriptions: GET /prescriptions?customer_id= — a customer's
// prescription history (e.g. for refill lookups at the counter).
func (h *Handler) ListPrescriptions(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	customerID := r.URL.Query().Get("customer_id")

	list := []prescriptionResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, customer_id, doctor_name, COALESCE(doctor_reg_no,''), COALESCE(notes,''), created_at::text
			FROM prescriptions
			WHERE ($1 = '' OR customer_id::text = $1)
			ORDER BY created_at DESC`, customerID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p prescriptionResponse
			if err := rows.Scan(&p.PrescriptionID, &p.CustomerID, &p.DoctorName, &p.DoctorRegNo, &p.Notes, &p.CreatedAt); err != nil {
				return err
			}
			list = append(list, p)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list prescriptions")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"prescriptions": list})
}
