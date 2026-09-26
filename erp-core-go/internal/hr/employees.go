// Package hr implements Phase 5's employee records, shift management, and
// attendance (phased_roadmap.md; pos_frd_complete.md's HR section).
//
// There is no separate "employees" table — `users` (migrations/001_schema.sql)
// already is this system's staff record (employee_code, name, phone,
// email, branch_id, PIN/password login), and there was no way to create
// one outside a migration before this package: POST /hr/employees is the
// first user-creation endpoint this codebase has ever had. See
// migrations/023_hr_payroll.sql's header comment for the full reasoning.
package hr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

type Handler struct {
	DB *db.DB
}

var errEmployeeCodeExists = errors.New("employee code already in use")

type employeeResponse struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Email          string   `json:"email,omitempty"`
	Phone          string   `json:"phone,omitempty"`
	EmployeeCode   string   `json:"employee_code"`
	BranchID       *string  `json:"branch_id,omitempty"`
	Status         string   `json:"status"`
	Designation    string   `json:"designation,omitempty"`
	Department     string   `json:"department,omitempty"`
	EmploymentType string   `json:"employment_type"`
	DateOfJoining  string   `json:"date_of_joining,omitempty"`
	DateOfExit     string   `json:"date_of_exit,omitempty"`
	PANNumber      string   `json:"pan_number,omitempty"`
	UANNumber      string   `json:"uan_number,omitempty"`
	ESINumber      string   `json:"esi_number,omitempty"`
	BankAccountNo  string   `json:"bank_account_no,omitempty"`
	BankIFSC       string   `json:"bank_ifsc,omitempty"`
	ShiftID        *string  `json:"shift_id,omitempty"`
	Roles          []string `json:"roles"`
}

type createEmployeeRequest struct {
	Name           string  `json:"name"`
	Email          string  `json:"email"`
	Phone          string  `json:"phone"`
	EmployeeCode   string  `json:"employee_code"`
	BranchID       *string `json:"branch_id"`
	RoleID         string  `json:"role_id"`
	Designation    string  `json:"designation"`
	Department     string  `json:"department"`
	EmploymentType string  `json:"employment_type"`
	DateOfJoining  string  `json:"date_of_joining"`
	PANNumber      string  `json:"pan_number"`
	UANNumber      string  `json:"uan_number"`
	ESINumber      string  `json:"esi_number"`
	BankAccountNo  string  `json:"bank_account_no"`
	BankIFSC       string  `json:"bank_ifsc"`
	PIN            string  `json:"pin"`
	Password       string  `json:"password"`
}

// CreateEmployee: POST /hr/employees — the first user-creation endpoint
// this codebase has had (see this file's package comment). Login
// credentials (pin/password) are optional at creation — an employee with
// neither is a real, valid row (e.g. warehouse staff with no system
// access); one can be set later via the existing PIN-login/password-reset
// paths once the employee actually needs to log in.
func (h *Handler) CreateEmployee(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req createEmployeeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.Name == "" || req.EmployeeCode == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "name and employee_code are required")
		return
	}
	if req.EmploymentType == "" {
		req.EmploymentType = "full_time"
	}

	var pinHash, passwordHash *string
	if req.PIN != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(req.PIN), bcrypt.DefaultCost)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not hash pin")
			return
		}
		s := string(h)
		pinHash = &s
	}
	if req.Password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not hash password")
			return
		}
		s := string(h)
		passwordHash = &s
	}

	var resp employeeResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		var userID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO users (id, merchant_id, branch_id, name, email, phone, employee_code, password_hash, pin_hash,
			                    designation, department, employment_type, date_of_joining, pan_number, uan_number, esi_number,
			                    bank_account_no, bank_ifsc)
			VALUES (gen_random_uuid(), current_setting('app.tenant_id')::uuid, $1, $2, NULLIF($3,''), NULLIF($4,''), $5, $6, $7,
			        NULLIF($8,''), NULLIF($9,''), $10, NULLIF($11,'')::date, NULLIF($12,''), NULLIF($13,''), NULLIF($14,''),
			        NULLIF($15,''), NULLIF($16,''))
			RETURNING id`,
			req.BranchID, req.Name, req.Email, req.Phone, req.EmployeeCode, passwordHash, pinHash,
			req.Designation, req.Department, req.EmploymentType, req.DateOfJoining, req.PANNumber, req.UANNumber, req.ESINumber,
			req.BankAccountNo, req.BankIFSC,
		).Scan(&userID); err != nil {
			if isUniqueViolation(err) {
				return errEmployeeCodeExists
			}
			return err
		}

		if req.RoleID != "" {
			if _, err := tx.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)`, userID, req.RoleID); err != nil {
				return err
			}
		}

		return loadEmployee(ctx, tx, userID, &resp)
	})

	switch {
	case errors.Is(err, errEmployeeCodeExists):
		httpx.Error(w, http.StatusConflict, "EMPLOYEE_CODE_EXISTS", "this employee_code is already in use")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create employee")
	default:
		httpx.JSON(w, http.StatusCreated, resp)
	}
}

// ListEmployees: GET /hr/employees?status=active|exited&branch_id=
func (h *Handler) ListEmployees(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	status := r.URL.Query().Get("status")
	branchID := r.URL.Query().Get("branch_id")

	var employees []employeeResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id FROM users
			WHERE ($1 = '' OR ($1 = 'active' AND date_of_exit IS NULL) OR ($1 = 'exited' AND date_of_exit IS NOT NULL))
			  AND ($2 = '' OR branch_id::text = $2)
			  AND employee_code IS NOT NULL
			ORDER BY name`, status, branchID)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		employees = []employeeResponse{}
		for _, id := range ids {
			var e employeeResponse
			if err := loadEmployee(ctx, tx, id, &e); err != nil {
				return err
			}
			employees = append(employees, e)
		}
		return nil
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not list employees")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"employees": employees})
}

// GetEmployee: GET /hr/employees/{id}
func (h *Handler) GetEmployee(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")

	var resp employeeResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return loadEmployee(ctx, tx, id, &resp)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no employee with this id")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load employee")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

type updateEmployeeRequest struct {
	BranchID       *string `json:"branch_id"`
	Designation    *string `json:"designation"`
	Department     *string `json:"department"`
	EmploymentType *string `json:"employment_type"`
	DateOfExit     *string `json:"date_of_exit"`
	PANNumber      *string `json:"pan_number"`
	UANNumber      *string `json:"uan_number"`
	ESINumber      *string `json:"esi_number"`
	BankAccountNo  *string `json:"bank_account_no"`
	BankIFSC       *string `json:"bank_ifsc"`
	ShiftID        *string `json:"shift_id"`
	RoleID         *string `json:"role_id"`
}

// UpdateEmployee: PATCH /hr/employees/{id} — partial update; only fields
// present in the request body are touched (COALESCE against the current
// value), the same pattern PATCH /products/{id} and PATCH /branches/{id}
// already use. Setting date_of_exit also flips status to 'inactive' in
// the same statement — an exited employee's login is disabled as part of
// recording the exit, not a separate step an operator could forget.
func (h *Handler) UpdateEmployee(w http.ResponseWriter, r *http.Request) {
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	var req updateEmployeeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}

	var resp employeeResponse
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE users SET
			  branch_id = COALESCE($2, branch_id),
			  designation = COALESCE($3, designation),
			  department = COALESCE($4, department),
			  employment_type = COALESCE($5, employment_type),
			  date_of_exit = COALESCE($6::date, date_of_exit),
			  status = CASE WHEN $6::date IS NOT NULL THEN 'inactive' ELSE status END,
			  pan_number = COALESCE($7, pan_number),
			  uan_number = COALESCE($8, uan_number),
			  esi_number = COALESCE($9, esi_number),
			  bank_account_no = COALESCE($10, bank_account_no),
			  bank_ifsc = COALESCE($11, bank_ifsc),
			  shift_id = COALESCE($12::uuid, shift_id)
			WHERE id = $1`,
			id, req.BranchID, req.Designation, req.Department, req.EmploymentType, req.DateOfExit,
			req.PANNumber, req.UANNumber, req.ESINumber, req.BankAccountNo, req.BankIFSC, req.ShiftID)
		if err != nil {
			return err
		}
		if req.RoleID != nil && *req.RoleID != "" {
			if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)`, id, *req.RoleID); err != nil {
				return err
			}
		}
		return loadEmployee(ctx, tx, id, &resp)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no employee with this id")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update employee")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func loadEmployee(ctx context.Context, tx pgx.Tx, id string, resp *employeeResponse) error {
	if err := tx.QueryRow(ctx, `
		SELECT id, name, COALESCE(email,''), COALESCE(phone,''), COALESCE(employee_code,''), branch_id, status,
		       COALESCE(designation,''), COALESCE(department,''), employment_type,
		       COALESCE(date_of_joining::text,''), COALESCE(date_of_exit::text,''),
		       COALESCE(pan_number,''), COALESCE(uan_number,''), COALESCE(esi_number,''),
		       COALESCE(bank_account_no,''), COALESCE(bank_ifsc,''), shift_id
		FROM users WHERE id = $1`, id,
	).Scan(&resp.ID, &resp.Name, &resp.Email, &resp.Phone, &resp.EmployeeCode, &resp.BranchID, &resp.Status,
		&resp.Designation, &resp.Department, &resp.EmploymentType,
		&resp.DateOfJoining, &resp.DateOfExit, &resp.PANNumber, &resp.UANNumber, &resp.ESINumber,
		&resp.BankAccountNo, &resp.BankIFSC, &resp.ShiftID); err != nil {
		return err
	}

	rows, err := tx.Query(ctx, `SELECT r.name FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE ur.user_id = $1`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	resp.Roles = []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		resp.Roles = append(resp.Roles, name)
	}
	return rows.Err()
}

// isUniqueViolation matches internal/catalog/barcode_assign.go's own
// identical helper.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
