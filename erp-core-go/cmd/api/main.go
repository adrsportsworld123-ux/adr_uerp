// Command api is the Go transactional-core service's entry point for
// Phase 0/1: auth (login) + the barcode-scan catalog lookup, wired against
// a real Postgres instance with row-level-security tenant isolation.
package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"erp-core-go/internal/ai/llm"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/config"
	"erp-core-go/internal/customers"
	"erp-core-go/internal/db"
	"erp-core-go/internal/einvoice"
	"erp-core-go/internal/httpserver"
	"erp-core-go/internal/inventory"
	"erp-core-go/internal/notifications"
	"erp-core-go/internal/purchase"
	"erp-core-go/internal/quotations"
	"erp-core-go/internal/sales"
	"erp-core-go/internal/search"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()

	database, err := db.New(ctx, cfg.DatabaseDSN)
	if err != nil {
		log.Fatalf("startup: %v", err)
	}
	defer database.Close()

	issuer := authn.NewTokenIssuer(cfg.JWTSecret, "erp-core-go", 24*time.Hour)

	searchClient := search.NewClient(cfg.OpenSearchURL)
	if searchClient.Enabled() {
		// Best-effort: OpenSearch being briefly unreachable at boot must
		// never stop the whole API from starting — search is layered on
		// top of Postgres, not load-bearing for it. GET /products/search
		// and POST /search/reindex retry EnsureIndex themselves.
		if err := searchClient.EnsureIndex(ctx); err != nil {
			log.Printf("search: ensure index at startup: %v (will retry lazily)", err)
		} else if count, err := searchClient.Count(ctx); err != nil {
			log.Printf("search: count documents at startup: %v", err)
		} else if count == 0 {
			// An index with zero documents at boot means either this is a
			// brand-new environment or OpenSearch's data was lost since
			// the last run (no volume, a wiped volume, disaster recovery)
			// — found live exactly this way once already. Backfill from
			// Postgres now rather than leaving search silently empty
			// until someone remembers POST /search/reindex exists.
			log.Printf("search: index is empty at startup, backfilling from Postgres...")
			if indexed, err := search.BackfillAllTenants(ctx, database, searchClient); err != nil {
				log.Printf("search: startup backfill: %v", err)
			} else {
				log.Printf("search: startup backfill indexed %d document(s) across all tenants", indexed)
			}
		}
	}

	notifyProvider := notifications.NewProviderFromConfig(cfg.SMTPHost, notifications.SMTPConfig{
		Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.SMTPFrom,
	})
	notify := &notifications.Handler{DB: database, Provider: notifyProvider, PhoneChannel: cfg.NotificationsPhoneChannel}

	// einvoice.NewStubGSPClient is the only GSPClient today — real IRN/
	// e-way-bill generation needs a licensed GSP (GST Suvidha Provider)
	// vendor decision the operator hasn't made yet (see
	// migrations/022_einvoice.sql's header comment). Swapping in a real
	// vendor later is a new GSPClient implementation constructed here,
	// nothing else in the stack changes.
	einv := &einvoice.Handler{DB: database, GSP: einvoice.NewStubGSPClient(), Provider: einvoice.ProviderStub}

	// llm.NewFromConfig returns ErrNotConfigured when LLM_PROVIDER is
	// unset — logged and left nil, not a startup failure, matching
	// OpenSearchURL's graceful-disable pattern above: NLP-BI and the AI
	// Copilot (internal/ai's Ask/CopilotChat) just answer 503
	// LLM_UNAVAILABLE until a provider is configured.
	llmClient, err := llm.NewFromConfig(llm.Config{
		Provider:        cfg.LLMProvider,
		AnthropicAPIKey: cfg.AnthropicAPIKey,
		AnthropicModel:  cfg.AnthropicModel,
		OllamaBaseURL:   cfg.OllamaBaseURL,
		OllamaModel:     cfg.OllamaModel,
	})
	if err != nil {
		if err == llm.ErrNotConfigured {
			log.Printf("llm: no provider configured — NLP-BI and the AI Copilot will answer 503 LLM_UNAVAILABLE")
		} else {
			log.Printf("llm: %v — NLP-BI and the AI Copilot will answer 503 LLM_UNAVAILABLE", err)
		}
	}

	go sales.RunExpirySweeper(ctx, database, time.Minute)
	go quotations.RunExpirySweeper(ctx, database, time.Hour)
	go inventory.RunLowStockSweeper(ctx, database, notify, 15*time.Minute)
	go purchase.RunPaymentReminderSweeper(ctx, database, notify, time.Hour)
	go customers.RunReceivableReminderSweeper(ctx, database, notify, time.Hour)

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpserver.NewRouter(database, issuer, cfg.DevAuthToolsEnabled, searchClient, notify, einv, llmClient),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("erp-core-go listening on %s", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
