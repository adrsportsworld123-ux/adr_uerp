package einvoice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
)

// interstateThreshold mirrors the FRD's own wording verbatim ("E-Way Bill
// (auto-generate for interstate >Rs.50k)") — but generation here stays a
// deliberate, explicit action (this handler), not something Checkout
// triggers automatically: an e-way bill needs transporter/vehicle data
// the POS checkout flow never collects today, so "auto-generate" would
// either have to guess that data or block checkout to ask for it — a real
// UX decision, not something to silently invent. requiredByRule in the
// response tells the caller whether this shipment meets the FRD's own
// stated trigger condition, so a future checkout-integration or a
// back-office "orders needing an e-way bill" report can act on it without
// this package guessing what that workflow should look like.
const interstateThreshold = 50000.0

type eWayBillResponse struct {
	SalesOrderID   string `json:"sales_order_id"`
	GSPProvider    string `json:"gsp_provider"`
	EWBNo          string `json:"ewb_no"`
	EWBDate        string `json:"ewb_date"`
	ValidUntil     string `json:"valid_until"`
	VehicleNo      string `json:"vehicle_no"`
	TransporterID  string `json:"transporter_id"`
	DistanceKM     int    `json:"distance_km"`
	Status         string `json:"status"`
	Interstate     bool   `json:"interstate"`
	RequiredByRule bool   `json:"required_by_rule"`
}

type createEWayBillRequest struct {
	VehicleNo     string `json:"vehicle_no"`
	TransporterID string `json:"transporter_id"`
	DistanceKM    int    `json:"distance_km"`
}

// GenerateEWayBill: POST /sales/orders/{id}/e-way-bill — idempotent, same
// reasoning as GenerateEInvoice: a second call against an order that
// already has a 'generated' row returns it rather than calling the GSP
// again.
func (h *Handler) GenerateEWayBill(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")
	var req createEWayBillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}

	var resp eWayBillResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		if existing, err := loadEWayBill(ctx, tx, orderID); err == nil {
			if existing.Status == "cancelled" {
				return errAlreadyCancelled
			}
			resp = existing
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		var status, grandTotal, branchGSTIN, merchantGSTIN string
		var customerID *string
		if err := tx.QueryRow(ctx, `
			SELECT so.status, so.grand_total::text, so.customer_id, COALESCE(b.gstin,''), COALESCE(m.gstin,'')
			FROM sales_orders so
			JOIN branches b ON b.id = so.branch_id
			JOIN merchants m ON m.id = so.merchant_id
			WHERE so.id = $1`, orderID,
		).Scan(&status, &grandTotal, &customerID, &branchGSTIN, &merchantGSTIN); err != nil {
			return err
		}
		if status != "finalized" {
			return errOrderNotFinalized
		}
		supplierGSTIN := branchGSTIN
		if supplierGSTIN == "" {
			supplierGSTIN = merchantGSTIN
		}
		if supplierGSTIN == "" {
			return errSupplierGSTINMissing
		}

		var buyerGSTIN string
		if customerID != nil {
			if err := tx.QueryRow(ctx, `SELECT COALESCE(gstin,'') FROM customers WHERE id = $1`, *customerID).Scan(&buyerGSTIN); err != nil {
				return err
			}
		}

		fromState := stateCode(supplierGSTIN)
		// A B2C customer (no GSTIN on file) has no state this codebase can
		// resolve — same honestly-documented limitation internal/gst's
		// GSTR-1 export already carries for B2C place-of-supply. Treated as
		// intrastate here rather than guessed, so requiredByRule never
		// falsely fires for a walk-in sale this system has no way to know
		// crossed a state line.
		toState := fromState
		if buyerGSTIN != "" {
			toState = stateCode(buyerGSTIN)
		}
		interstate := fromState != "" && toState != "" && fromState != toState

		grandTotalFloat, err := parseMoney(grandTotal)
		if err != nil {
			return err
		}
		requiredByRule := interstate && grandTotalFloat > interstateThreshold

		var irn string
		_ = tx.QueryRow(ctx, `SELECT COALESCE(irn,'') FROM e_invoices WHERE sales_order_id = $1 AND status = 'generated'`, orderID).Scan(&irn)

		ewbResp, err := h.GSP.GenerateEWayBill(ctx, EWayBillRequest{
			SalesOrderID:  orderID,
			IRN:           irn,
			SupplierGSTIN: supplierGSTIN,
			BuyerGSTIN:    buyerGSTIN,
			FromStateCode: fromState,
			ToStateCode:   toState,
			DocumentValue: grandTotalFloat,
			VehicleNo:     req.VehicleNo,
			TransporterID: req.TransporterID,
			DistanceKM:    req.DistanceKM,
		})
		if err != nil {
			return err
		}

		return tx.QueryRow(ctx, `
			INSERT INTO e_way_bills (id, merchant_id, sales_order_id, gsp_provider, ewb_no, ewb_date, valid_until, vehicle_no, transporter_id, distance_km, interstate, required_by_rule, status)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'generated')
			RETURNING sales_order_id::text, gsp_provider, ewb_no, ewb_date::text, valid_until::text,
			          COALESCE(vehicle_no,''), COALESCE(transporter_id,''), COALESCE(distance_km,0), interstate, required_by_rule, status`,
			orderID, string(h.Provider), ewbResp.EWBNo, ewbResp.EWBDate, ewbResp.ValidUntil, req.VehicleNo, req.TransporterID, req.DistanceKM, interstate, requiredByRule,
		).Scan(&resp.SalesOrderID, &resp.GSPProvider, &resp.EWBNo, &resp.EWBDate, &resp.ValidUntil,
			&resp.VehicleNo, &resp.TransporterID, &resp.DistanceKM, &resp.Interstate, &resp.RequiredByRule, &resp.Status)
	})

	switch {
	case errors.Is(err, errOrderNotFinalized):
		httpx.Error(w, http.StatusConflict, "ORDER_NOT_EDITABLE", "order must be finalized before generating an e-way bill")
	case errors.Is(err, errSupplierGSTINMissing):
		httpx.Error(w, http.StatusConflict, "EINVOICE_SUPPLIER_GSTIN_MISSING", errSupplierGSTINMissing.Error())
	case errors.Is(err, errAlreadyCancelled):
		httpx.Error(w, http.StatusConflict, "EWAY_BILL_ALREADY_CANCELLED", "this order's e-way bill was already cancelled and cannot be regenerated")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "order not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not generate e-way bill")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}

// GetEWayBill: GET /sales/orders/{id}/e-way-bill
func (h *Handler) GetEWayBill(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")

	var resp eWayBillResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		resp, err = loadEWayBill(ctx, tx, orderID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no e-way bill for this order")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load e-way bill")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// CancelEWayBill: POST /sales/orders/{id}/e-way-bill/cancel
func (h *Handler) CancelEWayBill(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	orderID := chi.URLParam(r, "id")
	var req cancelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}

	var resp eWayBillResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var ewbNo, status string
		var createdAt time.Time
		if err := tx.QueryRow(ctx, `SELECT ewb_no, status, created_at FROM e_way_bills WHERE sales_order_id = $1`, orderID).
			Scan(&ewbNo, &status, &createdAt); err != nil {
			return err
		}
		if status == "cancelled" {
			return errAlreadyCancelled
		}
		if time.Since(createdAt) > cancelWindow {
			return errCancelWindowExpired
		}
		if err := h.GSP.CancelEWayBill(ctx, ewbNo, req.Reason); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			UPDATE e_way_bills SET status = 'cancelled', cancel_reason = $2, cancelled_at = now()
			WHERE sales_order_id = $1
			RETURNING sales_order_id::text, gsp_provider, ewb_no, ewb_date::text, valid_until::text,
			          COALESCE(vehicle_no,''), COALESCE(transporter_id,''), COALESCE(distance_km,0), interstate, required_by_rule, status`,
			orderID, req.Reason,
		).Scan(&resp.SalesOrderID, &resp.GSPProvider, &resp.EWBNo, &resp.EWBDate, &resp.ValidUntil,
			&resp.VehicleNo, &resp.TransporterID, &resp.DistanceKM, &resp.Interstate, &resp.RequiredByRule, &resp.Status)
	})

	switch {
	case errors.Is(err, errAlreadyCancelled):
		httpx.Error(w, http.StatusConflict, "EWAY_BILL_ALREADY_CANCELLED", "this e-way bill was already cancelled")
	case errors.Is(err, errCancelWindowExpired):
		httpx.Error(w, http.StatusConflict, "EWAY_BILL_CANCEL_WINDOW_EXPIRED", "an e-way bill can only be cancelled within 24 hours of generation")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no e-way bill for this order")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not cancel e-way bill")
	default:
		httpx.JSON(w, http.StatusOK, resp)
	}
}

func loadEWayBill(ctx context.Context, tx pgx.Tx, orderID string) (eWayBillResponse, error) {
	var resp eWayBillResponse
	err := tx.QueryRow(ctx, `
		SELECT sales_order_id::text, gsp_provider, COALESCE(ewb_no,''), COALESCE(ewb_date::text,''), COALESCE(valid_until::text,''),
		       COALESCE(vehicle_no,''), COALESCE(transporter_id,''), COALESCE(distance_km,0), interstate, required_by_rule, status
		FROM e_way_bills WHERE sales_order_id = $1`, orderID,
	).Scan(&resp.SalesOrderID, &resp.GSPProvider, &resp.EWBNo, &resp.EWBDate, &resp.ValidUntil,
		&resp.VehicleNo, &resp.TransporterID, &resp.DistanceKM, &resp.Interstate, &resp.RequiredByRule, &resp.Status)
	return resp, err
}
