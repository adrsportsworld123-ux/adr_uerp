// Package llm defines a single, minimal interface both real LLM backends
// implement, so internal/ai's NLP-BI and AI Copilot features never depend
// on a specific vendor's SDK/wire format directly. Mirrors the
// GSPClient/StubGSPClient interface precedent from e-invoicing: build the
// interface first, keep every vendor-specific detail (auth headers, request
// shape, response parsing) behind it.
//
// Deliberately just one method. Neither NLP-BI nor the Copilot needs
// streaming, tool-calling, or multi-turn server-side state — NLP-BI issues
// one classification call plus one summary call per question, and the
// Copilot is a stateless "send the whole transcript, get the next reply"
// loop, matching this codebase's stateless-JWT philosophy. Adding
// tool-calling later is a real interface change, not a hidden capability
// this type quietly gains.
package llm

import (
	"context"
	"errors"
)

// ErrNotConfigured is returned by NewFromConfig when LLM_PROVIDER is unset
// — the same "feature simply isn't configured" signal OpenSearchURL's
// empty-string convention uses elsewhere in this codebase. Callers turn
// this into 503 LLM_UNAVAILABLE, never a fatal startup error.
var ErrNotConfigured = errors.New("llm: no provider configured (set LLM_PROVIDER=ollama|anthropic)")

// Client completes a single prompt against a system instruction. Both
// implementations return the model's raw text response — callers that need
// structured output (NLP-BI's intent classification) are responsible for
// instructing the model to emit JSON and for validating what comes back;
// this interface makes no promise the response is well-formed, since no
// real LLM API can guarantee that either.
type Client interface {
	Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}
