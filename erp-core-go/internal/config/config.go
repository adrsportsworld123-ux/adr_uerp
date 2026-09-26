// Package config loads runtime configuration from environment variables.
// Kept deliberately tiny for Phase 0 — grows as new modules need settings.
package config

import "os"

type Config struct {
	HTTPAddr    string // e.g. ":8080"
	DatabaseDSN string // Postgres connection string
	JWTSecret   string // HS256 signing key — replace with an asymmetric key + rotation before production

	// OpenSearchURL points at the product-search cluster (see
	// internal/search). Empty means search is simply not configured —
	// internal/search.Client treats that as "disabled," not a startup
	// failure, so a deployment without OpenSearch still runs everything
	// else fine; GET /products/search and POST /search/reindex just
	// answer 503 SEARCH_UNAVAILABLE.
	OpenSearchURL string

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

	// SMTP* configure internal/notifications' real email provider. Empty
	// SMTPHost means "not configured" — degrades to a console/log stand-in
	// for email too, same graceful-disable pattern as OpenSearchURL, rather
	// than failing startup over a notification channel nothing else in
	// this codebase depends on to function.
	SMTPHost     string
	SMTPPort     string
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string

	// NotificationsPhoneChannel is "sms" or "whatsapp" — which channel
	// internal/notifications.Handler.DispatchPhone resolves to. Both route
	// through the same console/log stand-in today (see
	// internal/notifications/provider.go's package doc for why: no SMS/
	// WhatsApp vendor has been chosen, and neither has real credentials in
	// this environment) — this only decides which label gets recorded and
	// which channel a future real integration would need to implement.
	NotificationsPhoneChannel string

	// LLMProvider selects which internal/ai/llm.Client backs NLP-BI
	// (POST /ai/ask) and the AI Copilot (POST /ai/copilot/chat): "ollama"
	// or "anthropic". Empty means "not configured" — the same
	// graceful-disable pattern as OpenSearchURL above, so those two
	// endpoints answer a clean 503 LLM_UNAVAILABLE instead of the service
	// failing to start. The user explicitly chose to support both behind
	// one interface (see internal/ai/llm) rather than commit to a single
	// vendor, after initially picking Anthropic alone and then asking for
	// Ollama (self-hosted, no data leaves this stack) to be considered too.
	LLMProvider string

	// AnthropicAPIKey/AnthropicModel configure internal/ai/llm.AnthropicClient.
	// Never hardcoded and never asked for in chat — read from the
	// environment only. Empty key means the anthropic provider can't be
	// selected; checked at client-construction time, not at startup, so a
	// deployment that only uses "ollama" never needs this set.
	AnthropicAPIKey string
	AnthropicModel  string

	// OllamaBaseURL/OllamaModel configure internal/ai/llm.OllamaClient — a
	// self-hosted model server (see docker-compose.yml's "ollama" service).
	// Unlike Anthropic, nothing here is a secret: it's a plain HTTP
	// endpoint on the same docker network the api service already runs on.
	OllamaBaseURL string
	OllamaModel   string
}

func Load() Config {
	return Config{
		HTTPAddr: getenv("HTTP_ADDR", ":8080"),
		// erp_app, not app_user — app_user is the schema-owning superuser
		// migrations run as; the service must run as the RLS-restricted role
		// (see migrations/004_least_privilege_app_role.sql).
		DatabaseDSN:         getenv("DATABASE_DSN", "postgres://erp_app:erp_app_password@localhost:5432/erp?sslmode=disable"),
		JWTSecret:           getenv("JWT_SECRET", "dev-only-secret-change-me"),
		OpenSearchURL:       getenv("OPENSEARCH_URL", ""),
		DevAuthToolsEnabled: getenv("DEV_AUTH_TOOLS_ENABLED", "false") == "true",

		SMTPHost:     getenv("SMTP_HOST", ""),
		SMTPPort:     getenv("SMTP_PORT", "587"),
		SMTPUsername: getenv("SMTP_USERNAME", ""),
		SMTPPassword: getenv("SMTP_PASSWORD", ""),
		SMTPFrom:     getenv("SMTP_FROM", "no-reply@example.com"),

		NotificationsPhoneChannel: getenv("NOTIFICATIONS_PHONE_CHANNEL", "sms"),

		LLMProvider:     getenv("LLM_PROVIDER", ""),
		AnthropicAPIKey: getenv("ANTHROPIC_API_KEY", ""),
		AnthropicModel:  getenv("ANTHROPIC_MODEL", "claude-3-5-haiku-latest"),
		OllamaBaseURL:   getenv("OLLAMA_BASE_URL", "http://localhost:11434"),
		OllamaModel:     getenv("OLLAMA_MODEL", "llama3.2:3b"),
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
