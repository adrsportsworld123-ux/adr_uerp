package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/branches"
	"erp-core-go/internal/catalog"
	"erp-core-go/internal/db"
	"erp-core-go/internal/inventory"
	"erp-core-go/internal/pricing"
	"erp-core-go/internal/purchase"
	"erp-core-go/internal/reports"
	"erp-core-go/internal/sales"
	"erp-core-go/internal/sync"
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
	productList := &catalog.ListHandler{DB: database}
	prc := &pricing.Handler{DB: database}
	orders := &sales.Handler{DB: database}
	inv := &inventory.Handler{DB: database}
	rpt := &reports.Handler{DB: database}
	syncH := &sync.Handler{DB: database}
	pur := &purchase.Handler{DB: database}
	acct := &accounting.Handler{DB: database}
	br := &branches.Handler{DB: database}

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
			protected.Get("/products", productList.ListProducts)

			protected.Post("/sales/orders", orders.CreateOrder)
			protected.Get("/sales/orders/{id}", orders.GetOrder)
			protected.Get("/sales/orders/{id}/receipt", orders.GetReceipt)
			protected.Post("/sales/orders/{id}/lines", orders.AddLine)
			protected.Patch("/sales/orders/{id}/lines/{line_id}", orders.UpdateLine)
			protected.Delete("/sales/orders/{id}/lines/{line_id}", orders.DeleteLine)
			protected.Post("/sales/orders/{id}/customer", orders.AttachCustomer)
			protected.Post("/sales/orders/{id}/discounts", orders.ApplyDiscount)
			protected.Post("/sales/orders/{id}/checkout", orders.Checkout)
			protected.With(authn.RequirePermission(database, "sales.void")).
				Post("/sales/orders/{id}/void", orders.Void)

			protected.Get("/inventory", inv.GetStock)
			protected.With(authn.RequirePermission(database, "inventory.adjust")).
				Post("/inventory/adjustments", inv.AdjustStock)

			protected.Get("/reports/daily-sales", rpt.DailySales)
			protected.Get("/reports/stock-summary", rpt.StockSummary)
			protected.Get("/reports/eod-cash", rpt.EODCash)
			protected.Get("/reports/consolidated-sales", rpt.ConsolidatedSales)
			protected.Get("/reports/consolidated-stock", rpt.ConsolidatedStock)

			protected.Post("/sync/push", syncH.Push)
			protected.Get("/sync/pull", syncH.Pull)

			protected.Post("/suppliers", pur.CreateSupplier)
			protected.Get("/suppliers", pur.ListSuppliers)
			protected.Patch("/suppliers/{id}", pur.UpdateSupplier)

			protected.Post("/purchase/grn", pur.CreateGRN)
			protected.Get("/purchase/grn", pur.ListGRNs)
			protected.Get("/purchase/grn/{id}", pur.GetGRN)
			protected.Post("/purchase/grn/{id}/lines", pur.AddGRNLine)
			protected.Post("/purchase/grn/{id}/complete", pur.CompleteGRN)

			protected.Post("/purchase/bills", pur.CreateBill)
			protected.Get("/purchase/bills", pur.ListBills)
			protected.Get("/purchase/bills/{id}", pur.GetBill)
			protected.Post("/purchase/bills/{id}/payments", pur.RecordBillPayment)

			protected.Post("/purchase/returns", pur.CreatePurchaseReturn)
			protected.Get("/purchase/returns", pur.ListPurchaseReturns)

			protected.Get("/accounting/accounts", acct.ListAccounts)
			protected.Post("/accounting/accounts", acct.CreateAccount)
			protected.Post("/accounting/journal-entries", acct.CreateManualJournalEntry)
			protected.Get("/accounting/party-ledger", acct.PartyLedger)
			protected.Get("/accounting/day-book", acct.DayBook)
			protected.Get("/accounting/cash-book", acct.CashBook)

			protected.Post("/branches", br.CreateBranch)
			protected.Get("/branches", br.ListBranches)
			protected.Patch("/branches/{id}", br.UpdateBranch)

			protected.Post("/branch-transfers", br.CreateTransfer)
			protected.Get("/branch-transfers", br.ListTransfers)
			protected.Get("/branch-transfers/{id}", br.GetTransfer)
			protected.With(authn.RequirePermission(database, "branch_transfer.approve")).
				Post("/branch-transfers/{id}/approve", br.ApproveTransfer)
			protected.With(authn.RequirePermission(database, "branch_transfer.approve")).
				Post("/branch-transfers/{id}/reject", br.RejectTransfer)
			protected.Post("/branch-transfers/{id}/dispatch", br.DispatchTransfer)
			protected.Post("/branch-transfers/{id}/complete", br.CompleteTransfer)
			protected.Post("/branch-transfers/{id}/cancel", br.CancelTransfer)

			protected.Post("/pricing/calculate", prc.Calculate)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Patch("/pricing/variants/{id}", prc.UpdateVariantPricing)
			protected.Post("/pricing/bulk-update/preview", prc.PreviewBulkUpdate)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Post("/pricing/bulk-update/apply", prc.ApplyBulkUpdate)
		})
	})

	return r
}
