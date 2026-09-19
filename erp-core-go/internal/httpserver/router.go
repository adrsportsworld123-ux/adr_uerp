package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"erp-core-go/internal/authn"
	"erp-core-go/internal/catalog"
	"erp-core-go/internal/db"
	"erp-core-go/internal/inventory"
	"erp-core-go/internal/reports"
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
	pinLogin := &authn.PinLoginHandler{DB: database, Issuer: issuer}
	refresh := &authn.RefreshHandler{DB: database, Issuer: issuer}
	logout := &authn.LogoutHandler{DB: database}
	barcode := &catalog.BarcodeHandler{DB: database}
	orders := &sales.Handler{DB: database}
	inv := &inventory.Handler{DB: database}
	rpt := &reports.Handler{DB: database}

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
		api.Post("/auth/pin-login", pinLogin.ServeHTTP)
		api.Post("/auth/refresh", refresh.ServeHTTP)

		// Authenticated — everything below requires a valid Bearer token,
		// and every handler inside this group gets its tenant from
		// authn.FromContext(ctx), never from a client-supplied parameter.
		api.Group(func(protected chi.Router) {
			protected.Use(authn.RequireAuth(issuer))
			protected.Post("/auth/logout", logout.ServeHTTP)

			protected.Get("/products/barcode/{code}", barcode.ServeHTTP)

			protected.Post("/sales/orders", orders.CreateOrder)
			protected.Get("/sales/orders/{id}", orders.GetOrder)
			protected.Post("/sales/orders/{id}/lines", orders.AddLine)
			protected.Delete("/sales/orders/{id}/lines/{line_id}", orders.DeleteLine)
			protected.Post("/sales/orders/{id}/discounts", orders.ApplyDiscount)
			protected.Post("/sales/orders/{id}/checkout", orders.Checkout)

			protected.Get("/inventory", inv.GetStock)
			protected.Post("/inventory/adjustments", inv.AdjustStock)

			protected.Get("/reports/daily-sales", rpt.DailySales)
			protected.Get("/reports/stock-summary", rpt.StockSummary)
			protected.Get("/reports/eod-cash", rpt.EODCash)
		})
	})

	return r
}
