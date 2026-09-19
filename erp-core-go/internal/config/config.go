// Package config loads runtime configuration from environment variables.
// Kept deliberately tiny for Phase 0 — grows as new modules need settings.
package config

import "os"

type Config struct {
	HTTPAddr    string // e.g. ":8080"
	DatabaseDSN string // Postgres connection string
	JWTSecret   string // HS256 signing key — replace with an asymmetric key + rotation before production

	// DevAuthToolsEnabled wires up POST /dev/hash-password and POST
	// /dev/set-password (see internal/authn/dev_handlers.go) — public,
	// unauthenticated password tooling meant only for local development,
	// where generating a real bcrypt hash any other way requires a second
	// toolchain. Defaults to false/off; false in any environment but your
	// own machine, and never true in anything reachable by anyone but you.
	// SetPasswordHandler in particular is an unauthenticated
	// "change this account's password" endpoint — a real vulnerability
	// the moment real user accounts exist.
	DevAuthToolsEnabled bool
}

func Load() Config {
	return Config{
		HTTPAddr: getenv("HTTP_ADDR", ":8080"),
		// erp_app, not app_user — app_user is the schema-owning superuser
		// migrations run as; the service must run as the RLS-restricted role
		// (see migrations/004_least_privilege_app_role.sql).
		DatabaseDSN:         getenv("DATABASE_DSN", "postgres://erp_app:erp_app_password@localhost:5432/erp?sslmode=disable"),
		JWTSecret:           getenv("JWT_SECRET", "dev-only-secret-change-me"),
		DevAuthToolsEnabled: getenv("DEV_AUTH_TOOLS_ENABLED", "false") == "true",
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
