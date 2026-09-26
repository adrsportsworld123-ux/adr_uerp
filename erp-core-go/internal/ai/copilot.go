package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/httpx"
	"erp-core-go/internal/reports"
)

// AI Copilot — phased_roadmap.md Phase 6 item 7. Scoped, as agreed,
// strictly to advisory/informational chat for v1: it can discuss what it's
// told about this merchant's own current numbers and give general
// operational advice, but it has NO tool-calling or mutation ability
// whatsoever — it cannot place an order, change a price, adjust stock, or
// call any other endpoint in this system, not even a read-only one beyond
// the one small context bundle built below. Every reply is just text back
// to the merchant; nothing it says is ever executed automatically.
//
// Stateless by design, matching this codebase's JWT/no-server-session
// philosophy: the client sends the full conversation transcript on every
// call, the server holds nothing between requests.

type copilotMessage struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content string `json:"content"`
}

type copilotChatRequest struct {
	Messages []copilotMessage `json:"messages"`
}

type copilotChatResponse struct {
	Reply string `json:"reply"`
}

// CopilotChat: POST /ai/copilot/chat — body {"messages": [{"role":"user","content":"..."}]}.
func (h *Handler) CopilotChat(w http.ResponseWriter, r *http.Request) {
	if !h.requireLLM(w) {
		return
	}
	claims, ok := authn.FromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authentication required")
		return
	}
	var req copilotChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) == 0 {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "messages must be a non-empty array")
		return
	}
	lastMsg := req.Messages[len(req.Messages)-1]
	if lastMsg.Role != "user" || strings.TrimSpace(lastMsg.Content) == "" {
		httpx.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "the last message must be a non-empty user message")
		return
	}

	var reply string
	err := h.DB.WithTenant(r.Context(), claims.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		snapshot, err := buildCopilotContext(ctx, tx)
		if err != nil {
			return err
		}
		system := `You are an operations advisor built into a retail/wholesale ERP system, talking to a merchant admin or manager.
Scope you MUST respect:
- You can discuss the "Today's snapshot" data given below, explain what reports/features in this ERP mean, and give general retail-operations advice (pricing strategy, inventory discipline, staffing, customer retention, etc.).
- You have NO ability to take any action in this system — you cannot place orders, change prices, adjust stock, issue refunds, or call any API. If asked to do something, explain that you can only advise, not act, and suggest which screen in the app to use instead.
- Never invent specific numbers beyond what's given in "Today's snapshot" — if asked about data you don't have, say so plainly rather than guessing.
- Keep replies concise and practical.

Today's snapshot (real data, computed just now):
` + snapshot

		userTurn := renderTranscript(req.Messages)
		text, callErr := h.LLM.Complete(ctx, system, userTurn)
		if callErr != nil {
			return &llmCallError{callErr}
		}
		reply = strings.TrimSpace(text)
		return nil
	})
	var llmErr *llmCallError
	if err != nil {
		if as, ok := err.(*llmCallError); ok {
			llmErr = as
		}
	}
	if llmErr != nil {
		httpx.Error(w, http.StatusBadGateway, "LLM_ERROR", "could not get a reply from the configured LLM provider: "+llmErr.Error())
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not build the copilot's context")
		return
	}
	httpx.JSON(w, http.StatusOK, copilotChatResponse{Reply: reply})
}

// buildCopilotContext assembles a small, real, tenant-scoped snapshot the
// system prompt grounds its answers in — today's consolidated sales and
// current reorder-worthy variant count. Deliberately tiny: this is meant
// to make general advice feel current, not to be a full data export into
// the model's context.
func buildCopilotContext(ctx context.Context, tx pgx.Tx) (string, error) {
	today := time.Now().Format("2006-01-02")
	sales, err := reports.BuildConsolidatedSales(ctx, tx, today)
	if err != nil {
		return "", err
	}
	var lowStockCount int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM stock_levels
		WHERE reorder_point > 0 AND (on_hand - reserved) <= reorder_point`).Scan(&lowStockCount); err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("Date: " + today + "\n")
	sb.WriteString("Orders finalized today (all branches): " + strconv.Itoa(sales.TotalOrderCount) + "\n")
	sb.WriteString("Total sales today (all branches): " + sales.TotalGrandTotal + "\n")
	sb.WriteString("Stock lines at or below reorder point right now: " + strconv.Itoa(lowStockCount) + "\n")
	return sb.String(), nil
}

// renderTranscript turns the client-supplied message array into the
// single prompt string llm.Client.Complete expects — role-prefixed lines,
// since neither backend implementation here needs true multi-turn message
// objects for a feature this simple.
func renderTranscript(messages []copilotMessage) string {
	var sb strings.Builder
	for _, m := range messages {
		role := "User"
		if m.Role == "assistant" {
			role = "Assistant"
		}
		sb.WriteString(role + ": " + m.Content + "\n")
	}
	return sb.String()
}
