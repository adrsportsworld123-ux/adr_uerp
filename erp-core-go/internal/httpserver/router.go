package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/catalog"
	"erp-core-go/internal/db"
	"erp-core-go/internal/sales"
)

func NewRouter(database *db.DB, issuer *authn.TokenIssuer, devAuthToolsEnabled bool) http.Handler {
	r := chi.NewRouter()
	r.Use(CORS)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	login := &authn.LoginHandler{DB: database, Issuer: issuer}
	barcode := &catalog.BarcodeHandler{DB: database}
	orders := &sales.Handler{DB: database}

	// Dev-only, public, no-auth password tooling — see the package comment
	// on internal/authn/dev_handlers.go for exactly what these two
	// endpoints do and why each is (or isn't) safe to leave unauthenticated.
	// Registered only when DEV_AUTH_TOOLS_ENABLED=true; if the flag is off
	// these routes simply don't exist (404), not "exist but refuse" — so a
	// misconfigured deployment can't accidentally advertise the endpoint.
	if devAuthToolsEnabled {
		hashPassword := &authn.HashPasswordHandler{}
		setPassword := &authn.SetPasswordHandler{DB: database}
		r.Route("/dev", func(dev chi.Router) {
			dev.Post("/hash-password", hashPassword.ServeHTTP)
			dev.Post("/set-password", setPassword.ServeHTTP)
		})
	}

	r.Route("/api/v1", func(api chi.Router) {
		// Unauthenticated
		api.Post("/auth/login", login.ServeHTTP)

		// Authenticated — everything below requires a valid Bearer token,
		// and every handler inside this group gets its tenant from
		// authn.FromContext(ctx), never from a client-supplied parameter.
		api.Group(func(protected chi.Router) {
			protected.Use(authn.RequireAuth(issuer))
			protected.Get("/products/barcode/{code}", barcode.ServeHTTP)

			protected.Post("/sales/orders", orders.CreateOrder)
			protected.Get("/sales/orders/{id}", orders.GetOrder)
			protected.Post("/sales/orders/{id}/lines", orders.AddLine)
			protected.Post("/sales/orders/{id}/checkout", orders.Checkout)
		})
	})

	return r
}
