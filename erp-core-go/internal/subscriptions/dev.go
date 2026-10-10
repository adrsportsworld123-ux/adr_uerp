package subscriptions

import (
	"encoding/json"
	"net/http"

	"golang.org/x/crypto/bcrypt"

	"erp-core-go/internal/db"
	"erp-core-go/internal/httpx"
)

// DevSetPlatformPassword: POST /dev/set-platform-password {email,
// new_password}: the platform-staff twin of /dev/set-password, registered
// only when DEV_AUTH_TOOLS_ENABLED=true (the seeded operator's placeholder
// hash never verifies). Also clears a lockout.
func DevSetPlatformPassword(database *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email       string `json:"email"`
			NewPassword string `json:"new_password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" || req.NewPassword == "" || len(req.NewPassword) > 72 {
			httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "email and new_password (at most 72 bytes) are required")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not hash")
			return
		}
		tag, err := database.Pool.Exec(r.Context(), `UPDATE platform_staff SET password_hash = $2, failed_login_attempts = 0, locked_until = NULL
			WHERE lower(email) = lower($1)`, req.Email, string(hash))
		if err != nil || tag.RowsAffected() == 0 {
			httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "no matching platform staff")
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"status": "ok", "email": req.Email})
	}
}
