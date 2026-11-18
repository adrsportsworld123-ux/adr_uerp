package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Attachment is a file the model reads along with the prompt: an image
// (image/jpeg, image/png, image/webp, image/gif) or a PDF
// (application/pdf).
type Attachment struct {
	MediaType string
	Data      []byte
}

// DocumentReader is a Client whose model can read images and PDFs — the
// purchase invoice reader's AI path (internal/purchase/invoice_ai.go),
// for photos and scanned PDFs that carry no text. A separate interface,
// not a Client method, so a text-only backend (Ollama's small local
// models) simply doesn't offer it.
type DocumentReader interface {
	ReadDocument(ctx context.Context, systemPrompt, userPrompt string, files []Attachment) (string, error)
}

// ErrUnsupportedAttachment: a media type the model can't read.
var ErrUnsupportedAttachment = errors.New("llm: unsupported attachment type")

// NewDocumentReader builds the invoice reader: Anthropic when an API key
// is set (whatever LLM_PROVIDER says — that one picks NLP-BI's and the
// Copilot's backend), else nil, and the reader answers 503.
func NewDocumentReader(apiKey, model string) DocumentReader {
	if apiKey == "" {
		return nil
	}
	if model == "" {
		model = "claude-sonnet-5-5"
	}
	c := NewAnthropicClient(apiKey, model)
	// A few pages of an invoice take longer than a chat answer.
	c.HTTPClient = &http.Client{Timeout: 180 * time.Second}
	return c
}

type anthropicBlockSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicBlock struct {
	Type   string                `json:"type"`
	Text   string                `json:"text,omitempty"`
	Source *anthropicBlockSource `json:"source,omitempty"`
}

type anthropicDocRequest struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	System    string `json:"system,omitempty"`
	Messages  []struct {
		Role    string           `json:"role"`
		Content []anthropicBlock `json:"content"`
	} `json:"messages"`
}

// ReadDocument sends the files as image / document blocks ahead of the
// prompt and returns the model's text answer.
func (c *AnthropicClient) ReadDocument(ctx context.Context, systemPrompt, userPrompt string, files []Attachment) (string, error) {
	var blocks []anthropicBlock
	for _, f := range files {
		kind := ""
		switch f.MediaType {
		case "image/jpeg", "image/png", "image/webp", "image/gif":
			kind = "image"
		case "application/pdf":
			kind = "document"
		default:
			return "", fmt.Errorf("%w: %s", ErrUnsupportedAttachment, f.MediaType)
		}
		blocks = append(blocks, anthropicBlock{Type: kind, Source: &anthropicBlockSource{
			Type: "base64", MediaType: f.MediaType, Data: base64.StdEncoding.EncodeToString(f.Data),
		}})
	}
	blocks = append(blocks, anthropicBlock{Type: "text", Text: userPrompt})

	req := anthropicDocRequest{Model: c.Model, MaxTokens: 16000, System: systemPrompt}
	req.Messages = append(req.Messages, struct {
		Role    string           `json:"role"`
		Content []anthropicBlock `json:"content"`
	}{Role: "user", Content: blocks})
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("llm: marshal anthropic request: %w", err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("llm: build anthropic request: %w", err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("x-api-key", c.APIKey)
	hreq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.HTTPClient.Do(hreq)
	if err != nil {
		return "", fmt.Errorf("llm: anthropic request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("llm: read anthropic response: %w", err)
	}
	var parsed anthropicResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("llm: parse anthropic response: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("llm: anthropic API error (%s): %s", parsed.Error.Type, parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: anthropic returned HTTP %d: %s", resp.StatusCode, string(raw))
	}
	for _, b := range parsed.Content {
		if b.Type == "text" {
			return b.Text, nil
		}
	}
	return "", fmt.Errorf("llm: anthropic response had no text content block")
}
