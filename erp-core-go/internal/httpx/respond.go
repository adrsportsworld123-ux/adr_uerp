// Package httpx holds the tiny set of JSON response helpers shared by every
// handler package, so every endpoint in the system returns errors in
// exactly the shape documented in phase0_1_design.md §3:
//
//	{ "error": { "code": "...", "message": "...", "details": {} } }
package httpx

import (
	"encoding/json"
	"net/http"
)

func JSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, map[string]apiError{"error": {Code: code, Message: message}})
}

func ErrorWithDetails(w http.ResponseWriter, status int, code, message string, details any) {
	JSON(w, status, map[string]apiError{"error": {Code: code, Message: message, Details: details}})
}
