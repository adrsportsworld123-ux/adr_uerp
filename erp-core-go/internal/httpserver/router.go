package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"erp-core-go/internal/accounting"
	"erp-core-go/internal/ai"
	"erp-core-go/internal/ai/llm"
	"erp-core-go/internal/audit"
	"erp-core-go/internal/authn"
	"erp-core-go/internal/branches"
	"erp-core-go/internal/catalog"
	"erp-core-go/internal/customers"
	"erp-core-go/internal/db"
	"erp-core-go/internal/einvoice"
	"erp-core-go/internal/gst"
	"erp-core-go/internal/hr"
	"erp-core-go/internal/inventory"
	"erp-core-go/internal/loyalty"
	"erp-core-go/internal/notifications"
	"erp-core-go/internal/payroll"
	"erp-core-go/internal/pharmacy"
	"erp-core-go/internal/pricing"
	"erp-core-go/internal/promotions"
	"erp-core-go/internal/purchase"
	"erp-core-go/internal/quotations"
	"erp-core-go/internal/rbac"
	"erp-core-go/internal/reports"
	"erp-core-go/internal/sales"
	"erp-core-go/internal/search"
	"erp-core-go/internal/sync"
)

func NewRouter(database *db.DB, issuer *authn.TokenIssuer, devAuthToolsEnabled bool, searchClient *search.Client, notify *notifications.Handler, einv *einvoice.Handler, llmClient llm.Client) http.Handler {
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
	label := &catalog.LabelHandler{DB: database}
	productList := &catalog.ListHandler{DB: database}
	prc := &pricing.Handler{DB: database, Search: searchClient}
	loy := &loyalty.Handler{DB: database}
	orders := &sales.Handler{DB: database, Loyalty: loy, Notify: notify}
	inv := &inventory.Handler{DB: database}
	rpt := &reports.Handler{DB: database}
	syncH := &sync.Handler{DB: database}
	pur := &purchase.Handler{DB: database}
	acct := &accounting.Handler{DB: database}
	br := &branches.Handler{DB: database}
	srch := &search.Handler{DB: database, Client: searchClient}
	cust := &customers.Handler{DB: database}
	promo := &promotions.Handler{DB: database}
	gstH := &gst.Handler{DB: database}
	auditH := &audit.Handler{DB: database}
	hrH := &hr.Handler{DB: database}
	payrollH := &payroll.Handler{DB: database}
	rbacH := &rbac.Handler{DB: database}
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
		})
	}

	r.Route("/api/v1", func(api chi.Router) {
		// Unauthenticated
		api.Post("/auth/login", login.ServeHTTP)
		api.Post("/auth/pin-login", pinLogin.ServeHTTP)
		api.Post("/auth/refresh", refresh.ServeHTTP)
		api.Post("/auth/forgot-password", passwordReset.ForgotPassword)
		api.Post("/auth/reset-password", passwordReset.ResetPassword)

		// Authenticated — everything below requires a valid Bearer token,
		// and every handler inside this group gets its tenant from
		// authn.FromContext(ctx), never from a client-supplied parameter.
		api.Group(func(protected chi.Router) {
			protected.Use(authn.RequireAuth(issuer))
			protected.Post("/auth/logout", logout.ServeHTTP)

			protected.Get("/products/barcode/{code}", barcode.ServeHTTP)
			protected.Post("/products/variants/{id}/barcodes", barcodeAssign.AssignBarcode)
			protected.Get("/products/variants/{id}/label", label.PrintLabel)
			protected.Get("/products", productList.ListProducts)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Post("/products", productList.CreateProduct)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Patch("/products/{id}", productList.UpdateProduct)
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
			protected.Get("/collections", taxonomy.ListCollections)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Post("/collections", taxonomy.CreateCollection)

			// Phase 8: Vertical Expansion — attribute-set configuration.
			protected.Get("/attributes", taxonomy.ListAttributes)
			protected.With(authn.RequirePermission(database, "catalog.manage")).
				Post("/attributes", taxonomy.CreateAttribute)
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

			protected.Post("/sales/orders", orders.CreateOrder)
			protected.Get("/sales/orders/{id}", orders.GetOrder)
			protected.Get("/sales/orders/{id}/receipt", orders.GetReceipt)
			protected.Get("/sales/orders/{id}/receipt/print", orders.PrintReceipt)
			protected.Post("/sales/orders/{id}/receipt/notify", orders.NotifyReceipt)
			protected.Post("/sales/orders/{id}/lines", orders.AddLine)
			protected.Patch("/sales/orders/{id}/lines/{line_id}", orders.UpdateLine)
			protected.Delete("/sales/orders/{id}/lines/{line_id}", orders.DeleteLine)
			protected.Post("/sales/orders/{id}/customer", orders.AttachCustomer)
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

			protected.Get("/inventory", inv.GetStock)
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

			protected.Get("/coupons", promo.ListCoupons)
			protected.With(authn.RequirePermission(database, "promotions.manage")).
				Post("/coupons", promo.CreateCoupon)
			protected.With(authn.RequirePermission(database, "promotions.manage")).
				Patch("/coupons/{id}", promo.UpdateCoupon)

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

			protected.Post("/pricing/calculate", prc.Calculate)
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
				Delete("/pricing/price-lists/{id}", prc.DeletePriceList)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Put("/pricing/price-lists/{id}/items/{variant_id}", prc.UpsertPriceListItem)
			protected.With(authn.RequirePermission(database, "pricing.manage")).
				Delete("/pricing/price-lists/{id}/items/{variant_id}", prc.DeletePriceListItem)

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
			protected.Post("/hr/attendance/clock-in", hrH.ClockIn)
			protected.Post("/hr/attendance/clock-out", hrH.ClockOut)
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
			protected.Post("/quotations/{id}/send", quoH.SendQuotation)
			protected.Post("/quotations/{id}/accept", quoH.AcceptQuotation)
			protected.Post("/quotations/{id}/reject", quoH.RejectQuotation)
			protected.Post("/quotations/{id}/convert", quoH.ConvertQuotation)
			protected.Delete("/quotations/{id}", quoH.DeleteQuotation)
		})
	})

	return r
}
