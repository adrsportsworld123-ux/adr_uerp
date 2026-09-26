package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OllamaClient calls a self-hosted Ollama server (see docker-compose.yml's
// "ollama" service) — no API key, no data leaves this stack. Uses
// /api/generate with stream:false rather than /api/chat: NLP-BI and the
// Copilot each already assemble one full prompt string per call (the
// Copilot includes prior turns itself, matching this codebase's stateless
// request/response philosophy), so there's no multi-turn state for Ollama
// itself to track.
type OllamaClient struct {
	BaseURL    string
	Model      string
	HTTPClient *http.Client
}

func NewOllamaClient(baseURL, model string) *OllamaClient {
	return &OllamaClient{
		BaseURL: baseURL,
		Model:   model,
		// Local models on modest hardware are slow — a generous timeout
		// beats a false "LLM is down" from a model that's simply thinking.
		HTTPClient: &http.Client{Timeout: 120 * time.Second},
	}
}

type ollamaRequest struct {
	Model  string `json:"model"`
	System string `json:"system,omitempty"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type ollamaResponse struct {
	Response string `json:"response"`
	Error    string `json:"error"`
}

func (c *OllamaClient) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	reqBody, err := json.Marshal(ollamaRequest{
		Model:  c.Model,
		System: systemPrompt,
		Prompt: userPrompt,
		Stream: false,
	})
	if err != nil {
		return "", fmt.Errorf("llm: marshal ollama request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/generate", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("llm: build ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: ollama request failed (is the ollama service reachable at %s?): %w", c.BaseURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("llm: read ollama response: %w", err)
	}

	var parsed ollamaResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("llm: parse ollama response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := parsed.Error
		if msg == "" {
			msg = string(body)
		}
		return "", fmt.Errorf("llm: ollama returned HTTP %d: %s", resp.StatusCode, msg)
	}
	return parsed.Response, nil
}
