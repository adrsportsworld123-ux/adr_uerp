// Package customers implements Phase 3's Customer Management sub-area
// (phased_roadmap.md; pos_frd_complete.md §6), narrowed to what the
// roadmap actually asks for this pass — see migrations/011_customers.sql's
// header comment for what's deliberately deferred (credit facility,
// loyalty points, customer-level discounts).
package customers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB *db.DB
}

// segmentedCustomersCTE computes VIP/Regular/New/Dormant auto-segmentation
// (pos_frd_complete.md §6) on read from real sales_orders history, the
// one and only definition of the rule — both ListCustomers and
// GetCustomer select from this, so there is nowhere for the two to drift
// apart. Precedence matters: a customer who hasn't bought anything in 6+
// months is "dormant" even if their lifetime spend would otherwise make
// them "vip" (FRD lists Dormant as its own bucket, so a customer lands in
// exactly one segment); a customer with zero orders ever is "new", not
// "dormant" — dormancy describes someone who WAS buying and stopped.
const segmentedCustomersCTE = `
	WITH stats AS (
		SELECT customer_id,
		       SUM(grand_total) AS total_spend,
		       COUNT(*) AS transaction_count,
		       MAX(finalized_at) AS last_purchase_at
		FROM sales_orders
		WHERE status = 'finalized' AND customer_id IS NOT NULL
		GROUP BY customer_id
	),
	segmented AS (
		SELECT c.*,
		       COALESCE(s.total_spend, 0::numeric(14,2)) AS total_spend,
		       COALESCE(s.transaction_count, 0) AS transaction_count,
		       s.last_purchase_at,
		       CASE
		           WHEN COALESCE(s.transaction_count, 0) = 0 THEN 'new'
		           WHEN s.last_purchase_at < now() - interval '6 months' THEN 'dormant'
		           WHEN COALESCE(s.total_spend, 0) > 100000 OR s.transaction_count > 50 THEN 'vip'
		           WHEN s.transaction_count >= 10 THEN 'regular'
		           ELSE 'new'
		       END AS segment
		FROM customers c
		LEFT JOIN stats s ON s.customer_id = c.id
	)
`

type customerResponse struct {
	CustomerID       string  `json:"customer_id"`
	Name             string  `json:"name"`
	Phone            string  `json:"phone"`
	Email            string  `json:"email"`
	CustomerType     string  `json:"customer_type"`
	Address          string  `json:"address"`
	DateOfBirth      *string `json:"date_of_birth"`
	Anniversary      *string `json:"anniversary"`
	CompanyName      string  `json:"company_name"`
	GSTIN            string  `json:"gstin"`
	Status           string  `json:"status"`
	TotalSpend       string  `json:"total_spend"`
	TransactionCount int     `json:"transaction_count"`
	LastPurchaseAt   *string `json:"last_purchase_at"`
	Segment          string  `json:"segment"`
}

func scanCustomerRow(row pgx.Row) (customerResponse, error) {
	var c customerResponse
	err := row.Scan(
		&c.CustomerID, &c.Name, &c.Phone, &c.Email, &c.CustomerType,
		&c.Address, &c.DateOfBirth, &c.Anniversary, &c.CompanyName, &c.GSTIN, &c.Status,
		&c.TotalSpend, &c.TransactionCount, &c.LastPurchaseAt, &c.Segment,
	)
	return c, err
}

const customerColumns = `
	id, COALESCE(name,''), COALESCE(phone,''), COALESCE(email,''), customer_type,
	COALESCE(address,''), date_of_birth::text, anniversary::text, COALESCE(company_name,''), COALESCE(gstin,''), status,
	total_spend::text, transaction_count, last_purchase_at::text, segment
`

// ---------------------------------------------------------------------
// POST /customers — full CRM registration (distinct from the lenient
// walk-in creation POST /sales/orders/{id}/customer still does inline —
// that path is unchanged and stays deliberately minimal for a quick
// at-register walk-in add). This is the FRD's actual registration flow:
// Name/Phone/Email are mandatory, and a B2B customer additionally
// requires a GSTIN ("B2B: Mandatory registration, GSTIN required").
// ---------------------------------------------------------------------

type createCustomerRequest struct {
	Name         string `json:"name"`
	Phone        string `json:"phone"`
	Email        string `json:"email"`
	CustomerType string `json:"customer_type"` // "b2c" (default) or "b2b"
	Address      string `json:"address"`
	DateOfBirth  string `json:"date_of_birth"` // "YYYY-MM-DD", optional
	Anniversary  string `json:"anniversary"`   // "YYYY-MM-DD", optional
	CompanyName  string `json:"company_name"`
	GSTIN        string `json:"gstin"`
}

func (h *Handler) CreateCustomer(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createCustomerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.CustomerType == "" {
		req.CustomerType = "b2c"
	}
	if req.CustomerType != "b2c" && req.CustomerType != "b2b" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "customer_type must be b2c or b2b")
		return
	}
	if req.Name == "" || req.Phone == "" || req.Email == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name, phone, and email are required")
		return
	}
	if req.CustomerType == "b2b" && req.GSTIN == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "gstin is required for a b2b customer")
		return
	}

	var customerID string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO customers (id, merchant_id, name, phone, email, customer_type, address, date_of_birth, anniversary, company_name, gstin)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, $3, $4, $5, NULLIF($6,'')::date, NULLIF($7,'')::date, $8, $9)
			RETURNING id`,
			req.Name, req.Phone, req.Email, req.CustomerType, req.Address, req.DateOfBirth, req.Anniversary, req.CompanyName, req.GSTIN,
		).Scan(&customerID)
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create customer")
		return
	}

	customer, err := h.fetchByID(r.Context(), claims.TenantID, customerID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "customer created but could not be reloaded")
		return
	}
	httpx.JSON(w, http.StatusCreated, customer)
}

// ---------------------------------------------------------------------
// GET /customers?q=&customer_type=&segment=&page=&limit= — search/list
// with auto-segmentation, filterable by the exact segment values
// segmentedCustomersCTE computes (vip/regular/new/dormant).
// ---------------------------------------------------------------------

func (h *Handler) ListCustomers(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	q := r.URL.Query()
	search := q.Get("q")
	customerType := q.Get("customer_type")
	segment := q.Get("segment")

	limit := 50
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 && l <= 200 {
		limit = l
	}
	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}
	offset := (page - 1) * limit

	customerList := []customerResponse{}
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, segmentedCustomersCTE+`
			SELECT `+customerColumns+`
			FROM segmented
			WHERE ($1 = '' OR name ILIKE '%' || $1 || '%' OR phone ILIKE '%' || $1 || '%' OR email ILIKE '%' || $1 || '%')
			  AND ($2 = '' OR customer_type = $2)
			  AND ($3 = '' OR segment = $3)
			ORDER BY name NULLS LAST
			LIMIT $4 OFFSET $5`,
			search, customerType, segment, limit, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCustomerRow(rows)
			if err != nil {
				return err
			}
			customerList = append(customerList, c)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list customers")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"customers": customerList, "page": page, "limit": limit})
}

// ---------------------------------------------------------------------
// GET /customers/{id} — full profile + segment + recent purchase history.
// The history query has no branch filter, deliberately: FRD §6's
// "Multi-location: Profile accessible at all branches, Purchase history
// consolidated" is already true of this schema (customers and
// sales_orders are merchant-scoped, not branch-scoped) — this endpoint
// just needs to not accidentally narrow that back down to one branch,
// so it includes each order's branch name to make that visible.
// ---------------------------------------------------------------------

type orderHistoryEntry struct {
	OrderNumber string `json:"order_number"`
	BranchName  string `json:"branch_name"`
	FinalizedAt string `json:"finalized_at"`
	GrandTotal  string `json:"grand_total"`
}

type customerDetailResponse struct {
	customerResponse
	RecentOrders []orderHistoryEntry `json:"recent_orders"`
}

func (h *Handler) GetCustomer(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	customerID := chi.URLParam(r, "id")

	customer, err := h.fetchByID(r.Context(), claims.TenantID, customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "customer not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load customer")
		return
	}

	resp := customerDetailResponse{customerResponse: customer, RecentOrders: []orderHistoryEntry{}}
	err = h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT so.order_number, b.name, so.finalized_at::text, so.grand_total::text
			FROM sales_orders so
			JOIN branches b ON b.id = so.branch_id
			WHERE so.customer_id = $1 AND so.status = 'finalized'
			ORDER BY so.finalized_at DESC
			LIMIT 20`, customerID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var o orderHistoryEntry
			if err := rows.Scan(&o.OrderNumber, &o.BranchName, &o.FinalizedAt, &o.GrandTotal); err != nil {
				return err
			}
			resp.RecentOrders = append(resp.RecentOrders, o)
		}
		return rows.Err()
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load purchase history")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------
// PATCH /customers/{id} — partial update. Same b2b-requires-gstin rule
// as creation, checked against the post-update state (changing type to
// b2b without a gstin already on file is rejected the same as creating
// one that way).
// ---------------------------------------------------------------------

type updateCustomerRequest struct {
	Name         *string `json:"name"`
	Phone        *string `json:"phone"`
	Email        *string `json:"email"`
	CustomerType *string `json:"customer_type"`
	Address      *string `json:"address"`
	DateOfBirth  *string `json:"date_of_birth"`
	Anniversary  *string `json:"anniversary"`
	CompanyName  *string `json:"company_name"`
	GSTIN        *string `json:"gstin"`
	Status       *string `json:"status"`
}

func (h *Handler) UpdateCustomer(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	customerID := chi.URLParam(r, "id")

	var req updateCustomerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.CustomerType != nil && *req.CustomerType != "b2c" && *req.CustomerType != "b2b" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "customer_type must be b2c or b2b")
		return
	}

	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var currentType, currentGSTIN string
		if err := tx.QueryRow(ctx, `SELECT customer_type, COALESCE(gstin,'') FROM customers WHERE id = $1`, customerID).
			Scan(&currentType, &currentGSTIN); err != nil {
			return err
		}
		newType, newGSTIN := currentType, currentGSTIN
		if req.CustomerType != nil {
			newType = *req.CustomerType
		}
		if req.GSTIN != nil {
			newGSTIN = *req.GSTIN
		}
		if newType == "b2b" && newGSTIN == "" {
			return errB2BNeedsGSTIN
		}

		_, err := tx.Exec(ctx, `
			UPDATE customers SET
				name = COALESCE($1, name),
				phone = COALESCE($2, phone),
				email = COALESCE($3, email),
				customer_type = COALESCE($4, customer_type),
				address = COALESCE($5, address),
				date_of_birth = COALESCE(NULLIF($6,'')::date, date_of_birth),
				anniversary = COALESCE(NULLIF($7,'')::date, anniversary),
				company_name = COALESCE($8, company_name),
				gstin = COALESCE($9, gstin),
				status = COALESCE($10, status),
				updated_at = now()
			WHERE id = $11`,
			req.Name, req.Phone, req.Email, req.CustomerType, req.Address,
			req.DateOfBirth, req.Anniversary, req.CompanyName, req.GSTIN, req.Status, customerID)
		return err
	})

	switch {
	case errors.Is(err, errB2BNeedsGSTIN):
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "gstin is required for a b2b customer")
	case errors.Is(err, pgx.ErrNoRows):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "customer not found")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update customer")
	default:
		customer, err := h.fetchByID(r.Context(), claims.TenantID, customerID)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "customer updated but could not be reloaded")
			return
		}
		httpx.JSON(w, http.StatusOK, customer)
	}
}

var errB2BNeedsGSTIN = errors.New("gstin is required for a b2b customer")

// FetchSegment returns customerID's auto-computed segment (vip/regular/
// new/dormant) from inside a caller's own transaction — used by
// internal/promotions to evaluate a promotion's target_segment (the
// discount hierarchy's "customer-level discounts" layer) without
// duplicating segmentedCustomersCTE's rule in a second place. Returns
// ("", pgx.ErrNoRows) for an unknown customer id, same as every other
// lookup in this codebase.
func FetchSegment(ctx context.Context, tx pgx.Tx, customerID string) (string, error) {
	var segment string
	err := tx.QueryRow(ctx, segmentedCustomersCTE+`SELECT segment FROM segmented WHERE id = $1`, customerID).Scan(&segment)
	return segment, err
}

func (h *Handler) fetchByID(ctx context.Context, tenantID, customerID string) (customerResponse, error) {
	var c customerResponse
	err := h.DB.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, segmentedCustomersCTE+`SELECT `+customerColumns+` FROM segmented WHERE id = $1`, customerID)
		var scanErr error
		c, scanErr = scanCustomerRow(row)
		return scanErr
	})
	return c, err
}
