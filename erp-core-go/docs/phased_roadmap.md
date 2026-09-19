# Universal Retail & Wholesale ERP — Phased Build Roadmap

**Version:** 1.0 | **Date:** September 14, 2026
**Basis:** Finalized Tech Stack (see `tech_stack_decision.md`) + BRS + POS FRD
**Builder capacity:** Solo / 1-2 people, no fixed deadline — sequenced by dependency and value, not by calendar
**Reference vertical:** Sports & general retail first (masters kept generic so grocery/pharmacy/apparel/etc. slot in later without code changes)

---

## How this roadmap is sequenced

With 1-2 people against 40+ modules, the only way this gets built is if every phase (a) produces something real you could demo or run a pilot store on, and (b) doesn't require the *next* phase's work to function. So the order below follows the actual dependency chain of a retail transaction — you can't do Purchase before Inventory exists, you can't do Accounting before Sales generates ledger entries, you can't do AI forecasting before there's transaction history to forecast from — rather than the BRS's module numbering.

Each phase lists what's **in**, what's explicitly **deferred**, the **tech stack pieces** it activates, and a **Definition of Done**. Relative sizing (S/M/L/XL) reflects solo-builder effort, not calendar time, since you don't have a fixed deadline.

---

## Phase 0 — Walking Skeleton
**Size: S | Goal: Prove the whole stack talks to itself before writing a single business feature.**

**In:**
- Docker Compose stack running locally: Go monolith, Python monolith, Postgres, Redis, OpenSearch, MinIO, auth service — all as empty/hello-world services that can reach each other
- Merchant → Branch → POS Terminal schema skeleton with `tenant_id` + Postgres RLS policies wired (even with just one table)
- Auth v0: username/password login issuing a JWT, one hardcoded role, PIN-login stub
- CI pipeline (GitHub Actions): lint + test on push for both Go and Python repos
- Flutter app and Next.js admin each showing a login screen that successfully calls the real auth service

**Deferred:** everything business-related.

**Definition of Done:** You can log into both the Flutter app and the web admin against a real (if empty) backend, and a request round-trips through the API Gateway to both monoliths.

**Status: done** (see `phase0_1_design.md` §4 in this docs/ folder for exactly what shipped and how it was verified) — including the login → JWT flow and the CORS/dev-password hardening that followed real testing against the running service.

---

## Phase 1 — Core Retail Loop (the real MVP)
**Size: XL | Goal: One store can run entirely on the system, end to end, for real.**

**In:**
- **Product & Catalog Management:** categories, brands, attributes, variant matrix (size/color — sports gear), barcode assignment (hybrid: use supplier barcode if present, else auto-generate EAN-13)
- **Inventory Management:** SKU + variant + serial-number tracking (bikes, high-value gear), stock reservation with optimistic locking (15-min cart hold), stock adjustments with audit trail
- **Sales & Billing:** full POS transaction flow (scan → cart → discount → GST → payment → receipt) targeting the ≤6-second flow; Cash, Card, UPI to start (Wallets/BNPL/Gift Cards/Crypto deferred)
- **Tax & GST (basic):** CGST/SGST/IGST auto-calc, pre-configured slabs, HSN codes per product — e-invoicing and e-way bill deferred (only legally required above turnover thresholds, not needed to run a pilot store)
- **Barcode & Label Generation:** standard + compact label templates, one printer integration (pick whichever brand you'll actually pilot with — Zebra or a generic ESC/POS printer)
- **Auth (hardened):** PIN quick-login for cashiers, device binding, session timeouts per FRD (15/30/60 min tiers)
- **Basic reports:** daily sales, stock summary, EOD cash reconciliation

**Deferred:** multi-branch, Purchase, Accounting beyond raw transaction log, CRM/Loyalty, HR, AI, e-invoicing/e-way bill, wholesale/B2B.

**Tech activated:** Go core fully exercised (Sales, Inventory, Auth); Flutter POS app in real daily use; Postgres + Redis in production-shape use; OpenSearch not yet needed at single-branch catalog size (can defer to Phase 2).

**Definition of Done:** A real sports/general retail store could process actual daily sales on this — offline-capable, GST-correct, receipt-printing, stock-accurate.

**Status: in progress — a hardening pass closed most of the previous open list.** Built: catalog barcode lookup, full cart → add-line → discount → delete-line → checkout with optimistic-locking stock reservation, idempotent checkout, session-tiered auth (login/PIN-login/refresh/logout), failed-login lockout, the reservation-expiry sweeper, inventory adjustments with audit trail, and basic reports (see `phase0_1_design.md` §6 for the full list and how each was verified live against this repo's own `docker-compose.yml`).

**§6.1 of that doc is worth reading before touching anything else here**: the hardening pass found that `docker-compose.yml`'s `api` service was connecting to Postgres as a superuser (`app_user`, via `POSTGRES_USER`), which silently disabled every RLS policy in the system since Phase 0 — the multi-tenancy guarantee this whole codebase is built around had never actually been enforced by the database in this stack. Fixed (a dedicated non-superuser `erp_app` role, `migrations/004_least_privilege_app_role.sql`) and re-verified live, but **if you've deployed this anywhere outside the shipped docker-compose, check that deployment's DB role the same way** — this class of bug (an admin/master role standing in for the app's own role) isn't specific to Docker.

**Still open, in dependency order:**
1. `/sync/push`/`/sync/pull` + a Flutter-side offline store — the single biggest remaining gap against "offline-capable" in this phase's Definition of Done below. Needs a decision on the Flutter local-storage approach before implementation starts.
2. Barcode & label generation, printer integration
3. `GET /sales/orders/{id}/receipt`, `POST /sales/orders/{id}/void`, `POST /sales/orders/{id}/customer`, `PATCH .../lines/{line_id}`
4. General permission-code enforcement (`permissions`/`role_permissions` — currently only two handlers gate by role name, not the schema's permission-code model)
5. Discount-before-tax GST treatment (today's discount is a post-tax reduction — see `phase0_1_design.md` §3.4)
6. PIN quick-login UI in the Flutter app (backend supports it; no screen calls it yet)

---

## Phase 2 — Restock the Store: Purchase, Accounting, Multi-Branch
**Size: L | Goal: The store can be restocked and its books actually balance, and a second branch can exist.**

**In:**
- **Purchase Management:** direct GRN→Bill flow first (skip formal PO approval workflow initially), supplier management, purchase returns, landed cost
- **Ledger & Accounting:** chart of accounts, double-entry auto-journaling from every sale/purchase, party ledgers (customer/supplier/bank), Day Book/Cash Book
- **Multi-Branch:** Branch hierarchy fully live, inter-branch transfer workflow (Pending → Approved → In-Transit → Completed), consolidated cross-branch reporting
- **Pricing Management:** cost/MRP/selling price tiers, margin calculation, bulk price updates
- **Product Search:** OpenSearch goes live here — catalog size and multi-branch stock lookups now justify it (≤100ms target)

**Deferred:** formal PO approval workflow, bank reconciliation, wholesale pricing, CRM.

**Definition of Done:** You can run 2+ branches, restock them from suppliers, and your accounting ledger reconciles against actual sales/purchases without manual patching.

---

## Phase 3 — Customer Engagement: CRM, Loyalty, Promotions
**Size: M | Goal: Repeat customers become visible and rewardable.**

**In:**
- **Customer Management:** B2C/B2B profiles, auto-segmentation (VIP/Regular/New/Dormant), multi-location unified profile
- **Promotions & Loyalty:** discount hierarchy, coupon codes, BOGO/bundles/volume discounts, loyalty points earn/redeem
- **Notifications (expanded):** move beyond basic email receipts to SMS/WhatsApp for receipts, low-stock alerts, payment reminders — this is also where the "Notification Microservice" from your BRD becomes worth splitting out of the Go monolith (see Migration Triggers in the tech stack doc)

**Deferred:** credit facility/B2B payment terms (bundle with Phase 4's accounting depth), AI-driven promotion recommendations.

**Definition of Done:** Returning customers get recognized, discounted, and notified automatically; you can run a real promotion campaign end to end.

---

## Phase 4 — Compliance & Financial Depth
**Size: L | Goal: The system is fully GST/audit compliant and the books survive a real audit.**

**In:**
- **E-Invoicing** (IRN generation, QR code) and **E-Way Bill** (auto-generate for interstate >₹50k)
- **GSTR-1 / GSTR-3B** export (JSON for GST portal)
- **Bank Reconciliation** (statement import, auto-match)
- **Reconciliation & Audit:** cash, payment gateway, inventory variance workflows; immutable audit trail with 7-year retention
- **B2B credit facility:** credit limits, aging, auto-block on breach — this is also the natural point to add formal PO approval workflow if you have suppliers who need it

**Definition of Done:** You could pass a GST audit and a financial audit using only what the system produces.

---

## Phase 5 — HR & Payroll
**Size: L | Goal: Staff costs run through the system instead of a spreadsheet.**

**In:**
- Employee records, attendance (POS clock-in/out), shift management
- Payroll run (earnings/deductions, statutory: PF/ESI/TDS/PT/LWF, challan generation)
- Commission engine (net-sales-after-returns model, category-weighted, tiered)

**Note:** This module is largely self-contained and can be deferred further or pulled earlier depending on whether you're hiring staff before or after other phases — it has the fewest dependencies on everything else.

**Definition of Done:** A real payroll run for your pilot store's staff, statutory deductions included, executes correctly.

---

## Phase 6 — AI Platform v1
**Size: L | Goal: The AI capabilities that differentiate this from a generic POS.**

**In (roughly in order of ROI vs. effort for a solo builder):**
1. **Reorder suggestions** (sales velocity + lead time) — extends the Phase 2 reorder management you already have with real intelligence
2. **Recommendation engine** (cross-sell/up-sell using transaction co-occurrence — classic "customers also bought," doesn't need deep ML to start)
3. **NLP-BI** ("ask a question, get a chart" against your Reporting data — an LLM-API-backed feature, not a custom model)
4. **Demand forecasting** (time-series, needs real transaction history — this is why it's deferred this long)
5. **Fraud/anomaly detection** on discounts, returns, voids
6. **OCR** for purchase-invoice data entry
7. **AI Copilot/chatbot** for merchant admins

**Definition of Done:** At least reorder suggestions and the recommendation engine are live and demonstrably improving a real decision (what to restock, what to upsell).

---

## Phase 7 — Wholesale/B2B & Omnichannel
**Size: L**

**In:** formal B2B quotations, wholesale price lists at scale, e-commerce/omnichannel storefront with order sync back into the same inventory.

**Definition of Done:** The same catalog and stock serve both a walk-in retail customer and a B2B wholesale order without double-entry.

---

## Phase 8 — Vertical Expansion
**Size: ongoing, per new industry**

Because the masters were built generic from Phase 1 onward, each new vertical is mostly **configuration**, not new code:
- **Grocery/FMCG:** activate batch/lot + expiry tracking, weighing-scale barcode integration, switch valuation to FIFO for perishables
- **Pharmacy:** activate prescription linkage, stricter expiry compliance, regulatory reporting
- **Apparel:** you're already close from Phase 1's variant matrix — mainly needs seasonal/collection catalog features
- **Jewelry, Steel, Chemicals, Electronics:** attribute-set configuration (purity/weight for jewelry, technical specs for electronics, etc.) — validates the "configurable masters, not industry-specific code" promise in your BRS

---

## Solo-Builder Notes

A few honest calls worth making as you execute this, given it's 1-2 people:

- **Don't build what you can configure or buy.** Use existing GST-calculation logic patterns, existing e-invoice/e-way-bill integration SDKs (several Indian GSP providers offer these) rather than building NIC portal integration from scratch in Phase 4. Use `shadcn/ui` + Tailwind component libraries rather than designing a UI kit from zero.
- **Auth is the one area worth revisiting.** You chose custom-built auth to keep a future Keycloak/managed-IdP swap easy — that's the right long-term call, but it's also one of the highest-effort, highest-risk-if-wrong pieces (MFA, SSO, session security) to hand-build solo. If Phase 0/1 velocity is suffering, dropping in Keycloak now (it's self-hosted and fits your local-first requirement just as well) and building the thin custom layer on top of it — rather than fully custom underneath — is a reasonable mid-course correction. Worth revisiting once you're actually in it, not a decision to force now. **(User's explicit answer, on record: keep custom-built auth as-is — "I need all this in my control in my service." Do not re-raise this unless the user brings it up again.)**
- **Phase 1 is the only phase that really matters for validating the business.** Everything from Phase 2 onward can slip without risk; if Phase 1 doesn't prove a real store can run on this, the rest of the roadmap is moot. Resist the pull to add "just one more module" before Phase 1 is genuinely done.

---

## Summary Timeline (dependency order, not calendar)

| Phase | Focus | Size | Status |
|---|---|---|---|
| 0 | Walking skeleton | S | Done |
| 1 | Core retail loop (real MVP) | XL | In progress — see open items above |
| 2 | Purchase, Accounting, Multi-branch | L | Not started |
| 3 | CRM, Loyalty, Promotions | M | Not started |
| 4 | GST compliance depth, Audit | L | Not started |
| 5 | HR & Payroll | L | Not started |
| 6 | AI Platform v1 | L | Not started |
| 7 | Wholesale/B2B, Omnichannel | L | Not started |
| 8 | Vertical expansion (grocery, pharmacy, apparel, ...) | Ongoing | Not started |
