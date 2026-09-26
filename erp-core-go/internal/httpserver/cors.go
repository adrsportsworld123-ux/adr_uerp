package httpserver

import "net/http"

// CORS allows a browser-based client (Flutter web today; a future
// erp-web-admin Next.js app tomorrow) to call this API from a different
// origin during local development. Without it, a browser's preflight
// OPTIONS request gets chi's default 405 (no OPTIONS route is registered
// for any path — only GET/POST are), and the browser refuses to send the
// real request at all. A native Android/iOS/desktop client never triggers
// this: CORS is a browser same-origin-policy mechanism, not a server
// permission model, so this middleware is invisible to those clients.
//
// "*" is deliberately permissive for local dev — this API is Bearer-token
// authenticated, not cookie-based, so a wildcard origin here does not
// expose credentials the way it would for a cookie-authenticated API.
// Tighten to the real deployed origin(s) before this leaves local dev.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "600")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
