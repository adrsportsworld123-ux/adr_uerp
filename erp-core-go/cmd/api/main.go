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

	"erp-core-go/internal/authn"
	"erp-core-go/internal/config"
	"erp-core-go/internal/db"
	"erp-core-go/internal/httpserver"
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

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpserver.NewRouter(database, issuer, cfg.DevAuthToolsEnabled),
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
