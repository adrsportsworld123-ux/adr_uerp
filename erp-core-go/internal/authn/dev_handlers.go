package authn

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

// ---------------------------------------------------------------------
// Dev-only password tooling. Neither handler is wired into the router
// (see NewRouter in internal/httpserver/router.go) unless the service is
// started with DEV_AUTH_TOOLS_ENABLED=true — off by default, and it must
// stay off anywhere but a local/dev environment. See the package comment
// on why, and README.md for the full explanation you should read before
// turning this on anywhere that isn't your own machine.
//
// Rationale for what's public and what isn't:
//   - HashPasswordHandler computes a bcrypt hash of a string the caller
//     already knows and sends back. No account, no stored data, no
//     tenant is ever touched — it can't leak anything it wasn't already
//     given. Its only real cost is CPU (bcrypt is deliberately slow), so
//     an unauthenticated deployment of it is a rate-limiting/DoS concern,
//     not a data-exposure one. Still gated by the same flag, since a
//     public bcrypt oracle serves no purpose once you have real users.
//   - SetPasswordHandler is the dangerous one to expose without auth: an
//     endpoint that changes any known account's password without proving
//     who's asking is a textbook account-takeover primitive the moment
//     real users exist. It stays behind the same flag for that reason —
//     this is a seed/dev convenience, not a "forgot password" flow. A
//     real forgot-password flow (emailed, time-limited, single-use reset
//     token) is a Phase 1+ item; don't ship this endpoint enabled anywhere
//     that isn't your own machine.
// ---------------------------------------------------------------------

type HashPasswordHandler struct{}

type hashPasswordRequest struct {
	Password string `json:"password"`
}

type hashPasswordResponse struct {
	Hash string `json:"hash"`
}

// ServeHTTP: POST /dev/hash-password
// Body:   { "password": "Passw0rd!" }
// 200:    { "hash": "$2a$10$..." }
// Pure computation — accepts a plaintext password, returns its bcrypt
// hash. Does not read or write the database. Use the returned hash in a
// manual UPDATE, or via SetPasswordHandler below.
func (h *HashPasswordHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req hashPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if len(req.Password) == 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "password is required")
		return
	}
	// bcrypt silently truncates input beyond 72 bytes rather than erroring —
	// reject up front so a caller never gets a hash of a password shorter
	// than the one they thought they were hashing.
	if len(req.Password) > 72 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "password must be 72 bytes or fewer (bcrypt's limit)")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not generate hash")
		return
	}

	httpx.JSON(w, http.StatusOK, hashPasswordResponse{Hash: string(hash)})
}

type SetPasswordHandler struct {
	DB *db.DB
}

type setPasswordRequest struct {
	MerchantCode string `json:"merchant_code"`
	Email        string `json:"email"`
	NewPassword  string `json:"new_password"`
}

type setPasswordResponse struct {
	Status string `json:"status"`
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

// ServeHTTP: POST /dev/set-password
// Body:   { "merchant_code": "acme-sports", "email": "ravi@acme-sports.test", "new_password": "Passw0rd!" }
// 200:    { "status": "ok", "user_id": "...", "email": "ravi@acme-sports.test" }
// 404:    unknown merchant_code or email — deliberately vague (same
//
//	INVALID_REQUEST-shaped 404, not "which one was wrong") so this
//	endpoint isn't itself a tool for discovering which accounts
//	exist, even in a dev environment.
//
// Hashes new_password with the same bcrypt.DefaultCost the login handler
// verifies against, then updates that one user's password_hash inside a
// WithTenant transaction — the same tenant-isolation path every other
// tenant-scoped write in this codebase uses (see db.WithTenant).
func (h *SetPasswordHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req setPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "could not parse request body")
		return
	}
	if req.MerchantCode == "" || req.Email == "" || req.NewPassword == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "merchant_code, email and new_password are required")
		return
	}
	if len(req.NewPassword) > 72 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "new_password must be 72 bytes or fewer (bcrypt's limit)")
		return
	}

	ctx := r.Context()

	var tenantID string
	err := h.DB.Pool.QueryRow(ctx, `SELECT id FROM merchants WHERE code = $1 AND status = 'active'`, req.MerchantCode).Scan(&tenantID)
	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no matching merchant_code/email")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not resolve merchant")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not generate hash")
		return
	}

	var userID string
	err = h.DB.WithTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			UPDATE users SET password_hash = $1
			WHERE email = $2
			RETURNING id`, string(hash), req.Email).Scan(&userID)
	})
	if err == pgx.ErrNoRows {
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no matching merchant_code/email")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not update password")
		return
	}

	httpx.JSON(w, http.StatusOK, setPasswordResponse{Status: "ok", UserID: userID, Email: req.Email})
}
