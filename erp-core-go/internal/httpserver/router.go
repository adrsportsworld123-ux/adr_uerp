package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/activity"
	"erp-core-go/internal/ai"
	"erp-core-go/internal/ai/llm"
	"erp-core-go/internal/approvals"
	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/banking"
	"erp-core-go/internal/branches"
	"erp-core-go/internal/catalog"
	"erp-core-go/internal/customers"
	"erp-core-go/internal/dashboard"
	"erp-core-go/internal/db"
	"erp-core-go/internal/dependencies"
	"erp-core-go/internal/documents"
	"erp-core-go/internal/einvoice"
	"erp-core-go/internal/expenses"
	"erp-core-go/internal/gst"
	"erp-core-go/internal/hr"
	"erp-core-go/internal/inventory"
	"erp-core-go/internal/loans"
	"erp-core-go/internal/locale"
	"erp-core-go/internal/loyalty"
	"erp-core-go/internal/merchants"
	"erp-core-go/internal/notifications"
	"erp-core-go/internal/partners"
	"erp-core-go/internal/payroll"
	"erp-core-go/internal/pharmacy"
	"erp-core-go/internal/pricing"
	"erp-core-go/internal/promotions"
	"erp-core-go/internal/purchase"
	"erp-core-go/internal/quotations"
	"erp-core-go/internal/rbac"
	"erp-core-go/internal/reportbuilder"
	"erp-core-go/internal/reports"
	"erp-core-go/internal/sales"
	"erp-core-go/internal/search"
	"erp-core-go/internal/softdelete"
	"erp-core-go/internal/subscriptions"
	"erp-core-go/internal/sync"
	"erp-core-go/internal/taxes"
	"erp-core-go/internal/terminals"
	"erp-core-go/internal/users"
)

func NewRouter(database *db.DB, issuer *authn.TokenIssuer, devAuthToolsEnabled bool, searchClient *search.Client, notify *notifications.Handler, einv *einvoice.Handler, llmClient llm.Client, holdDurationMinutes int, reportRunner *reportbuilder.Runner, activityLog *activity.Recorder) http.Handler {
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
	passwordReset := &authn.PasswordResetHandler{DB: database, Notify: notify}
	barcode := &catalog.BarcodeHandler{DB: database}
	barcodeAssign := &catalog.BarcodeAssignHandler{DB: database}
	taxonomy := &catalog.TaxonomyHandler{DB: database}
	deps := &dependencies.Handler{DB: database}
	terms := &terminals.Handler{DB: database}
	me := &authn.MeHandler{DB: database}
	label := &catalog.LabelHandler{DB: database}
	productList := &catalog.ListHandler{DB: database, Search: searchClient}
	prc := &pricing.Handler{DB: database, Search: searchClient}
	loy := &loyalty.Handler{DB: database}
	orders := &sales.Handler{DB: database, Loyalty: loy, Notify: notify, HoldDurationMinutes: holdDurationMinutes}
	inv := &inventory.Handler{DB: database}
	rpt := &reports.Handler{DB: database}
	syncH := &sync.Handler{DB: database}
	pur := &purchase.Handler{DB: database, Search: searchClient}
	acct := &accounting.Handler{DB: database}
	br := &branches.Handler{DB: database}
	docs := &documents.Handler{DB: database}
	taxH := &taxes.Handler{DB: database}
	usersH := &users.Handler{DB: database}
	localeH := &locale.Handler{DB: database}
	srch := &search.Handler{DB: database, Client: searchClient}
	cust := &customers.Handler{DB: database}
	promo := &promotions.Handler{DB: database}
	orders.OnCartChange = promotions.Reapply
	gstH := &gst.Handler{DB: database}
	auditH := &audit.Handler{DB: database}
	hrH := &hr.Handler{DB: database}
	payrollH := &payroll.Handler{DB: database}
	rbacH := &rbac.Handler{DB: database}
	apprH := &approvals.Handler{DB: database}
	aiH := &ai.Handler{DB: database, LLM: llmClient}
	quoH := &quotations.Handler{DB: database}
	pharmH := &pharmacy.Handler{DB: database}

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
			dev.Post("/set-platform-password", subscriptions.DevSetPlatformPassword(database))
		})
	}

	r.Route("/api/v1", func(api chi.Router) {
		// Unauthenticated
		// The activity log records sign-ins too; the handlers report the
		// merchant and user they resolved (internal/reqmeta).
		api.With(activityLog.Middleware).Post("/auth/login", login.ServeHTTP)
		api.With(activityLog.Middleware).Post("/auth/pin-login", pinLogin.ServeHTTP)
		api.Post("/auth/refresh", refresh.ServeHTTP)
		api.Post("/auth/forgot-password", passwordReset.ForgotPassword)
		api.Post("/auth/reset-password", passwordReset.ResetPassword)
		// Phase 12: a fresh device pairs itself with merchant code + a
		// single-use code an admin issued — no session exists yet.
		api.Post("/pos-terminals/pair", terms.Pair)

		// Phase 16: the SaaS console (platform staff, no tenant). A platform
		// token is refused by every tenant route, and the other way round.
		plat := &subscriptions.PlatformHandler{DB: database, Issuer: issuer}
		merchantPlat := &merchants.PlatformHandler{DB: database}
		api.Post("/platform/auth/login", plat.Login)
		api.Group(func(pr chi.Router) {
			pr.Use(authn.RequireAuth(issuer))
			pr.Use(subscriptions.RequirePlatform)
			pr.Get("/platform/merchants", plat.ListMerchants)
			pr.Post("/platform/merchants", plat.CreateMerchant)
			pr.Get("/platform/merchants/{id}", plat.GetMerchant)
			// Merchant Management (internal/merchants, migrations/065).
			pr.Get("/platform/merchants/{id}/profile", merchantPlat.GetMerchant)
			pr.Patch("/platform/merchants/{id}/profile", merchantPlat.UpdateMerchant)
			pr.Post("/platform/merchants/{id}/status", merchantPlat.SetStatus)
			pr.Post("/platform/merchants/{id}/admins/{user_id}/password", merchantPlat.ResetAdminPassword)
			pr.Patch("/platform/merchants/{id}/subscription", plat.UpdateSubscription)
			pr.Post("/platform/merchants/{id}/invoices/{invoice_id}/{action}", plat.InvoiceAction)
			pr.Get("/platform/plans", plat.ListPlans)
			pr.Patch("/platform/plans/{code}", plat.UpdatePlan)
			pr.Get("/platform/audit-log", plat.AuditLog)
		})

		// Authenticated — everything below requires a valid Bearer token,
		// and every handler inside this group gets its tenant from
		// authn.FromContext(ctx), never from a client-supplied parameter.
		api.Group(func(protected chi.Router) {
			protected.Use(authn.RequireAuth(issuer))
			// Every POS and web-admin action, refusals included — so it
			// sits before the scope, plan and permission checks below.
			protected.Use(activityLog.Middleware)
			// Phase 15: own-branch vs all-branches data scope, enforced by RLS.
			protected.Use(authn.BranchScopeMiddleware(database))
			// Phase 16: the merchant's plan: read-only once expired, plan
			// features, report retention (internal/subscriptions).
			protected.Use(subscriptions.Middleware(database))
			subH := &subscriptions.Handler{DB: database, Provider: subscriptions.StubProvider{}}
			protected.Get("/me/entitlements", subH.Entitlements)
			protected.Get("/plans", subH.Plans)
			protected.With(authn.RequirePermission(database, "subscription.manage")).Get("/subscription", subH.Get)
			protected.With(authn.RequirePermission(database, "subscription.manage")).Post("/subscription/change", subH.Change)
			protected.With(authn.RequirePermission(database, "subscription.manage")).Post("/subscription/invoices/{id}/pay", subH.Pay)

			// Phase 16: the custom report builder (internal/reportbuilder);
			// the whole /report-builder prefix needs the plan feature.
			rb := &reportbuilder.Handler{DB: database, Runner: reportRunner}
			build := authn.RequirePermission(database, "reports.build")
			protected.Get("/report-builder/datasets", rb.Datasets)
			protected.With(build).Post("/report-builder/query", rb.Query)
			protected.Get("/report-builder/templates", rb.ListTemplates)
			protected.With(build).Post("/report-builder/templates", rb.CreateTemplate)
			protected.Get("/report-builder/templates/{id}", rb.GetTemplate)
			protected.With(build).Put("/report-builder/templates/{id}", rb.UpdateTemplate)
			protected.With(build).Delete("/report-builder/templates/{id}", rb.DeleteTemplate)
			protected.With(build).Post("/report-builder/templates/{id}/duplicate", rb.DuplicateTemplate)
			protected.Post("/report-builder/templates/{id}/run", rb.RunTemplate)
			protected.Post("/report-builder/runs", rb.StartRun)
			protected.Get("/report-builder/runs", rb.ListRuns)
			protected.Get("/report-builder/runs/{id}", rb.GetRun)
			protected.Get("/report-builder/runs/{id}/download", rb.Download)
			// The standard report library and saved reports
			// (internal/reportbuilder/library.go): every plan, any
			// signed-in user, inside their branch scope.
			protected.Get("/reports/library", rb.LibraryList)
			protected.Post("/reports/library/{key}/run", rb.LibraryRun)
			protected.Post("/reports/library/{key}/export", rb.LibraryExport)
			protected.Get("/reports/saved", rb.SavedList)
			protected.Post("/reports/saved", rb.SaveLibrary)
			protected.Post("/reports/saved/{id}/run", rb.SavedRun)
			protected.Post("/reports/saved/{id}/export", rb.SavedExport)
			protected.Delete("/reports/saved/{id}", rb.SavedDelete)
			protected.Get("/reports/runs", rb.ReportRuns)
			protected.Get("/reports/runs/{id}/download", rb.ReportRunDownload)
			protected.Post("/reports/runs/{id}/regenerate", rb.Regenerate)
			protected.Post("/auth/logout", logout.ServeHTTP)
			// Phase 11: the caller's permission codes (a UI hint — every
			// gate below still enforces on its own), and "what uses this
			// record?" counts for master data.
			protected.Get("/me/permissions", me.Permissions)
			// User Management (internal/users, migrations/062): passwords,
			// PINs, status and home branch; one's own password and PIN.
			// Company profile (internal/merchants, migrations/065).
			merchantH := &merchants.Handler{DB: database}
			protected.Get("/merchant/profile", merchantH.GetProfile)
			protected.With(authn.RequirePermission(database, "merchant.profile")).Put("/merchant/profile", merchantH.UpdateProfile)
			// Localization (internal/locale, migrations/064).
			protected.Get("/settings/localization", localeH.GetSettings)
			protected.Get("/settings/localization/terminal", localeH.ForTerminal)
			protected.Put("/me/localization", localeH.UpdateMine)
			protected.With(authn.RequirePermission(database, "localization.manage")).Put("/settings/localization", localeH.UpdateSettings)
			protected.Put("/me/password", usersH.ChangeOwnPassword)
			protected.Put("/me/pin", usersH.ChangeOwnPIN)
			manageUsers := authn.RequirePermission(database, "users.manage")
			protected.With(manageUsers).Get("/users", usersH.ListUsers)
			protected.With(manageUsers).Get("/users/{id}", usersH.GetUser)
			protected.With(manageUsers).Patch("/users/{id}", usersH.UpdateUser)
			protected.With(manageUsers).Put("/users/{id}/password", usersH.SetPassword)
			protected.With(manageUsers).Put("/users/{id}/pin", usersH.SetPIN)
			protected.With(manageUsers).Delete("/users/{id}/pin", usersH.RemovePIN)
			protected.Get("/dependencies/{entity}/{id}", deps.Get)

			// Phase 12: POS terminal management (migrations/037).
			protected.Get("/pos-terminals", terms.List)
			protected.With(authn.RequirePermission(database, "terminal.manage"), subscriptions.Limit(database, subscriptions.LimitPOSTerminals)).
				Post("/pos-terminals", terms.Create)
			protected.With(authn.RequirePermission(database, "terminal.manage"), subscriptions.Limit(database, subscriptions.LimitPOSTerminals)).
				Patch("/pos-terminals/{id}", terms.Update)
			protected.With(authn.RequirePermission(database, "terminal.manage")).
				Post("/pos-terminals/{id}/pairing-code", terms.IssuePairingCode)
			protected.With(authn.RequirePermission(database, "terminal.manage")).
				Post("/pos-terminals/{id}/reset-device", terms.ResetDevice)

			protected.Get("/products/barcode/{code}", barcode.ServeHTTP)
			protected.Post("/products/variants/{id}/barcodes", barcodeAssign.AssignBarcode)
			protected.Get("/products/variants/{id}/label", label.PrintLabel)
			// Phase 14 label studio: any size and fields, ZPL / TSPL / ESC/POS.
			protected.Post("/labels/preview", label.PreviewLabels)
			protected.Post("/labels/print", label.PrintLabels)
			protected.Get("/products", productList.ListProducts)
			protected.With(authn.RequirePermission(database, "catalog.manage"), subscriptions.Limit(database, subscriptions.LimitProducts)).
				Post("/products", productList.CreateProduct)
			protected.With(authn.RequirePermission(database, "catalog.manage"), subscriptions.Limit(database, subscriptions.LimitProducts)).
				Patch("/products/{id}", productList.UpdateProduct)
			// Product editing: one product's full detail for an edit
			// screen, variant edits, and adding a variant later.
			protected.Get("/products/{id}", productList.GetProduct)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Patch("/products/variants/{id}", productList.UpdateVariant)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Post("/products/{id}/variants", productList.AddVariant)
			protected.Get("/products/search", srch.Search)

			protected.Get("/categories", taxonomy.ListCategories)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Post("/categories", taxonomy.CreateCategory)
			protected.Get("/brands", taxonomy.ListBrands)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Post("/brands", taxonomy.CreateBrand)
			protected.Get("/tax-slabs", taxonomy.ListTaxSlabs)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Post("/tax-slabs", taxonomy.CreateTaxSlab)
			// Phase 11 (migrations/036): edit, activate/deactivate, and a
			// delete that refuses (409 IN_USE) while anything references it.
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Patch("/categories/{id}", taxonomy.UpdateCategory)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Delete("/categories/{id}", taxonomy.DeleteCategory)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Patch("/brands/{id}", taxonomy.UpdateBrand)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Delete("/brands/{id}", taxonomy.DeleteBrand)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Patch("/tax-slabs/{id}", taxonomy.UpdateTaxSlab)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Delete("/tax-slabs/{id}", taxonomy.DeleteTaxSlab)
			protected.Get("/collections", taxonomy.ListCollections)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Post("/collections", taxonomy.CreateCollection)

			// Phase 8: Vertical Expansion — attribute-set configuration.
			protected.Get("/attributes", taxonomy.ListAttributes)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Post("/attributes", taxonomy.CreateAttribute)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Patch("/attributes/{id}", taxonomy.UpdateAttribute)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Post("/attributes/{id}/values", taxonomy.CreateAttributeValue)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Delete("/attributes/{id}/values/{value_id}", taxonomy.DeleteAttributeValue)
			protected.Get("/categories/{id}/attributes", taxonomy.ListCategoryAttributes)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Post("/categories/{id}/attributes", taxonomy.SetCategoryAttribute)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Delete("/categories/{id}/attributes/{attribute_id}", taxonomy.DeleteCategoryAttribute)

			protected.With(authn.RequirePermission(database, "search.reindex")).
				Post("/search/reindex", srch.Reindex)

			protected.With(subscriptions.Limit(database, subscriptions.LimitTransactionsPM)).Post("/sales/orders", orders.CreateOrder)
			// Phase 13: sales history (newest first, paged) and the
			// terminal/day summary, on the branch's local calendar day.
			protected.Get("/sales/orders", orders.ListOrders)
			protected.Get("/sales/summary", orders.Summary)
			protected.Get("/sales/orders/held", orders.ListHeld)
			protected.Get("/sales/orders/{id}", orders.GetOrder)
			protected.Post("/sales/orders/{id}/hold", orders.Hold)
			protected.Post("/sales/orders/{id}/recall", orders.Recall)
			protected.Get("/sales/orders/{id}/receipt", orders.GetReceipt)
			protected.Get("/sales/orders/{id}/receipt/print", orders.PrintReceipt)
			protected.Get("/sales/orders/{id}/receipt/pdf", orders.DownloadReceipt)
			protected.Post("/sales/orders/{id}/receipt/notify", orders.NotifyReceipt)
			protected.Post("/sales/orders/{id}/lines", orders.AddLine)
			protected.Patch("/sales/orders/{id}/lines/{line_id}", orders.UpdateLine)
			protected.Delete("/sales/orders/{id}/lines/{line_id}", orders.DeleteLine)
			protected.Post("/sales/orders/{id}/customer", orders.AttachCustomer)
			protected.Put("/sales/orders/{id}/place-of-supply", orders.SetPlaceOfSupply)
			protected.Put("/sales/orders/{id}/lines/{line_id}/description", orders.SetLineDescription)
			protected.Post("/sales/orders/{id}/prescription", orders.AttachPrescription)
			protected.Post("/prescriptions", pharmH.CreatePrescription)
			protected.Get("/prescriptions", pharmH.ListPrescriptions)
			protected.Post("/sales/orders/{id}/discounts", orders.ApplyDiscount)
			protected.Post("/sales/orders/{id}/promotions/apply", promo.ApplyPromotions)
			protected.Post("/sales/orders/{id}/coupons", promo.ApplyCoupon)
			protected.Post("/sales/orders/{id}/loyalty/redeem", loy.Redeem)
			protected.Post("/sales/orders/{id}/checkout", orders.Checkout)
			protected.With(authn.RequirePermission(database, "sales.void")).
				Post("/sales/orders/{id}/void", orders.Void)
			// A held order: its own cashier, or sales.void (checked in-handler).
			protected.Post("/sales/orders/{id}/void-held", orders.VoidHeld)
			protected.Post("/sales/orders/{id}/release", orders.Release)
			// Phase 13 returns: any cashier may start one; whether a manager
			// must approve is the merchant's setting (sales_settings).
			// Phase 14: the back office's full view of a sale, and its payment-method correction.
			protected.Get("/sales/orders/{id}/detail", orders.OrderDetail)
			protected.With(authn.RequirePermission(database, "sales.backoffice")).
				Post("/sales/orders/{id}/payments/{payment_id}/correct", orders.CorrectPayment)
			protected.Get("/sales/orders/{id}/returnable", orders.GetReturnable)
			protected.Get("/sales/orders/{id}/returns", orders.ListReturns)
			protected.Post("/sales/orders/{id}/returns", orders.CreateReturn)
			protected.Get("/sales/returns/{id}/pdf", orders.DownloadCreditNote)
			// Phase 13 shifts: any signed-in user runs their till's shift;
			// a count that doesn't match, and a paid-out, need a manager's
			// code + PIN inline (internal/sales/shifts.go).
			protected.Get("/pos-shifts", orders.ListShifts)
			protected.Get("/pos-shifts/current", orders.CurrentShift)
			protected.Post("/pos-shifts", orders.OpenShift)
			protected.Get("/pos-shifts/{id}", orders.GetShift)
			protected.Post("/pos-shifts/{id}/cash-movements", orders.AddCashMovement)
			protected.Post("/pos-shifts/{id}/close", orders.CloseShift)
			protected.Get("/pos-shifts/{id}/report/print", orders.PrintShiftReport)
			// The day-wise Z report (internal/sales/day_report.go): read by
			// anyone signed in, like the other reports; closing a day needs
			// reports.day_close.
			protected.Get("/reports/z-report", orders.ZReport)
			protected.Get("/reports/z-report/print", orders.PrintZReport)
			protected.Get("/reports/z-reports", orders.ListZReports)
			protected.With(authn.RequirePermission(database, "reports.day_close")).Post("/reports/z-report/close", orders.CloseDay)
			protected.Get("/sales/settings", orders.GetSettings)
			protected.With(authn.RequirePermission(database, "sales.settings")).
				Put("/sales/settings", orders.UpdateSettings)
			protected.With(authn.RequirePermission(database, "sales.settings")).
				Put("/sales/settings/branches/{id}", orders.UpdateBranchSettings)
			// Tax split on bills and invoices (migrations/060): the
			// merchant's regime, each branch's override and GST state.
			protected.Get("/tax/settings", taxH.GetSettings)
			protected.Get("/tax/regime", taxH.GetRegime)
			taxSettings := authn.RequirePermission(database, "tax.settings")
			protected.With(taxSettings).Put("/tax/settings", taxH.UpdateSettings)
			protected.With(taxSettings).Put("/tax/settings/branches/{id}", taxH.UpdateBranchSettings)

			protected.Get("/inventory", inv.GetStock)
			// Phase 13: oversold offline sales waiting for review (migrations/041).
			protected.Get("/inventory/variants/{id}/branches", inv.VariantStock)
			protected.Get("/inventory/review", inv.ListStockReview)
			protected.With(authn.RequirePermission(database, "inventory.adjust")).
				Post("/inventory/review/{id}/resolve", inv.ResolveStockReview)
			protected.With(authn.RequirePermission(database, "inventory.adjust")).
				Post("/inventory/reservations", orders.CreateReservation)
			protected.With(authn.RequirePermission(database, "inventory.adjust")).
				Delete("/inventory/reservations/{id}", orders.ReleaseManualReservation)
			protected.With(authn.RequirePermission(database, "inventory.adjust")).
				Post("/inventory/adjustments", inv.AdjustStock)
			protected.With(authn.RequirePermission(database, "inventory.adjust")).
				Patch("/inventory/reorder-point", inv.SetReorderPoint)
			protected.Post("/inventory/reconciliation", inv.CreateReconciliation)
			protected.Get("/inventory/reconciliation/history", inv.ReconciliationHistory)
			protected.Get("/inventory/reconciliation/{id}", inv.GetReconciliation)
			protected.Get("/inventory/expiring-batches", inv.GetExpiringBatches)

			protected.With(authn.RequirePermission(database, "notifications.view")).
				Get("/notifications", notify.ListNotifications)

			// The sales dashboard (internal/dashboard): dashboard.view to see
			// it, dashboard.configure to choose each role's widgets and the
			// sales targets.
			dash := &dashboard.Handler{DB: database}
			dashView := authn.RequirePermission(database, "dashboard.view")
			dashConfigure := authn.RequirePermission(database, "dashboard.configure")
			protected.Get("/dashboard", dash.Dashboard)
			protected.With(dashView).Get("/dashboard/widgets", dash.Catalog)
			protected.With(dashView).Get("/dashboard/targets", dash.Targets)
			protected.With(dashConfigure).Put("/dashboard/targets", dash.SetTargets)
			protected.With(dashConfigure).Get("/dashboard/roles", dash.ListRoles)
			protected.With(dashConfigure).Put("/dashboard/roles/{id}", dash.SetRole)
			protected.Get("/reports/daily-sales", rpt.DailySales)
			protected.Get("/reports/stock-summary", rpt.StockSummary)
			protected.Get("/reports/eod-cash", rpt.EODCash)
			protected.Get("/reports/consolidated-sales", rpt.ConsolidatedSales)
			protected.Get("/reports/consolidated-stock", rpt.ConsolidatedStock)
			protected.With(authn.RequirePermission(database, "audit.view")).
				Get("/reports/schedule-drug-sales", rpt.GetScheduleDrugSales)

			protected.Post("/sync/push", syncH.Push)
			protected.Get("/sync/pull", syncH.Pull)

			protected.Post("/suppliers", pur.CreateSupplier)
			protected.Get("/suppliers", pur.ListSuppliers)
			protected.Patch("/suppliers/{id}", pur.UpdateSupplier)

			// Purchase orders (migrations/057): PO → GRNs → a bill per GRN.
			protected.Post("/purchase/orders", pur.CreatePurchaseOrder)
			protected.Get("/purchase/orders", pur.ListPurchaseOrders)
			protected.Get("/purchase/orders/{id}", pur.GetPurchaseOrder)
			protected.Post("/purchase/orders/{id}/lines", pur.AddPurchaseOrderLine)
			protected.Delete("/purchase/orders/{id}/lines/{line_id}", pur.DeletePurchaseOrderLine)
			protected.Post("/purchase/orders/{id}/issue", pur.IssuePurchaseOrder)
			protected.Put("/purchase/orders/{id}/funding", pur.SetFunding)
			protected.Post("/purchase/orders/{id}/cancel", pur.CancelPurchaseOrder)
			protected.Post("/purchase/orders/{id}/close", pur.ClosePurchaseOrder)
			protected.Post("/purchase/orders/{id}/grn", pur.ReceivePurchaseOrder)
			protected.Get("/purchase/orders/{id}/pdf", pur.DownloadPurchaseOrder)
			protected.Delete("/purchase/orders/{id}", pur.DeletePurchaseOrder)

			protected.Post("/purchase/grn", pur.CreateGRN)
			protected.Get("/purchase/grn", pur.ListGRNs)
			protected.Get("/purchase/grn/{id}", pur.GetGRN)
			protected.Post("/purchase/grn/{id}/lines", pur.AddGRNLine)
			protected.Post("/purchase/grn/{id}/complete", pur.CompleteGRN)
			protected.Get("/purchase/grn/{id}/pdf", pur.DownloadGRN)
			protected.Delete("/purchase/grn/{id}", pur.DeleteGRN)

			// Supplier invoice import from the CSV template (import.go).
			protected.Get("/purchase/imports/template", pur.ImportTemplate)
			protected.Post("/purchase/imports/preview", pur.PreviewImport)
			protected.Post("/purchase/imports/commit", pur.CommitImport)
			protected.Post("/purchase/bills", pur.CreateBill)
			protected.Get("/purchase/bills", pur.ListBills)
			protected.Get("/purchase/bills/{id}", pur.GetBill)
			protected.Post("/purchase/bills/{id}/payments", pur.RecordBillPayment)
			protected.Get("/purchase/bills/{id}/pdf", pur.DownloadBill)

			protected.Post("/purchase/returns", pur.CreatePurchaseReturn)
			protected.Get("/purchase/returns", pur.ListPurchaseReturns)
			protected.Get("/purchase/returns/{id}/pdf", pur.DownloadPurchaseReturn)

			protected.Get("/accounting/accounts", acct.ListAccounts)
			protected.With(authn.RequirePermission(database, "accounting.manage")).
				Patch("/accounting/accounts/{id}", acct.UpdateAccount)
			protected.Post("/accounting/accounts", acct.CreateAccount)
			protected.Post("/accounting/journal-entries", acct.CreateManualJournalEntry)
			// Phase 14: the ledger browsable + exportable, journal vouchers with an
			// optional approval workflow, and the period lock (migrations/043).
			protected.Get("/accounting/journal-entries", acct.ListJournalEntries)
			protected.Get("/accounting/journal-entries/{id}", acct.GetJournalEntry)
			protected.Get("/accounting/vouchers", acct.ListVouchers)
			protected.Post("/accounting/vouchers", acct.CreateVoucher)
			protected.Get("/accounting/vouchers/{id}", acct.GetVoucher)
			protected.Put("/accounting/vouchers/{id}", acct.UpdateVoucher)
			protected.Delete("/accounting/vouchers/{id}", acct.DeleteVoucher)
			protected.Post("/accounting/vouchers/{id}/{action}", acct.VoucherAction)
			protected.Get("/accounting/settings", acct.GetAccountingSettings)
			protected.With(authn.RequirePermission(database, "accounting.manage")).
				Put("/accounting/settings", acct.UpdateAccountingSettings)
			protected.Get("/accounting/party-ledger", acct.PartyLedger)
			protected.Get("/accounting/parties", acct.PartySearch)

			// Bank accounts, cheque books and cheques (internal/banking, migrations/066).
			bankH := &banking.Handler{DB: database}
			bankManage := authn.RequirePermission(database, "bank.manage")
			chequesManage := authn.RequirePermission(database, "cheques.manage")
			protected.Get("/bank-accounts", bankH.ListAccounts)
			protected.With(bankManage).Post("/bank-accounts", bankH.CreateAccount)
			protected.With(bankManage).Patch("/bank-accounts/{id}", bankH.UpdateAccount)
			protected.Get("/bank-accounts/{id}/cheque-books", bankH.ListBooks)
			protected.With(bankManage).Post("/bank-accounts/{id}/cheque-books", bankH.CreateBook)
			protected.With(bankManage).Post("/cheque-books/{id}/close", bankH.CloseBook)
			protected.Get("/cheques/issued", bankH.ListIssued)
			protected.With(chequesManage).Post("/cheques/issued", bankH.CreateIssued)
			protected.With(chequesManage).Post("/cheques/issued/cancel-leaf", bankH.CancelLeaf)
			protected.With(chequesManage).Get("/cheques/issued/{id}/print", bankH.PrintIssued)
			protected.With(chequesManage).Post("/cheques/issued/{id}/{action}", bankH.IssuedAction)
			protected.Get("/cheques/received", bankH.ListReceived)
			protected.With(chequesManage).Post("/cheques/received", bankH.CreateReceived)
			protected.With(chequesManage).Post("/cheques/received/{id}/{action}", bankH.ReceivedAction)

			// Employee loans and advances (internal/loans, migrations/067).
			loansH := &loans.Handler{DB: database}
			loansManage := authn.RequirePermission(database, "loans.manage")
			protected.With(loansManage).Get("/employee-loans", loansH.List)
			protected.With(loansManage).Post("/employee-loans", loansH.Create)
			protected.With(loansManage).Post("/employee-loans/preview", loansH.PreviewSchedule)
			protected.Get("/employee-loans/{id}", loansH.Get)
			protected.With(loansManage).Post("/employee-loans/{id}/repayments", loansH.Repay)
			protected.With(loansManage).Post("/employee-loans/{id}/foreclose", loansH.Foreclose)
			protected.Get("/me/loans", loansH.Mine)

			// Partners' capital and expenses (migrations/074).
			partnersH := &partners.Handler{DB: database}
			partnersManage := authn.RequirePermission(database, "partners.manage")
			protected.Get("/partners", partnersH.List)
			protected.With(partnersManage).Post("/partners", partnersH.Create)
			protected.With(partnersManage).Get("/partners/summary", partnersH.Summary)
			protected.With(partnersManage).Patch("/partners/{id}", partnersH.Update)
			protected.With(partnersManage).Post("/partners/{id}/pay-in", partnersH.PayIn)
			protected.With(partnersManage).Post("/partners/{id}/pay-out", partnersH.PayOut)
			protected.With(partnersManage).Get("/partners/{id}/statement", partnersH.Statement)
			expH := &expenses.Handler{DB: database}
			expensesManage := authn.RequirePermission(database, "expenses.manage")
			protected.Get("/expense-heads", expH.ListHeads)
			protected.With(expensesManage).Post("/expense-heads", expH.SaveHead)
			protected.With(expensesManage).Patch("/expense-heads/{id}", expH.SaveHead)
			protected.With(expensesManage).Get("/expenses", expH.ListExpenses)
			protected.With(expensesManage).Post("/expenses", expH.CreateExpense)
			protected.With(expensesManage).Get("/expenses/import/template", expH.ImportTemplate)
			protected.With(expensesManage).Post("/expenses/import/preview", expH.PreviewImport)
			protected.With(expensesManage).Post("/expenses/import/commit", expH.CommitImport)
			protected.With(expensesManage).Post("/expenses/{id}/pay", expH.PayExpense)
			protected.With(expensesManage).Post("/expenses/{id}/cancel", expH.CancelExpense)
			protected.Get("/accounting/day-book", acct.DayBook)
			protected.Get("/accounting/cash-book", acct.CashBook)

			protected.Get("/accounting/bank-statement/imports", acct.ListBankStatementImports)
			protected.With(authn.RequirePermission(database, "bank_reconciliation.manage")).
				Post("/accounting/bank-statement/import", acct.ImportBankStatement)
			protected.Get("/accounting/bank-statement/lines", acct.ListBankStatementLines)
			protected.Get("/accounting/bank-statement/lines/{id}/candidates", acct.BankStatementLineCandidates)
			protected.With(authn.RequirePermission(database, "bank_reconciliation.manage")).
				Post("/accounting/bank-statement/lines/{id}/match", acct.MatchBankStatementLine)
			protected.With(authn.RequirePermission(database, "bank_reconciliation.manage")).
				Post("/accounting/bank-statement/lines/{id}/unmatch", acct.UnmatchBankStatementLine)
			protected.Get("/accounting/bank-reconciliation", acct.BankReconciliationReport)

			protected.Get("/accounting/payment-gateway/settlement/imports", acct.ListPaymentGatewaySettlementImports)
			protected.With(authn.RequirePermission(database, "payment_gateway_reconciliation.manage")).
				Post("/accounting/payment-gateway/settlement/import", acct.ImportPaymentGatewaySettlement)
			protected.Get("/accounting/payment-gateway/settlement/lines", acct.ListPaymentGatewaySettlementLines)
			protected.Get("/accounting/payment-gateway/settlement/lines/{id}/candidates", acct.PaymentGatewaySettlementLineCandidates)
			protected.With(authn.RequirePermission(database, "payment_gateway_reconciliation.manage")).
				Post("/accounting/payment-gateway/settlement/lines/{id}/match", acct.MatchPaymentGatewaySettlementLine)
			protected.With(authn.RequirePermission(database, "payment_gateway_reconciliation.manage")).
				Post("/accounting/payment-gateway/settlement/lines/{id}/unmatch", acct.UnmatchPaymentGatewaySettlementLine)
			protected.Get("/accounting/payment-gateway/reconciliation", acct.PaymentGatewayReconciliationReport)

			protected.Post("/cash-reconciliation", acct.CreateCashReconciliation)
			protected.Get("/cash-reconciliation", acct.GetCashReconciliation)
			protected.Get("/cash-reconciliation/history", acct.CashReconciliationHistory)

			protected.With(subscriptions.Limit(database, subscriptions.LimitBranches)).Post("/branches", br.CreateBranch)
			protected.Get("/branches", br.ListBranches)
			protected.With(subscriptions.Limit(database, subscriptions.LimitBranches)).Patch("/branches/{id}", br.UpdateBranch)

			// Document letterhead (internal/documents, migrations/049):
			// reads open (the POS and every document render need it),
			// writes gated.
			printSettings := authn.RequirePermission(database, "branch.print_settings")
			protected.Get("/branches/{id}/print-settings", docs.GetSettings)
			protected.With(printSettings).Put("/branches/{id}/print-settings", docs.UpdateSettings)
			protected.Get("/branches/{id}/print-settings/logo", docs.GetLogo)
			protected.With(printSettings).Put("/branches/{id}/print-settings/logo", docs.UploadLogo)
			protected.With(printSettings).Delete("/branches/{id}/print-settings/logo", docs.DeleteLogo)
			protected.Get("/branches/{id}/print-settings/preview", docs.Preview)

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

			protected.Post("/customers", cust.CreateCustomer)
			protected.Get("/customers", cust.ListCustomers)
			protected.Get("/customers/{id}", cust.GetCustomer)
			protected.Patch("/customers/{id}", cust.UpdateCustomer)
			protected.Get("/customers/{id}/loyalty", loy.GetCustomerLoyalty)
			protected.Get("/customers/{id}/credit", cust.GetCustomerCredit)
			protected.With(authn.RequirePermission(database, "credit.manage")).
				Patch("/customers/{id}/credit", cust.UpdateCustomerCredit)
			protected.Post("/customers/{id}/payments", cust.RecordReceivablePayment)

			protected.Get("/promotions", promo.ListPromotions)
			protected.With(authn.RequirePermission(database, "promotions.manage")).
				Post("/promotions", promo.CreatePromotion)
			protected.With(authn.RequirePermission(database, "promotions.manage")).
				Patch("/promotions/{id}", promo.UpdatePromotion)
			protected.With(authn.RequirePermission(database, "promotions.manage")).
				Delete("/promotions/{id}", promo.DeletePromotion)
			protected.With(authn.RequirePermission(database, "promotions.manage")).
				Post("/promotions/{id}/duplicate", promo.DuplicatePromotion)

			protected.Get("/coupons", promo.ListCoupons)
			protected.With(authn.RequirePermission(database, "promotions.manage")).
				Post("/coupons", promo.CreateCoupon)
			protected.With(authn.RequirePermission(database, "promotions.manage")).
				Patch("/coupons/{id}", promo.UpdateCoupon)
			protected.With(authn.RequirePermission(database, "promotions.manage")).
				Delete("/coupons/{id}", promo.DeleteCoupon)

			protected.Get("/loyalty/config", loy.GetConfig)
			protected.With(authn.RequirePermission(database, "loyalty.manage")).
				Patch("/loyalty/config", loy.UpdateConfig)

			protected.Get("/gst/gstr1", gstH.GSTR1)
			protected.Get("/gst/gstr3b", gstH.GSTR3B)

			protected.Get("/sales/orders/{id}/e-invoice", einv.GetEInvoice)
			protected.With(authn.RequirePermission(database, "einvoice.manage")).
				Post("/sales/orders/{id}/e-invoice", einv.GenerateEInvoice)
			protected.With(authn.RequirePermission(database, "einvoice.manage")).
				Post("/sales/orders/{id}/e-invoice/cancel", einv.CancelEInvoice)
			protected.Get("/sales/orders/{id}/e-way-bill", einv.GetEWayBill)
			protected.With(authn.RequirePermission(database, "einvoice.manage")).
				Post("/sales/orders/{id}/e-way-bill", einv.GenerateEWayBill)
			protected.With(authn.RequirePermission(database, "einvoice.manage")).
				Post("/sales/orders/{id}/e-way-bill/cancel", einv.CancelEWayBill)

			protected.With(authn.RequirePermission(database, "audit.view")).
				Get("/audit-logs", auditH.ListLogs)
			protected.With(authn.RequirePermission(database, "audit.view")).
				Get("/audit-logs/verify", auditH.VerifyChain)
			protected.With(authn.RequirePermission(database, "audit.view")).
				Get("/audit/activity", activityLog.ListActivity)
			protected.With(authn.RequirePermission(database, "audit.view")).
				Get("/audit/modules", activityLog.ListModules)
			protected.With(authn.RequirePermission(database, "audit.view")).
				Get("/audit/settings", activityLog.GetSettings)
			protected.With(authn.RequirePermission(database, "audit.configure")).
				Put("/audit/settings", activityLog.UpdateSettings)

			// Delete for every master that had none (migrations/054).
			registerMasterDeletes(protected, database)

			// Soft-deleted masters and drafts (migrations/050).
			bin := &softdelete.Handler{DB: database}
			protected.With(authn.RequirePermission(database, "recycle_bin.manage")).
				Get("/recycle-bin", bin.List)
			protected.With(authn.RequirePermission(database, "recycle_bin.manage")).
				Post("/recycle-bin/{entity}/{id}/restore", bin.RestoreHandler)

			protected.Post("/pricing/calculate", prc.Calculate)
			// Merchant-wide pricing defaults (migrations/033): currently
			// the tax-inclusive default for new products.
			protected.Get("/pricing/settings", prc.GetSettings)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Put("/pricing/settings", prc.UpdateSettings)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Patch("/pricing/variants/{id}", prc.UpdateVariantPricing)
			protected.Post("/pricing/bulk-update/preview", prc.PreviewBulkUpdate)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Post("/pricing/bulk-update/apply", prc.ApplyBulkUpdate)

			// Phase 7: wholesale price lists. Reads open to any authenticated
			// user (a sales rep building a quote needs to see wholesale
			// prices); writes gated behind pricing.manage, same tier as a
			// single variant's price change above.
			protected.Get("/pricing/price-lists", prc.ListPriceLists)
			protected.Get("/pricing/price-lists/{id}", prc.GetPriceList)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Post("/pricing/price-lists", prc.CreatePriceList)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Patch("/pricing/price-lists/{id}", prc.RenamePriceList)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Delete("/pricing/price-lists/{id}", prc.DeletePriceList)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Put("/pricing/price-lists/{id}/items/{variant_id}", prc.UpsertPriceListItem)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Delete("/pricing/price-lists/{id}/items/{variant_id}", prc.DeletePriceListItem)

			// Phase 10: scheduled price changes and dynamic price rules.
			// Reads open (a cashier asking "why is this cheaper right now?"
			// is informational); writes gated behind pricing.manage, the
			// same tier as changing a price immediately.
			protected.Get("/pricing/scheduled-changes", prc.ListScheduledChanges)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Post("/pricing/scheduled-changes", prc.CreateScheduledChange)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Post("/pricing/scheduled-changes/{id}/cancel", prc.CancelScheduledChange)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Put("/pricing/scheduled-changes/{id}", prc.UpdateScheduledChange)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Delete("/pricing/scheduled-changes/{id}", prc.DeleteScheduledChange)
			protected.Get("/pricing/rules", prc.ListPriceRules)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Post("/pricing/rules", prc.CreatePriceRule)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Patch("/pricing/rules/{id}", prc.SetPriceRuleActive)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Delete("/pricing/rules/{id}", prc.DeletePriceRule)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Put("/pricing/rules/{id}", prc.UpdatePriceRule)
			protected.Get("/pricing/variants/{id}/effective-price", prc.GetEffectivePrice)
			// Markup rules (migrations/071): cost + markup, used per the
			// branch's price benefit.
			protected.Get("/pricing/markup-rules", prc.ListMarkupRules)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Post("/pricing/markup-rules", prc.CreateMarkupRule)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Put("/pricing/markup-rules/{id}", prc.UpdateMarkupRule)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Patch("/pricing/markup-rules/{id}", prc.SetMarkupRuleActive)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Delete("/pricing/markup-rules/{id}", prc.DeleteMarkupRule)

			protected.With(authn.RequirePermission(database, "hr.manage")).
				Get("/hr/roles", hrH.ListRoles)
			protected.With(authn.RequirePermission(database, "hr.manage")).
				Post("/hr/employees", hrH.CreateEmployee)
			protected.With(authn.RequirePermission(database, "hr.manage")).
				Get("/hr/employees", hrH.ListEmployees)
			protected.With(authn.RequirePermission(database, "hr.manage")).
				Get("/hr/employees/{id}", hrH.GetEmployee)
			protected.With(authn.RequirePermission(database, "hr.manage")).
				Patch("/hr/employees/{id}", hrH.UpdateEmployee)
			protected.With(authn.RequirePermission(database, "hr.manage")).
				Post("/hr/shifts", hrH.CreateShift)
			protected.Get("/hr/shifts", hrH.ListShifts)
			protected.With(authn.RequirePermission(database, "hr.manage")).
				Patch("/hr/shifts/{id}", hrH.UpdateShift)
			protected.Post("/hr/attendance/clock-in", hrH.ClockIn)
			protected.Post("/hr/attendance/clock-out", hrH.ClockOut)
			// Check-in/out evidence, the day register, rules and geofences
			// (migrations/052).
			protected.Get("/hr/attendance/me", hrH.MyAttendance)
			protected.With(authn.RequirePermission(database, "hr.manage")).
				Get("/hr/attendance/register", hrH.Register)
			protected.With(authn.RequirePermission(database, "hr.manage")).
				Post("/hr/attendance/mark", hrH.MarkAttendance)
			protected.With(authn.RequirePermission(database, "hr.manage")).
				Post("/hr/attendance/{id}/sessions", hrH.AddSession)
			protected.With(authn.RequirePermission(database, "hr.manage")).
				Patch("/hr/attendance/sessions/{id}", hrH.CorrectSession)
			protected.With(authn.RequirePermission(database, "hr.manage")).
				Get("/hr/attendance/settings", hrH.GetAttendanceSettings)
			protected.With(authn.RequirePermission(database, "hr.manage")).
				Put("/hr/attendance/settings", hrH.UpdateAttendanceSettings)
			protected.With(authn.RequirePermission(database, "hr.manage")).
				Get("/hr/attendance/geofences", hrH.ListGeofences)
			protected.With(authn.RequirePermission(database, "hr.manage")).
				Put("/hr/attendance/geofences/{branch_id}", hrH.SetGeofence)
			protected.Get("/hr/attendance", hrH.ListAttendance)
			protected.With(authn.RequirePermission(database, "hr.manage")).
				Patch("/hr/attendance/{id}", hrH.CorrectAttendance)
			protected.With(authn.RequirePermission(database, "payroll.manage")).
				Get("/hr/statutory-config", hrH.GetStatutoryConfig)
			protected.With(authn.RequirePermission(database, "payroll.manage")).
				Patch("/hr/statutory-config", hrH.UpdateStatutoryConfig)
			protected.With(authn.RequirePermission(database, "payroll.manage")).
				Post("/hr/employees/{id}/salary-structure", hrH.SetSalaryStructure)
			protected.With(authn.RequirePermission(database, "payroll.manage")).
				Get("/hr/employees/{id}/salary-structure", hrH.GetSalaryStructure)

			protected.With(authn.RequirePermission(database, "payroll.manage")).
				Post("/payroll/commission-rules", payrollH.CreateCommissionRule)
			protected.With(authn.RequirePermission(database, "payroll.manage")).
				Get("/payroll/commission-rules", payrollH.ListCommissionRules)
			protected.With(authn.RequirePermission(database, "payroll.manage")).
				Patch("/payroll/commission-rules/{id}", payrollH.UpdateCommissionRule)
			protected.With(authn.RequirePermission(database, "payroll.manage")).
				Post("/payroll/commission/compute", payrollH.ComputeCommission)
			protected.With(authn.RequirePermission(database, "payroll.manage")).
				Post("/payroll/runs", payrollH.CreateOrRecomputeRun)
			protected.With(authn.RequirePermission(database, "payroll.manage")).
				Post("/payroll/runs/{id}/finalize", payrollH.FinalizeRun)
			protected.With(authn.RequirePermission(database, "payroll.manage")).
				Get("/payroll/runs", payrollH.ListRuns)
			protected.With(authn.RequirePermission(database, "payroll.manage")).
				Get("/payroll/runs/{id}", payrollH.GetRun)
			protected.With(authn.RequirePermission(database, "payroll.manage")).
				Get("/payroll/runs/{id}/challan", payrollH.GetChallanSummary)
			protected.Get("/payroll/employees/{id}/payslips", payrollH.GetMyPayslips)

			protected.With(authn.RequirePermission(database, "rbac.manage")).
				Get("/rbac/permissions", rbacH.ListPermissions)
			protected.With(authn.RequirePermission(database, "rbac.manage")).
				Post("/rbac/roles", rbacH.CreateRole)
			protected.With(authn.RequirePermission(database, "rbac.manage")).
				Get("/rbac/roles", rbacH.ListRoles)
			protected.With(authn.RequirePermission(database, "rbac.manage")).
				Get("/rbac/roles/{id}", rbacH.GetRole)
			protected.With(authn.RequirePermission(database, "rbac.manage")).
				Patch("/rbac/roles/{id}", rbacH.UpdateRole)
			protected.With(authn.RequirePermission(database, "rbac.manage")).
				Delete("/rbac/roles/{id}", rbacH.DeleteRole)
			protected.With(authn.RequirePermission(database, "rbac.manage")).
				Patch("/rbac/roles/{id}/permissions", rbacH.SetRolePermissions)
			protected.With(authn.RequirePermission(database, "rbac.manage")).
				Post("/rbac/users/{id}/roles", rbacH.AssignUserRole)
			protected.With(authn.RequirePermission(database, "rbac.manage")).
				Delete("/rbac/users/{id}/roles/{role_id}", rbacH.RevokeUserRole)
			// Phase 15: branch data scope management.
			protected.With(authn.RequirePermission(database, "rbac.manage")).
				Get("/rbac/data-access", rbacH.DataAccess)
			protected.With(authn.RequirePermission(database, "rbac.manage")).
				Put("/rbac/users/{id}/branches", rbacH.SetUserBranches)
			// Phase 15: approval policies (discount tiers, refunds, below-cost prices,
			// stock adjustments, JV posting).
			protected.Get("/settings/approval-policies", apprH.Get)
			protected.With(authn.RequirePermission(database, "approvals.manage")).
				Put("/settings/approval-policies", apprH.Put)
			// What is waiting for approval, and what was approved (migrations/073).
			protected.With(authn.RequirePermission(database, "approvals.view")).
				Get("/approvals/waiting", apprH.Waiting)
			protected.With(authn.RequirePermission(database, "approvals.view")).
				Get("/approvals/given", apprH.Given)

			protected.Get("/ai/reorder-suggestions", aiH.GetReorderSuggestions)
			protected.Get("/ai/recommendations/{variant_id}", aiH.GetRecommendations)
			protected.Get("/ai/demand-forecast/{variant_id}", aiH.GetDemandForecast)
			protected.With(authn.RequirePermission(database, "audit.view")).
				Get("/ai/anomalies", aiH.GetAnomalies)
			protected.Post("/ai/ask", aiH.Ask)
			protected.Post("/ai/copilot/chat", aiH.CopilotChat)

			// Phase 7: B2B quotations. Open to any authenticated user, same
			// tier as creating/editing a POS cart — a quote is a proposal,
			// not yet a financial commitment; the real risk (stock leaving
			// the building) only happens at ConvertQuotation, which reuses
			// internal/sales' own cart pipeline and its existing checks.
			protected.Post("/quotations", quoH.CreateQuotation)
			protected.Get("/quotations", quoH.ListQuotations)
			protected.Get("/quotations/{id}", quoH.GetQuotation)
			protected.Get("/quotations/{id}/pdf", quoH.DownloadQuotation)
			protected.Post("/quotations/{id}/send", quoH.SendQuotation)
			protected.Post("/quotations/{id}/accept", quoH.AcceptQuotation)
			protected.Post("/quotations/{id}/reject", quoH.RejectQuotation)
			protected.With(subscriptions.Limit(database, subscriptions.LimitTransactionsPM)).Post("/quotations/{id}/convert", quoH.ConvertQuotation)
			protected.Delete("/quotations/{id}", quoH.DeleteQuotation)
			// Phase 10: editing a draft (draft-only; 409 QUOTATION_NOT_DRAFT otherwise).
			protected.Patch("/quotations/{id}", quoH.UpdateQuotation)
			protected.Post("/quotations/{id}/lines", quoH.AddQuotationLine)
			protected.Patch("/quotations/{id}/lines/{line_id}", quoH.UpdateQuotationLine)
			protected.Delete("/quotations/{id}/lines/{line_id}", quoH.DeleteQuotationLine)
		})
	})

	return r
}
