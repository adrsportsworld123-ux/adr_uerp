package authn

import (
	"net/http"
	"strings"
)

// RequireAuth validates the Bearer token on every request and injects the
// resulting Claims into the request context. This is the ONLY place in the
// codebase that should read the Authorization header — every downstream
// handler gets its tenant/user/roles from FromContext(ctx), never by
// re-parsing the token itself.
func RequireAuth(issuer *TokenIssuer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(header, prefix) {
				writeAuthError(w, "MISSING_TOKEN", "Authorization: Bearer <token> header is required")
				return
			}

			claims, err := issuer.Parse(strings.TrimPrefix(header, prefix))
			if err != nil {
				writeAuthError(w, "INVALID_TOKEN", "the provided token is invalid or expired")
				return
			}

			ctx := WithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeAuthError(w http.ResponseWriter, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + message + `"}}`))
}
