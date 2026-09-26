package llm

import "fmt"

// Config is the minimal slice of internal/config.Config this package needs
// — a local interface rather than importing internal/config directly,
// since config already sits below every other package and shouldn't gain a
// reverse dependency on internal/ai/llm just for this constructor.
type Config struct {
	Provider        string
	AnthropicAPIKey string
	AnthropicModel  string
	OllamaBaseURL   string
	OllamaModel     string
}

// NewFromConfig builds the real Client for whichever provider is
// configured. Returns ErrNotConfigured if Provider is empty (the caller
// turns that into 503 LLM_UNAVAILABLE, not a startup failure), and a plain
// error for an unrecognized provider name or a provider selected without
// its required setting (e.g. "anthropic" with no API key).
func NewFromConfig(cfg Config) (Client, error) {
	switch cfg.Provider {
	case "":
		return nil, ErrNotConfigured
	case "ollama":
		return NewOllamaClient(cfg.OllamaBaseURL, cfg.OllamaModel), nil
	case "anthropic":
		if cfg.AnthropicAPIKey == "" {
			return nil, fmt.Errorf("llm: LLM_PROVIDER=anthropic but ANTHROPIC_API_KEY is not set")
		}
		return NewAnthropicClient(cfg.AnthropicAPIKey, cfg.AnthropicModel), nil
	default:
		return nil, fmt.Errorf("llm: unknown LLM_PROVIDER %q (must be \"ollama\" or \"anthropic\")", cfg.Provider)
	}
}
