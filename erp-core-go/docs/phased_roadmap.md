# Universal Retail & Wholesale ERP — Phased Build Roadmap

**Version:** 1.0 | **Date:** September 14, 2026
**Basis:** Finalized Tech Stack (see `tech_stack_decision.md`) + BRS + POS FRD
**Builder capacity:** Solo / 1-2 people, no fixed deadline — sequenced by dependency and value, not by calendar
**Reference vertical:** Sports & general retail first (masters kept generic so grocery/pharmacy/apparel/etc. slot in later without code changes)

---

## How this roadmap is sequenced

With 1-2 people against 40+ modules, the only way this gets built is if every phase (a) produces something real you could demo or run a pilot store on, and (b) doesn't require the _next_ phase's work to function. So the order below follows the actual dependency chain of a retail transaction — you can't do Purchase before Inventory exists, you can't do Accounting before Sales generates ledger entries, you can't do AI forecasting before there's transaction history to forecast from — rather than the BRS's module numbering.

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

**Status: done — but this was inaccurate for a long time.** The Flutter half (see `phase0_1_design.md` §4) was real from the start. The Next.js admin half was marked done without ever being built — `erp-web-admin` didn't exist as an actual repo until Phase 2's Purchase/Accounting work needed a UI and the gap was caught (see that phase's status below). It exists now: `../erp-web-admin`, login screen included, genuinely closing this line.

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

**Status: functionally complete against this phase's Definition of Done.** Every item on the original "Still open" list is now closed except on-device hardware verification, which is environment-blocked rather than a code gap (see bottom of this section). Built: catalog barcode lookup, full cart → add-line → discount → delete-line → checkout with optimistic-locking stock reservation, idempotent checkout, session-tiered auth (login/PIN-login/refresh/logout), failed-login lockout, the reservation-expiry sweeper, inventory adjustments with audit trail, and basic reports (see `phase0_1_design.md` §6 for the full list and how each was verified live against this repo's own `docker-compose.yml`).

**§6.1 of that doc is worth reading before touching anything else here**: the hardening pass found that `docker-compose.yml`'s `api` service was connecting to Postgres as a superuser (`app_user`, via `POSTGRES_USER`), which silently disabled every RLS policy in the system since Phase 0 — the multi-tenancy guarantee this whole codebase is built around had never actually been enforced by the database in this stack. Fixed (a dedicated non-superuser `erp_app` role, `migrations/004_least_privilege_app_role.sql`) and re-verified live, but **if you've deployed this anywhere outside the shipped docker-compose, check that deployment's DB role the same way** — this class of bug (an admin/master role standing in for the app's own role) isn't specific to Docker.

**A second hardening pass then closed everything else that didn't require a new architectural decision** — general permission-code enforcement, `PATCH`/`customer`/`receipt`/`void` endpoints, pre-tax GST discount treatment, and PIN-login UI in Flutter (see `phase0_1_design.md` §6.5 for what closed and how each was verified live).

**Offline sync — the last item on the previous open list — is also closed** (`/sync/push`/`/sync/pull`, and a `sqflite`-backed local store in the Flutter app; see `phase0_1_design.md` §6.6). One real gap in how it was verified: there's no Android/iOS device or emulator in this environment, so the offline flow was verified by a real SQLite-backed test (`sqflite_common_ffi`) plus static analysis, not by clicking through the app on a device — **do that before trusting this in front of a real cashier.**

**Barcode & label generation, printer integration — closed** (`internal/printing`, `internal/catalog/ean13.go`/`barcode_assign.go`; see `phase0_1_design.md` §3.8 for the full endpoint reference). Found still-open during an explicit "confirm Phase 1 is 100% before starting Phase 3" audit the user asked for — this item was named in Phase 1's own Definition of Done ("receipt-printing") and hadn't actually been built, so it was closed before Phase 3 started rather than carried forward as debt:

- `POST /products/variants/{id}/barcodes` — the FRD's "Hybrid: use supplier barcode if exists, else auto-generate" (`pos_frd_complete.md` §12). Auto-generated codes are sequential EAN-13s in GS1's reserved in-store-use prefix range (20-29), which can never collide with a real product's actual assigned barcode.
- `GET /products/variants/{id}/label?template=standard|compact` and `GET /sales/orders/{id}/receipt/print` — a real ESC/POS driver (`internal/printing`), not a stub, chosen as the roadmap's named "one printer integration" since a single ESC/POS thermal printer commonly serves both roles on a small retail counter.

Verified without physical printer hardware the same way offline sync was verified without a physical device: `internal/printing.Decode` parses generated byte streams back into structured commands, so unit tests assert the actual bytes a real printer would receive (barcode symbology byte + data, every text run) are correct — not just that the code compiles and runs. Beyond the unit tests, live-verified against the real backend: printed a real finalized order's receipt and hex-dumped it (content and ESC/POS framing both correct); fetched both label templates for the seed variant and hex-dumped them (confirmed the exact `GS k` barcode command — symbology byte `0x43` = EAN-13, correct length, correct 13-digit code); verified all four barcode-assignment paths (auto-generate, supplier-provided, invalid EAN-13 check digit rejected, duplicate code rejected with `409 BARCODE_EXISTS`); full Go test suite and all 4 Playwright specs still pass. EAN-13 checksum math is checked against GS1's own published worked examples, not just internally-consistent arithmetic. **On-device verification against real printer hardware remains open** — same caveat already on file for offline sync.

**Product & Catalog Management's actual gap — `POST/PATCH /products` — is now closed** (found during a 2026-09-22 "review previous phases for anything missing" audit, `migrations/019_catalog_management.sql`, `internal/catalog/{products_write,taxonomy}.go`): despite being named in `phase0_1_design.md`'s own contract table since Phase 0/1, no code path anywhere had ever created a second product beyond the one hard-seeded in `migrations/002_seed.sql` — every phase since, including Purchase, Accounting, Pricing, Search, CRM, Promotions, and three reconciliation types, verified itself against that same one seed product. Categories, brands, and tax slabs had the identical problem (schema since Phase 0, zero API ever). See `phase0_1_design.md` §3.19 for the full account, including two real checkout bugs this fix surfaced live (a brand-new variant had no `stock_levels` row, and `Checkout` never rejected an empty cart) and the resulting `erp-web-admin`/`erp-pos-flutter` work this pass also closed (a New Product screen, a Reports screen, an Inventory screen, a widespread `<Select>` label-display bug across ~19 files, and a large Flutter POS feature-parity pass — discounts, promotions, coupons, loyalty redemption, credit payment, void, and real receipt printing all previously unwired on the client that's supposed to use them).

**Password-reuse history and a real forgot-password flow are also now closed** (same-day follow-up audit, `internal/authn/password_reset.go`, `migrations/020_password_reset.sql`) — `POST /auth/forgot-password`/`POST /auth/reset-password`, a single-use one-hour emailed code, a 5-password reuse check, and every other session for that user revoked on a successful reset. See `phase0_1_design.md` §3.20 for the full account, including `erp-web-admin`'s new `/forgot-password` screen.

**Standalone `POST/DELETE /inventory/reservations` is also now closed** (2026-09-22, `internal/sales/manual_reservation.go`) — a stock hold outside the cart flow (e.g. a phone/counter order promised before any cart exists), reusing the same optimistic-locking `reserveStock`/`releaseReservation` helpers the cart flow already calls, gated by `inventory.adjust`. Verified live: reserving 5 units correctly moved `reserved` 0→5 and `available` 9→4; releasing moved it back to the exact starting numbers; over-reserving past available stock returned `409 STOCK_UNAVAILABLE`; a POS User (no `inventory.adjust`) was denied with `403 FORBIDDEN`; and a cart-owned reservation (created via the normal `AddLine` path) was correctly refused with `409 RESERVATION_CART_OWNED` rather than silently double-releasing stock `DeleteLine` already owns. See `phase0_1_design.md` §3.3.

**Still open:**

1. On-device verification of the offline sync flow (see above), of the printer integration, and of the 2026-09-22 Flutter parity pass — real Android/iOS hardware and a real network thermal printer have never been available in this environment; everything above is verified against a real backend over HTTP/TCP instead, which is meaningfully better than a stub but not the same as an actual cashier's hands on an actual device. This is an environment limitation, not a code gap — it can't be closed without physical hardware.

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

**Status: in progress. Sub-area 1, Purchase Management, is built and verified live** (`migrations/006_purchase.sql`, `internal/purchase/`) — direct GRN → Bill only, no formal PO step, matching this section's own scoping:

- `POST/GET /suppliers`, `PATCH /suppliers/{id}`
- `POST /purchase/grn` (draft), `POST /purchase/grn/{id}/lines`, `GET /purchase/grn/{id}`, `POST /purchase/grn/{id}/complete`
- `POST /purchase/bills`, `GET /purchase/bills/{id}`, `POST /purchase/bills/{id}/payments`
- `POST /purchase/returns`

Completing a GRN allocates freight/other charges across lines proportionally (landed cost), increments `stock_levels.on_hand`, and updates `product_variants.cost_price` via Weighted Average Cost — `(existing_value + new_value) / (existing_qty + new_qty)`, per `pos_frd_complete.md` §2 — computed across the variant's stock in every branch, since `cost_price` lives on `product_variants`, not per-branch. Verified live against a hand-computed example (10 existing units @ ₹1200, 10 new units landing at ₹1260/unit after ₹100 freight → new cost ₹1230, exact match) plus the full bill/payment/overpayment-rejection and purchase-return/insufficient-stock-rejection paths, and RLS confirmed on all four new tables. No new permission gates added — Purchase Management has no FRD-specified approval step for Phase 2 (formal PO approval is explicitly deferred), so it follows the same open-to-any-authenticated-user pattern as catalog/sales endpoints.

**Sub-area 2, Ledger & Accounting, is also built and verified live** (`migrations/007_accounting.sql`, `internal/accounting/`):

- Chart of accounts (`GET/POST /accounting/accounts`) — 11 system-default accounts seeded (Cash, Bank, Card/UPI Clearing, GST Input/Output, Receivable, Payable, Inventory Asset, Sales Revenue, Inventory Shrinkage), following the FRD's Assets/Liabilities/Equity/Income/Expenses code-numbering convention
- Double-entry posting (`accounting.PostJournalEntry`) called from inside the same transaction as the event that causes it — a sale, void, purchase bill, bill payment, purchase return, or inventory adjustment and its journal entry commit or roll back together, never one without the other
- `POST /accounting/journal-entries` (manual entries, post immediately — no approval workflow, same "skip the formal step" simplification as Purchase's PO stage)
- `GET /accounting/party-ledger`, `GET /accounting/day-book`, `GET /accounting/cash-book`

Verified live end to end: a ₹2712.82 cash sale posted exactly `Dr Cash 2712.82 / Cr Sales Revenue 2299.00 / Cr GST Output Payable 413.82`; voiding it posted the exact mirror image (net effect zero, original entry never deleted — an immutable audit trail); a GRN→bill posted `Dr Inventory 6050 / Dr GST Input 1080 / Cr Payable 7130` (landed cost, matching the figure GRN completion used for WAC); paying that bill and then returning stock against the same supplier walked the party ledger balance from -7130 → 0 → back into payable, exactly; the cash book correctly filtered to only Cash/Bank/clearing lines (net movement matched by hand); and an unbalanced manual entry was correctly rejected while a balanced one posted. RLS confirmed on all three new tables.

**Known simplification:** inventory adjustments book against a single "Inventory Shrinkage" plug account regardless of direction (gain or loss) — the FRD doesn't specify separate gain/loss accounts for this case, and splitting them wasn't worth the extra accounts for Phase 2.

**UI for both sub-areas above now exists** — `../erp-web-admin` (see that repo's own `CLAUDE.md`), covering suppliers, GRN, bills, purchase returns, chart of accounts, manual journal entries, party ledger, day book, and cash book, verified end-to-end with a real Playwright test against the live backend (`e2e/purchase-and-accounting.spec.ts`). This also closes Phase 0's long-inaccurate "Next.js admin" line above.

**Sub-area 3, Multi-Branch, is also built and verified live** (`migrations/008_multi_branch.sql`, `internal/branches/`):

- `POST/GET /branches`, `PATCH /branches/{id}` — branch management **never had an API at all** before this; a branch could only ever be created via raw SQL. Closed alongside the transfer workflow, not as a separate afterthought.
- `POST /branch-transfers` → `approve`/`reject` (permission-gated: `branch_transfer.approve`, Branch Manager/Merchant Admin only) → `dispatch` (all-or-nothing stock check against _available_, not just on-hand, stock at the source) → `complete` (adds stock at the destination; supports a per-line `received_quantity` override for transit discrepancies) → `cancel` (before dispatch only)
- `GET /reports/consolidated-sales`, `GET /reports/consolidated-stock` — the "consolidated cross-branch reporting" the roadmap's In list named, not scoped to one branch like the existing daily-sales/stock-summary reports

Deliberately **no journal entry for a normal transfer** — both branches share one merchant-level chart of accounts, so moving inventory between them doesn't change the total Inventory Asset value. Only a **discrepancy** (received ≠ sent — breakage/loss in transit) is journaled, at the variant's current cost price, against the same Inventory Shrinkage account inventory adjustments use.

Verified live end to end: dispatching correctly deducted exactly the requested quantity from the source (10 → 6) and rejected cleanly with no partial deduction when a second transfer requested more than was available; completing with a 1-unit shortfall (sent 4, received 3) added exactly 3 to the destination and posted a ₹1,200.00 Inventory Shrinkage entry (1 unit × that variant's ₹1,200 cost price, exact match); consolidated stock correctly showed 6 + 3 = 9 total across branches, accounting for the lost unit; reject and cancel paths both verified; RLS confirmed on both new tables; a Branch Manager could approve where a POS User was correctly denied with `403 FORBIDDEN`.

**Known simplification:** dispatch/complete aren't restricted to staff physically at the relevant branch — only `approve`/`reject` are permission-gated. Enforcing branch-scoped access would need password-login sessions to carry a `branch_id` claim, which today only PIN-login sets (see `internal/authn/claims.go`).

**Multi-Branch's UI is also built and verified live**, in the same pass as the backend this time, not as an afterthought — `../erp-web-admin`'s Branches and Branch Transfers screens (branch list/create, transfer request with barcode-driven line entry, and a status-driven detail page: Approve/Reject → Dispatch → Complete-with-optional-discrepancy → Cancel). Verified end to end with a real Playwright test (`e2e/multi-branch.spec.ts`) that logs in as three different sessions in sequence (POS User denied approval → Branch Manager approves → POS User dispatches and completes) against the live backend — not mocked.

**Phase 2 is now feature-complete across Purchase, Accounting, and Multi-Branch, backend and UI both**, for everything the roadmap's original "In" list named.

**Sub-area 4, Pricing Management, is also built and verified live** (`migrations/009_pricing.sql`, `internal/pricing/`, `internal/catalog/products.go`):

- `GET /products` — the catalog-browse endpoint `phase0_1_design.md` §3.2 documented from Phase 0/1 but never built (Phase 1 only ever needed the barcode-scan path). Returns each product's variants with cost/MRP/selling price and computed margin %/markup %. Needed regardless of pricing specifically — you can't bulk-reprice a catalog you can't list.
- `POST /pricing/calculate` — the FRD's cost-plus and target-margin calculator tools in one endpoint (give cost + markup% or cost + margin%, get the selling price and both figures back).
- `PATCH /pricing/variants/{id}` — the real write, gated by a new `pricing.manage` permission (Branch Manager/Merchant Admin only). Blocks a resulting negative margin unless `override:true` is passed — the FRD's "Block negative margin sales (override required)," applied at price-setting time.
- `POST /pricing/bulk-update/preview` and `.../apply` — filter by category/brand/price range, adjust by percent/fixed/set, with optional rounding to a denomination. Preview and Apply share the exact same computation path, so what you previewed is guaranteed to be what Apply does.
- `price_history` — every price change, individual or bulk, is logged (field, old/new value, reason, who) — the FRD's "Price history audit trail."

Scope note: the roadmap's own "Deferred" line for this phase names **wholesale pricing** alongside CRM — so multiple named price tiers (Retail/Wholesale/VIP/Special), customer-specific pricing, price lists assigned to segments/branches/channels, scheduled future-dated changes, and dynamic (time/day/season) pricing are all real FRD §11 content deliberately **not** built here; that's Phase 7 (wholesale/B2B) and Phase 3 (CRM segments) territory.

Verified live end to end: `GET /products` returned the seed product with margin 47.80%/markup 91.58% computed correctly; the calculator's target-margin mode produced an exact ₹2,000.00 for cost ₹1,200 at 40% margin; a Branch Manager was blocked from setting a below-cost price and then allowed through with `override:true`; a bulk +10%-with-rounding preview correctly matched what apply actually wrote (₹1,000 → ₹1,100); a POS User was denied both the single-update and bulk-apply endpoints with `403 FORBIDDEN`; a subsequent sale correctly picked up the newly-applied selling price; RLS confirmed on `price_history`.

**UI, built in this same pass:** `erp-web-admin`'s Pricing screen (product/variant browse with margin display, a calculator widget, single-variant editing with the negative-margin/override flow, and bulk update preview→apply) — verified with a real Playwright test (`e2e/pricing.spec.ts`).

**Sub-area 5, Product Search, is also built and verified live** (`migrations/010_search.sql`, `internal/search/`, `docker-compose.yml`'s new `opensearch` service) — the one sub-area that was genuinely new infrastructure, not just new endpoints on the existing Postgres/`WithTenant` pattern:

- New service: `opensearchproject/opensearch:2.17.0`, single-node, security plugin disabled (`plugins.security.disabled=true`) — the same "this compose file IS your dev environment" reasoning already applied to `DEV_AUTH_TOOLS_ENABLED`. `internal/search.Client` is a small hand-rolled REST wrapper (`net/http` + `encoding/json`) over OpenSearch's plain JSON API rather than the `opensearch-go` SDK — that surface (index/search/bulk) is a handful of JSON requests, not enough to justify this repo's first new dependency since Phase 0 (see `tech_stack_decision.md` §3.1's as-built note on staying dependency-light).
- `GET /products/search?q=&category_id=&brand_id=&min_price=&max_price=&page=&limit=` — free-text, typo-tolerant search (`multi_match` + `fuzziness: AUTO` across name/SKU/HSN/category/brand) with the same filters `GET /products` already had. This is the endpoint the FRD's ≤100ms target is actually about; `GET /products` (§3.2, Pricing's browse view) stays the plain-Postgres exact/`ILIKE` list.
- `POST /search/reindex` — rebuilds the caller's tenant's slice of the index from Postgres in one bulk call. Gated by a new `search.reindex` permission (Merchant Admin only, per `migrations/010_search.sql`) since it's a bulk operation, not day-to-day.
- Incremental sync: `PATCH /pricing/variants/{id}` and a successful bulk-apply item now reindex that one variant after the price change commits — the only product-mutating endpoints that exist today (there's still no general product/variant CRUD; see `phase0_1_design.md` §3.2's `POST/PATCH /products` rows, still unbuilt). Reindexing is best-effort and logged-not-fatal: a search-index write failure never rolls back or fails a price change that already committed in Postgres, the actual system of record. `POST /search/reindex` remains the full-catalog catch-up path for everything the incremental hooks can't reach yet.
- Tenant isolation has no RLS equivalent in OpenSearch — the entire boundary is `internal/search.Params.TenantID`, sourced only from `authn.FromContext` claims, applied as a hard `term` filter on every query. Verified live by writing a document under a different `merchant_id` directly into the index (bypassing the API) and confirming a real tenant's search never surfaces it.
- Graceful degradation verified live: stopping the `opensearch` container mid-session makes `GET /products/search` and `POST /search/reindex` answer `503 SEARCH_UNAVAILABLE` immediately, while every other endpoint (`/products`, `/accounting/accounts`, etc.) keeps working — search is layered on top of the transactional core, never load-bearing for it.

Verified live end to end: `EnsureIndex` runs automatically on API startup and is idempotent (confirmed the `products` index exists via `GET /_cat/indices` after a fresh container start); reindex correctly indexed the seed catalog; a fuzzy query with a deliberate typo ("crikcet") still matched "SG Cricket Bat"; an exact SKU query and a `min_price` filter that excludes the seed product both returned the correct result sets; a price change via `PATCH /pricing/variants/{id}` was reflected in a subsequent search with no explicit reindex call; a POS User was denied `POST /search/reindex` with `403 FORBIDDEN`; existing endpoints (`/products`, `/accounting/accounts`) regression-checked clean with OpenSearch back up.

**UI, built in this same pass:** `erp-web-admin`'s Search screen (free-text query box with debounced search, category/brand/price filters, results table linking into Pricing for edits) — verified with a real Playwright test (`e2e/search.spec.ts`).

**Bug found immediately after shipping this, by the user searching a real product name and getting zero results:** the `opensearch` service in `docker-compose.yml` had no volume — every container recreation started it as a brand-new empty node, silently discarding every indexed document while the API kept running normally against everything else. `EnsureIndex` recreated the (now-empty) index on the next API boot without complaint, so nothing _looked_ broken until a real search came back empty. Fixed two ways, not just one: (1) `opensearch-data` is now a named, persisted volume, same treatment `pgdata` already had, so an ordinary restart no longer loses anything; (2) `search.BackfillAllTenants` (`internal/search/index.go`) runs automatically at API startup whenever the index comes up with zero documents, rebuilding every tenant's slice from Postgres unattended — a real disaster-recovery/fresh-environment case the volume fix alone wouldn't cover. Verified live: recreated the `opensearch` container to reproduce the empty-index bug, confirmed the exact failure (`"SG Cricket Bat"` search returning zero results), then rebuilt with both fixes and confirmed the startup log showed the automatic backfill running and the search succeeding immediately after, and that the index survives a subsequent `opensearch` restart.

**Phase 2 is now fully complete** — Purchase, Accounting, Multi-Branch, Pricing, and Product Search, backend and UI, matching everything the roadmap's original "In" list named for this phase, including the sub-area (Search) explicitly flagged as new infrastructure rather than a continuation of the existing pattern.

---

## Phase 3 — Customer Engagement: CRM, Loyalty, Promotions

**Size: M | Goal: Repeat customers become visible and rewardable.**

**In:**

- **Customer Management:** B2C/B2B profiles, auto-segmentation (VIP/Regular/New/Dormant), multi-location unified profile
- **Promotions & Loyalty:** discount hierarchy, coupon codes, BOGO/bundles/volume discounts, loyalty points earn/redeem
- **Notifications (expanded):** move beyond basic email receipts to SMS/WhatsApp for receipts, low-stock alerts, payment reminders — this is also where the "Notification Microservice" from your BRD becomes worth splitting out of the Go monolith (see Migration Triggers in the tech stack doc)

**Deferred:** credit facility/B2B payment terms (bundle with Phase 4's accounting depth), AI-driven promotion recommendations.

**Definition of Done:** Returning customers get recognized, discounted, and notified automatically; you can run a real promotion campaign end to end.

**Status: now fully complete (all three sub-areas — see below). Sub-area 1, Customer Management, is built and verified live** (`migrations/011_customers.sql`, `internal/customers/`) — B2C/B2B profiles and auto-segmentation, going first because Promotions & Loyalty and expanded Notifications (this phase's next two sub-areas) both need real customer profiles/segments to target:

- `POST/GET/PATCH /customers`, `GET /customers/{id}` — full CRM registration (mandatory name/phone/email per the FRD, B2B additionally requiring a GSTIN), search/filter by name/phone/email/customer_type/segment, and a detail view with consolidated cross-branch purchase history.
- Auto-segmentation (VIP/Regular/New/Dormant) computed on read from real `sales_orders` history, matching `pos_frd_complete.md` §6's exact thresholds — see `phase0_1_design.md` §3.10 for the full rule and how each branch was verified live.

**Deferred, matching this sub-area's own scope** (see the migration's header comment): credit facility/B2B payment terms (bundled into Phase 4's accounting depth, per this phase's own "Deferred" line above), loyalty points and customer-level discounts (the next sub-area, Promotions & Loyalty, not this one).

**UI, built in this same pass:** `erp-web-admin`'s Customers screen (search/filter list with segment badges, a B2C/B2B registration form with conditional GSTIN validation, and a detail page with inline profile editing and purchase history) — verified with a real Playwright test (`e2e/customers.spec.ts`).

**Sub-area 2, Promotions & Loyalty, is also built and verified live** (`migrations/012_promotions_loyalty.sql`, `internal/promotions/`, `internal/loyalty/`) — every hierarchy item `pos_frd_complete.md` §5 names except coupon payment-method restriction (deferred — payment method isn't known until checkout, after discounts already apply; see the migration's header comment) and "Bundles" (already covered by Phase 1's composite-product model, not a new promotion type):

- `POST/GET/PATCH /promotions` — percent/fixed/BOGO/volume/min_value promotion types, product/category/order-scoped application, `target_segment` (folds hierarchy item 4, "customer-level discounts," into promotions rather than a separate mechanism), exclusive-vs-stackable, and an active date/day-of-week/time-of-day window (covers "time-based/happy hours" without a separate promo_type). Gated by a new `promotions.manage` permission.
- `POST/GET/PATCH /coupons` and `POST /sales/orders/{id}/coupons` — usage caps (total and per-customer), validity window, minimum purchase, branch, and channel constraints, all enforced at apply-time; one coupon per order.
- `POST /sales/orders/{id}/promotions/apply` — evaluates and auto-applies eligible promotions against a cart, respecting each promotion's stacking rule.
- `GET/PATCH /loyalty/config`, `GET /customers/{id}/loyalty`, `POST /sales/orders/{id}/loyalty/redeem` — configurable earn/redeem rates, a "rolling" whole-balance expiry (computed on read, no sweeper), and automatic point-earning on checkout finalize that correctly skips itself when the same order already redeemed points (the FRD's "cannot earn and redeem in same transaction").

The one piece of real engineering this sub-area needed beyond CRUD: every layer (promotional, coupon, customer-level, loyalty redemption, and — now updated — manual) applies through one shared function, `sales.ApplyDiscountLayer`, which distributes a resolved rupee amount across lines' _current remaining_ taxable value and adds it to `discount_amount`, so multiple layers stack instead of one overwriting another. This changed `POST /sales/orders/{id}/discounts` (manual, hierarchy item 6, previously the only discount source and the only one Phase 1 built) from overwrite to additive — a single manual-only discount still behaves identically (0 existing + share = share), but a cart that also has a promotion/coupon/redemption applied now correctly keeps all of them.

Verified live end to end against the seeded merchant and cricket bat: a category-scoped 10% exclusive promotion auto-applied, a flat-₹100 coupon stacked on top of it (not overwriting it), a second coupon attempt on the same order correctly rejected, a manual 5% discount stacked on top of both, checkout finalized correctly, the customer earned the correct floor-divided loyalty points, a second order redeemed those points for the correct rupee discount and checked out, a follow-up balance check confirmed that redeeming order did _not_ also earn, a `buy 2 get 1 free` BOGO promotion against 3 units discounted exactly one unit's price, and a coupon with an unreachable minimum purchase correctly rejected a small order. `promotions.manage` confirmed denying a POS User with `403 FORBIDDEN`. See `phase0_1_design.md` §3.11 for the full endpoint reference and exact verified figures.

**UI, built in this same pass:** `erp-web-admin`'s Promotions & Loyalty screen (`/promotions`) — promotion CRUD (a product picker sourced from `GET /products`, since there's still no category-list endpoint to build one from), coupon CRUD, and a loyalty settings form. Deliberately CRUD/config only — this app has no cart/checkout UI (that's `erp-pos-flutter`'s job), so _applying_ a promotion/coupon/redemption to a live sale isn't exposed here, matching the same boundary Pricing's screen already draws. A customer's loyalty balance/ledger is shown read-only on their existing detail page instead of a new one, since it's customer-scoped state, not merchant-wide config. Verified with a real Playwright test (`e2e/promotions.spec.ts`) that creates and deactivates a promotion and a coupon, confirms a POS User is denied writing, and — since the UI itself can't produce a finalized sale — seeds one real checkout via a direct API call to confirm the customer detail page's loyalty section renders the resulting earned points and ledger row correctly.

**Sub-area 3, Notifications (expanded), is also built and verified live** (`migrations/013_notifications.sql`, `internal/notifications/`), closing Phase 3's last open item:

- `POST /sales/orders/{id}/receipt/notify`, `PATCH /inventory/reorder-point`, `GET /notifications` — see `phase0_1_design.md` §3.12 for the full endpoint reference.
- Receipts send automatically at checkout (email + phone-channel, whichever contact info the customer has) via a new `internal/notifications.Handler`, injected into `sales.Handler` the same way `Loyalty` is.
- Low-stock alerts and (a scope substitution — see the migration's header comment) supplier bill payment reminders each run from their own periodic sweeper, mirroring `internal/sales.RunExpirySweeper`'s existing per-tenant pattern.
- No real SMS/WhatsApp vendor integration (no provider chosen, no credentials in this environment) — a real SMTP email provider plus a console/log stand-in for phone channels, the same "real interface, dev-only stand-in for the piece needing credentials this environment doesn't have" pattern already used for `DEV_AUTH_TOOLS_ENABLED` and `internal/printing`.

Verified live end to end: a real checkout produced both an email and an SMS receipt notification logged as `sent`; the resend endpoint worked on demand; setting a variant's reorder point above its on-hand quantity and sweeping produced a low-stock alert to both seeded manager users, a repeat sweep produced no duplicate, and recovering stock then sweeping again correctly cleared the alert marker; a synthetic overdue supplier bill produced a correctly-worded payment reminder, again with confirmed same-day dedup; `GET /notifications` confirmed denying a POS User with `403 FORBIDDEN`.

**UI, built in this same pass:** `erp-web-admin`'s Notifications screen (`/notifications`) — the sent-notification audit log and low-stock reorder-point configuration (this app has no general Inventory screen yet, so reorder-point setting lives here rather than there). The first screen in this app whose _read_, not just its writes, is permission-gated — a denied user sees the API's own permission message rather than a blank page. Verified with a real Playwright test (`e2e/notifications.spec.ts`) that seeds a real finalized sale via a direct API call to populate the log, sets a reorder point end-to-end, and confirms a POS User is denied the log (but can still look up stock) and denied saving a reorder point.

**Phase 3 (CRM, Loyalty, Promotions) is now fully complete, backend and UI both** — Customer Management, Promotions & Loyalty, and Notifications, matching everything the roadmap's original "In" list named for this phase.

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

**Status: in progress. Sub-area 1, B2B Credit Facility, is built and verified live** (`migrations/014_credit_facility.sql`, `internal/customers/credit.go`, `internal/customers/reminder_sweeper.go`, plus `internal/sales.Checkout` changes) — chosen to go first among this phase's five sub-areas because it needed no new vendor decision (unlike e-invoicing/e-way bill's GSP choice) and it closes two gaps this repo's own docs had already flagged as deferred here: `migrations/011_customers.sql`'s credit facility deferral, and `migrations/013_notifications.sql`'s customer-facing payment reminder deferral:

- `PATCH /customers/{id}/credit` (credit limit/payment terms/credit hold, gated by a new `credit.manage` permission), `GET /customers/{id}/credit` (outstanding + aging), `POST /customers/{id}/payments` (record a payment against a specific credit sale).
- `POST /sales/orders/{id}/checkout` now accepts a `credit` payment method, booking that portion to the Receivable account seeded since Phase 2 and never used until now — auto-blocked (`403 CREDIT_HOLD` / `409 CREDIT_LIMIT_EXCEEDED`) the same transaction it would otherwise post in.
- A new sweeper (`internal/customers.RunReceivableReminderSweeper`) reminds customers of their own overdue balance — the mirror image of `internal/purchase`'s existing supplier-bill reminder sweeper.

Verified live end to end: credit sales within and over limit, a partial-cash/partial-credit retry, credit hold blocking regardless of remaining limit, a payment correctly zeroing an invoice's outstanding, an overpayment correctly rejected, the party ledger (built generic since Phase 2, this is its first real customer-side use) matching the fast `credit_amount`/`credit_paid` columns exactly, and the reminder sweeper correctly notifying customers (not staff) with dedup confirmed. **One real bug found and fixed live**: `journal_entries.source_type`'s `CHECK` constraint had never been widened for the new `receivable_payment` source type — see `phase0_1_design.md` §3.13 for the full account and exact verified figures.

**UI, built in this same pass:** `erp-web-admin`'s Credit section, added to the existing customer detail page (`/customers/[id]`) rather than a new route — settings (gated by `credit.manage`, attempted-and-toast-erred on `403`), outstanding balance, an aging breakdown, and a per-invoice "record payment" action. No "make a credit sale" control — that's the POS terminal's job, same boundary this app draws everywhere else. Verified with a real Playwright test (`e2e/credit.spec.ts`) that seeds a credit sale via direct API call, edits credit settings through the UI as Branch Manager, confirms a POS User is denied that edit but can still view the card, and records a payment through the UI that correctly clears the invoice.

**Sub-area 2, GST Returns (GSTR-1/GSTR-3B export), is also built and verified live** (`internal/gst/`, no new migration — pure aggregation over data already posted correctly) — sequenced ahead of E-Invoicing/E-Way Bill and Bank Reconciliation because the FRD's own wording for this one ("export JSON for GST portal upload") needs no vendor integration and no stand-in, unlike those two:

- `GET /gst/gstr3b?period=YYYY-MM` — outward taxable value/tax and ITC available, populated only where this system has real, honestly-computable data (zero-rated/nil-rated/reverse-charge/non-GST all report zero rather than an invented figure — those need FRD items this phase hasn't built yet, Tax Exemptions and Reverse Charge Mechanism).
- `GET /gst/gstr1?period=YYYY-MM` — B2B (invoice-wise per customer GSTIN), B2CS (consolidated), and HSN summary.
- Both responses include a `reconciliation` block (this system's own computed tax checked against the GST Payable ledger — the FRD's "Reconciliation tool," in the one form buildable without reaching the actual GST portal) and a `disclaimer`: the exact GSTN JSON schema is reproduced from public documentation at a structural level, not verified against a live portal/GSP in this environment. See `phase0_1_design.md` §3.14 for the full named-gaps list (B2CL, doc_issue, UQC) and exact verified figures.

Verified live: GSTR-3B's outward tax reconciled against the ledger within rounding tolerance; GSTR-1 correctly grouped a real B2B customer's 8 invoices by GSTIN, consolidated everything else under B2CS, and produced a correct HSN summary; an invalid period correctly rejected. **One real bug found and fixed live**: a `DATE`-typed value scanned directly into a Go `string` without the `::text` cast this codebase's own convention requires — same class of bug (not this exact one) as the credit facility pass's `CHECK` constraint gap, another real finding from actually running the code against live Postgres rather than trusting it compiled.

**UI, built in this same pass:** `erp-web-admin`'s GST Returns screen (`/gst`) — a viewer, not a filer: GSTR-3B/GSTR-1 summaries, a reconciliation badge, GSTR-1's named gaps rendered as-is from the API, and a collapsible raw-JSON panel per return with copy-to-clipboard. Not permission-gated (matches the rest of `internal/accounting`'s reports), so no denial path. Verified with a real Playwright test (`e2e/gst.spec.ts`) against this repo's own real prior test data, confirming both returns render, the JSON panel expands with real content, and an empty period shows zero totals rather than an error.

**Sub-area 3, Bank Reconciliation, is also built and verified live** (`migrations/015_bank_reconciliation.sql`, `internal/accounting/bank_reconciliation.go`) — CSV import (no new dependency — Go's stdlib `encoding/csv`), auto-match by amount/date within a 3-day window (never guessing on an ambiguous multiple-candidate case), manual matching with a near-miss candidate picker, and reconciliation reports. Lives in the existing `internal/accounting` package, not a new one — this is squarely a Phase 2 Ledger & Accounting extension. See `phase0_1_design.md` §3.15 for the full endpoint reference and a genuinely non-obvious bug this pass found and fixed live: a `pgx` parameter used in two different expression shapes within one query (`$3 > 0` and `ABS($3)`) silently matched zero rows despite being provably correct clause-by-clause — fixed by casting explicitly (`$3::numeric`) rather than chasing the specific wrong type Postgres had inferred. A third class of live-verification catch this phase alone, after the `::text` scan gap (GST Returns) and the missing `CHECK` constraint value (Credit Facility) — each different, each only found by actually running the code against live Postgres.

**UI, built in this same pass:** `erp-web-admin`'s Bank Reconciliation screen (`/bank-reconciliation`) — the first file-upload UI in that app, the per-line match/unmatch workflow with a near-miss candidate picker, and the date-ranged reconciliation report. Verified with a real Playwright test (`e2e/bank-reconciliation.spec.ts`) that seeds a real ledger entry, uploads a real CSV from disk, confirms auto-match, walks the unmatch/manual-rematch flow through the candidate picker, checks the reconciliation report reflects it, and confirms a POS User is denied importing.

**Sub-area 4, Reconciliation & Audit, is in progress — Cash reconciliation (reconciliation type #1 of 4) is built and verified live** (`migrations/016_cash_reconciliation.sql`, `internal/accounting/cash_reconciliation.go`) — EOD denomination-wise counting against system cash sales, variance detection, and a gated adjustment (a Branch Manager/Merchant Admin PIN, checked inline the same way `ApplyDiscount` already authorizes a manual discount — not the router-level permission middleware, since the same endpoint stays open for the ordinary zero-variance case) posting to a new `Cash Over/Short` ledger account. Reconciliation type #4 (Inter-branch Transfer) was already closed in Phase 2; types #2 (Payment Gateway) and #3 (Inventory) are this sub-area's remaining items. See `phase0_1_design.md` §3.16 for the full endpoint reference and **two bugs found and fixed live this pass — one of them in shared accounting infrastructure other sub-areas already depend on**: the now-familiar `journal_entries.source_type` `CHECK` widening, and a genuinely new class of gap — `accounting.PostJournalEntry` had no way to post to a date other than today, invisible until a reconciliation for a _past_ day needed its variance entry dated correctly; fixed additively with `PostJournalEntryOnDate` rather than touching the original function's signature.

**UI, built in this same pass:** `erp-web-admin`'s Cash Reconciliation screen (`/cash-reconciliation`) — a denomination-grid count form with variance/approval-needed computed client-side as the cashier types (fetching that date's real cash sales live, the same figure the backend itself uses), so the reason/approver fields only appear once actually needed. No route-level permission gate to cover here, unlike this app's other gated screens — approval is an inline PIN check, so the test coverage is a wrong-PIN-rejected/right-PIN-accepted pair instead. Verified with a real Playwright test (`e2e/cash-reconciliation.spec.ts`) using far-past dates for fully deterministic math: a zero-variance count auto-closes with no approval box, a variance shows the box and rejects a wrong PIN, the correct PIN succeeds and posts the adjustment, and history reflects both.

**Payment Gateway reconciliation (reconciliation type #2 of 4) is also built and verified live** (`migrations/017_payment_gateway_reconciliation.sql`, `internal/accounting/payment_gateway_reconciliation.go`) — unlike Bank Reconciliation, which only _links_ an already-posted journal line, matching a settlement here posts a real not-yet-posted entry (`Dr Bank + Dr Payment Gateway Fees / Cr the clearing account`) since a card/UPI sale's collected amount sits in a clearing account until the gateway actually settles it, usually net of its own fee; unmatching posts the exact mirror image, never a delete. Matches by exact `payments.reference_no` against the settlement's own reference (a real UTR/transaction id), not an amount/date window. See `phase0_1_design.md` §3.17 for the full endpoint reference and **one real, necessary gap found and fixed in this same pass**: `POST /sales/orders/{id}/checkout` had no way to record a card/UPI payment's gateway reference at all — `payments.reference_no` had been silently `NULL` for every payment since Phase 1, which would have left this entire feature with nothing to match against. Fixed additively (`checkoutRequest.Payments[].Reference`, optional).

Verified live end to end: a real ₹12.39 card checkout with a gateway reference correctly persisted it; importing a settlement CSV for ₹12.09 against that reference auto-matched and posted exactly `Dr Bank 12.09 / Dr Payment Gateway Fees 0.30 / Cr Card Clearing 12.39`; the reconciliation report showed the correct matched total with empty unmatched/duplicate/missing lists; unmatching posted the exact mirror image and reset the line; the candidates endpoint then surfaced the freed payment for a manual re-match, which succeeded; a second settlement line importing the same already-matched reference was correctly marked `duplicate` rather than double-posted; an unsettled UPI sale correctly surfaced under the report's `missing_payments`; `payment_gateway_reconciliation.manage` confirmed denying a POS User with `403 FORBIDDEN` on import while the same user could still read the report.

**UI, built in this same pass:** `erp-web-admin`'s Payment Gateway Reconciliation screen (`/payment-gateway-reconciliation`) — gateway settlement CSV import, a per-line match/unmatch workflow with a candidate picker (same shape as Bank Reconciliation's screen), and the date-ranged report split into its three FRD-named buckets (unmatched, duplicate, missing payments). Verified with a real Playwright test (`e2e/payment-gateway-reconciliation.spec.ts`) that seeds a real card checkout via direct API call, uploads a real CSV from disk, confirms auto-match and the posted journal entry via the report, walks the unmatch/manual-rematch flow through the candidate picker, and confirms a POS User is denied importing but can still view the report.

**One real UI staleness bug found and fixed live via that e2e run**: this screen's Import and Report sections each fetch independently, so a match/unmatch made in one never told the other to refresh — a payment just matched in the Import section could still show as "missing" in the already-rendered Report section beneath it. Fixed with a shared refresh counter both sections now depend on. The identical bug was already sitting in the earlier Bank Reconciliation screen (same two-independent-sections shape; its own e2e test just never triggered it, since every step there reloads the page fresh between checks) — fixed there too in the same pass. See `phase0_1_design.md` §3.17 for the full account.

**Inventory reconciliation (reconciliation type #3 of 4, the last item in this sub-area) is also built and verified live** (`migrations/018_inventory_reconciliation.sql`, `internal/inventory/reconciliation.go`) — physical count vs system, cycle counting or full audit, following Cash Reconciliation's own already-shipped shape (one submission carries the whole count, a non-zero variance needs a Branch Manager/Merchant Admin PIN inline, no formal flag→investigate→resolve→approve→adjust state machine, same "skip the formal step" simplification this codebase has already applied elsewhere). Lives in `internal/inventory`, not `internal/accounting` — the ledger posting is a side effect of a `stock_levels` correction, the same relationship `AdjustStock` already has. See `phase0_1_design.md` §3.18 for the full endpoint reference.

Verified live end to end against the seeded merchant: a clean 60-unit count needed no approval; a shortage count was rejected with no reason, rejected again with a wrong PIN, then succeeded with the correct one, dropping `on_hand` to exactly 55 and posting `Dr Inventory Shrinkage 5,273.50 / Cr Inventory Asset 5,273.50` (5 units × the variant's ₹1,054.70 cost price, exact match); a follow-up overage count posted the exact mirror entry and restored stock to 60. **This sub-area's UI also found and fixed one real e2e-test bug, not a product bug**: the test's own "no approval needed" check ran before the counted-quantity field was filled in, so a freshly-added line's empty input (reading as 0) computed as a large spurious variance and correctly triggered the approval box the test wasn't yet expecting — reordered to fill first, check second. See `phase0_1_design.md` §3.18 for the full account.

**UI, built in this same pass:** `erp-web-admin`'s Inventory Reconciliation screen (`/inventory-reconciliation`) — a `GET /products`-sourced variant picker, live system-quantity lookup per added line, client-side variance computed as counts are typed, and a history table. Verified with a real Playwright test (`e2e/inventory-reconciliation.spec.ts`) that adds the seed variant, submits a zero-variance count, submits a shortage rejected by a wrong PIN and accepted by the correct one, and confirms each change against the variant's actual live system quantity (not just the result badge) before restoring stock to its original value.

**Phase 4's Sub-area 4, Reconciliation & Audit, is now fully complete across all three reconciliation types this environment can build without a vendor decision** — Cash, Payment Gateway, and Inventory, backend and UI both.

**Audit trail hardening (immutable/checksummed, 7-year retention) is also now closed** (`internal/audit/`, `migrations/021_audit_trail_hardening.sql`) — and turned out to be worse than "not built yet" on inspection: `audit_logs` had no RLS policy at all (the one tenant-scoped table in this schema missing it), and `erp_app` held full `UPDATE`/`DELETE` on it. Closed with RLS, a `REVOKE UPDATE, DELETE` making the database itself refuse a rewrite, and a `pgcrypto`-backed SHA-256 hash chain (`GET /audit-logs/verify` reports whether it holds). Retention is indefinite by design — no purge mechanism exists, which trivially satisfies "at least 7 years" without this pass making a unilateral call on when to delete compliance data. See `phase0_1_design.md` §3.20 for the full account, including three real bugs found live (a JSONB-round-trip mismatch that would have made every row "fail" verification, a repeated-parameter-cast bug matching a known class already on file from `bank_reconciliation.go`, and a `permission denied` finding that the app's own least-privilege role correctly can't create table partitions, fixed by moving that into the migration instead of an app-startup routine) and `erp-web-admin`'s new `/audit-logs` screen.

**E-Invoicing/E-Way Bill is also now closed** (2026-09-23, `internal/einvoice/`, `migrations/022_einvoice.sql`) — asked explicitly which GSP (GST Suvidha Provider) vendor to integrate, since real IRN/e-way-bill generation needs one and each has its own API/auth/onboarding, the answer was to build the vendor-agnostic side now and pick a vendor later. Delivered: a `GSPClient` interface every handler talks through, a `StubGSPClient` that fabricates real-shaped IRNs/QR codes/e-way-bill numbers with zero network calls, the real business rules (B2B-GSTIN precondition, the 24-hour NIC cancel window, a cancelled IRN can never be reused, the FRD's own interstate->₹50k e-way-bill trigger computed and persisted rather than left as a one-off), and `erp-web-admin`'s new `/e-invoicing` screen. E-way bill generation stays a deliberate manual action, not auto-triggered from checkout — it needs transporter/vehicle data the POS flow never collects, a real UX decision this pass didn't invent an answer for. See `phase0_1_design.md` §3.21 for the full account, including a real persistence bug this caught live before shipping (the interstate/required-by-rule flags were only correct in the initial response, not on a later `GET` or idempotent replay) and the e2e test that drives the real page against a real interstate B2B order. **Phase 4 is now fully complete** except formal PO approval workflow, which remains genuinely optional per this phase's own "if you have suppliers who need it" hedge — not a gap, a deliberate scope call already made when this phase started.

---

## Phase 5 — HR & Payroll

**Size: L | Goal: Staff costs run through the system instead of a spreadsheet.**

**In:**

- Employee records, attendance (POS clock-in/out), shift management
- Payroll run (earnings/deductions, statutory: PF/ESI/TDS/PT/LWF, challan generation)
- Commission engine (net-sales-after-returns model, category-weighted, tiered)

**Note:** This module is largely self-contained and can be deferred further or pulled earlier depending on whether you're hiring staff before or after other phases — it has the fewest dependencies on everything else.

**Definition of Done:** A real payroll run for your pilot store's staff, statutory deductions included, executes correctly.

**Status: complete** (2026-09-23, `internal/hr/`, `internal/payroll/`, `migrations/023_hr_payroll.sql`, `erp-web-admin`'s `/hr/employees`, `/hr/shifts`, `/hr/statutory-config`, `/payroll` screens).

**Employee records are not a new table.** `users` (migrations/001_schema.sql) already was this system's staff record — it has always had `employee_code`, name, phone, email, branch, and a PIN/password login — but there was no way to create one outside a migration. `POST /hr/employees` is the first user-creation endpoint this codebase has ever had, closing that gap as a byproduct of this phase rather than a separate audit finding. `GET /hr/roles` was added alongside it for the same reason (no `GET /roles` existed anywhere either) — and its query needed an explicit `merchant_id` filter matched against a real RLS policy, not skipped, since `roles` (like `users`) is protected by `001_schema.sql`'s dynamic per-table RLS loop, easy to miss with a plain `grep "CREATE POLICY"` since it's built via `format()` inside a `DO` block rather than a literal statement.

**Attendance** (`POST /hr/attendance/clock-in`/`clock-out`) is self-service by design — it always operates on the caller's own record (`claims.UserID`), never a body-supplied employee id, so one employee can never punch in for another. Idempotent (a repeat clock-in the same day leaves the original time untouched). `GET /hr/attendance` defaults to the caller's own record; viewing another employee's requires `hr.manage`, checked in-handler rather than at the router since the gate depends on which id was requested, not a fixed rule — the same shape `GET /payroll/employees/{id}/payslips` uses for self-vs-`payroll.manage`.

**Shift management** (`POST/GET /hr/shifts`) is a plain master list, assigned to an employee via `PATCH /hr/employees/{id}`'s `shift_id`.

**Salary structures are versioned, not overwritten in place** (`POST /hr/employees/{id}/salary-structure`, one row per `effective_from` date) — a raise never loses what an employee was actually paid before it, which a past payroll run's payslip still needs to reconcile against.

**Statutory configuration is merchant-editable, not hardcoded** (`GET/PATCH /hr/statutory-config`), seeded with reasonable current-as-of-2026 Karnataka defaults for the seed merchant — PF (12%/12%, ₹15,000 wage ceiling), ESI (0.75%/3.25%, ₹21,000 wage ceiling), a Professional Tax slab table (JSONB, editable brackets), and LWF, all real statutory concepts that change over time and vary by state, verify against the actual current EPFO/ESIC/state PT notification before relying on this for a real filing. **TDS is deliberately not a real income-tax computation** — no slabs, no old-vs-new-regime choice, no 80C declarations, that's an entire product category on its own. It's a flat, disabled-by-default (`0%`) percentage of gross pay, the same "don't build what you can configure or buy" boundary already drawn for e-invoicing's GSP integration — a real deployment needs a real TDS tool or a CA for that line.

**The commission engine** (`POST/GET/PATCH /payroll/commission-rules`, `POST /payroll/commission/compute`) computes "net sales after returns" honestly: `sales_orders.status = 'finalized'` only, so a voided or refunded order (status moves *off* `'finalized'` the moment either happens) is already excluded — never netted against as a separate subtraction step. Tiered and category-weighted: a rule with no `category_id` applies to a cashier's total net sales; one with a `category_id` applies only to that category's share, and the highest tier a period's net sales clears sets the rate for the *whole* period (a tier, not a marginal bracket, matching the FRD's own model). Recomputing a period overwrites rather than double-counts.

**The payroll run** (`POST /payroll/runs`, `POST /payroll/runs/{id}/finalize`, `GET /payroll/runs`/`{id}`) computes every eligible employee's payslip for a calendar month: an employee with no salary structure on file, or not employed at any point during the period, is skipped and named in `skipped_employees` rather than silently omitted or defaulted to zero. Attendance-based proration only docks pay for an explicit `absent`/`half_day` record — a day with no attendance row at all (a weekly off, a holiday, or an employee this deployment never clock-tracks) is paid, not treated as an unmarked absence; an employee with zero attendance rows for the whole period is paid in full. PF is calculated on Basic only (capped at the wage ceiling), matching the real rule that HRA/allowances aren't part of the PF wage base; ESI is calculated on total fixed gross and only applies when that gross is at or under the ESI ceiling for the period. A finalized run can never be silently recomputed — a second `POST /payroll/runs` for the same period returns `409 PAYROLL_RUN_FINALIZED`.

**Challan generation is scoped honestly**: `GET /payroll/runs/{id}/challan` computes the correct total payable per statutory head (PF, ESI, PT, TDS, LWF — employee + employer combined for PF/ESI/LWF) from a run's own payslips — the number that actually goes on a real EPFO/ESIC/PT challan — but does not produce or submit an actual government e-challan file, the same "export the number, don't build the portal integration" boundary `internal/gst`'s GSTR-1/3B export already draws.

Verified live end to end against the real backend: created an employee with a real PIN and confirmed they could immediately PIN-log-in and use it; clocked in/out (idempotent repeat clock-in, `409 ALREADY_CLOCKED_OUT` on a repeat clock-out); a POS User was correctly denied viewing another employee's attendance; a payroll run for a real salary structure (₹20,000 basic/₹8,000 HRA/₹2,000 special) computed exactly ₹1,800 PF (capped at the ₹15,000 ceiling, not the full ₹20,000 basic), ₹0 ESI (₹30,000 gross exceeds the ₹21,000 ceiling), ₹200 PT (the seeded slab), and ₹28,000 net pay; finalizing and re-running the same period correctly returned `409 PAYROLL_RUN_FINALIZED`; the commission engine correctly aggregated real historical finalized-order net sales per cashier and applied the tiered rate; the challan summary correctly summed PF/ESI employee+employer contributions across a run's payslips; a POS User was denied both `/hr/employees` and `/payroll` with the expected permission-code message. `erp-web-admin`'s new screens were driven end to end by a real Playwright test (`e2e/hr-payroll.spec.ts`) — creating an employee, assigning a salary structure, running and finalizing payroll, and viewing the challan summary, all against the live backend, plus the same POS-User-denied check in the browser.

**One real test-design bug found live while writing that e2e spec, not a product bug**: the spec's first version hardcoded a test period, which broke the moment the spec ran a second time — a payroll run is real, persistent, and permanent once finalized, so the second run's `POST /payroll/runs` correctly returned `409 PAYROLL_RUN_FINALIZED`, and the UI (correctly) never navigated to show payslips for a run it couldn't create, failing the test's own assertion. Fixed by deriving a pseudo-random period far outside any real business's date range from the test's own unique timestamp, so every run gets a period no previous or future run will ever collide with.

**Not modeled, deliberately, matching this phase's own scoping and the wider codebase's "don't build what you can configure or buy" posture:** a real income-tax (TDS) computation with slabs/regime/declarations, an actual government e-challan file/submission, a weekly-off/holiday calendar (attendance proration treats an unmarked day as paid rather than guessing), and separate paid/unpaid leave types (all `leave` status attendance counts as paid, matching the "leave" name's default assumption in an SMB retail context).

**Follow-up audit (2026-09-24): re-verified against `pos_frd_complete.md`'s full §7 text, not just this doc's own condensed bullets — found and closed three real gaps, confirmed the rest as legitimate, already-flagged scope boundaries, and along the way uncovered and fixed a real dev-environment data-loss trap unrelated to Phase 5 itself.**

Closed:
1. **`employment_type`'s CHECK constraint only allowed `full_time`/`part_time`/`contract`** — the FRD's own "Employee Types" line names five: "...Commission-based, Daily Wage (each with different payroll rules)". Widened (`migrations/024_hr_followup_fixes.sql`) to accept all five. Payroll computation itself still treats every type uniformly through the same prorated-salary-structure model — a true per-diem daily-wage rate is a larger feature this pass doesn't build, and the fix here is honestly scoped to "can now correctly classify and report," not "payroll math already branches on it."
2. **Late/early attendance tracking**, named directly in the FRD's Attendance bullet, was entirely missing. Added `late_minutes`/`early_departure_minutes` to every attendance read path, computed on read (never stored) against the employee's *currently* assigned shift — so reassigning someone to a different shift later never retroactively changes what an old attendance row reports as late. Verified live: a shift with a 01:00 start / 23:55 end correctly flagged a same-day clock-in as ~1134 minutes late and the clock-out as ~241 minutes early.
3. **The challan summary reported one combined "employer PF" figure**, but a real EPFO challan needs the employer's 12% split into its two statutory sub-allocations — 8.33% to EPS, 3.67% to EPF. Added `pf_employer_eps`/`pf_employer_epf` to the challan response, verified live (₹1,800 employer PF → exactly ₹1,249.50 EPS + ₹550.50 EPF, summing back to ₹1,800.00).

Documented, not code-changed (real footguns worth naming rather than silently leaving for someone to discover the hard way):
4. **LWF is added to *every* payroll run that reads a nonzero configured amount**, but the FRD (and Karnataka's actual rule) says LWF is due annually/half-yearly, not monthly — a merchant who sets a real LWF amount and runs payroll monthly would overpay 6-12x. Clarified in both the Go struct doc comment and the `/hr/statutory-config` UI: leave both LWF fields at 0 except in the specific run they're actually due, set the real amount right before that one run, then reset to 0.
5. **Commission "Payment: After return period (15-30 days)"** — `POST /payroll/commission/compute` has no enforcement or warning against being called too early in a period; doing so can pay commission on a sale that's later returned or voided. Documented in the handler's own doc comment and added an inline hint in the `/payroll` UI next to the compute button.

Confirmed as legitimate, already-correctly-scoped-out (real FRD-named features, but larger than a verification pass, and not restated in this doc's own condensed Phase 5 bullets): a Loans/Advances payroll deduction ledger, multi-frequency payroll (the FRD wants Monthly/Bi-weekly/Weekly/Daily configurable per employee; this build is Monthly-only), true overtime pay calculation (distinct from the late/early *tracking* just added), a commission "Target Multiplier" (achieve target → 1.5x), and a leave-management workflow (types, approval, balance). Employee/payroll *reports* and *notifications* named in the FRD's §14/§17 belong to the Reports and Notifications tracks, not this phase, and were correctly not built here.

**A real dev-environment finding, unrelated to Phase 5's own code**: this pass discovered live that `docker-compose.yml`'s comment claiming the migrations volume mount "auto-runs 001_schema.sql + 002_seed.sql on first boot" was wrong — Postgres's `docker-entrypoint-initdb.d` mechanism runs *every* `.sql` file there in filename order on a genuinely fresh volume, not just the first two. The `pgdata` volume was reset at some point outside this session's control between the previous turn and this one, and the next `docker compose up` silently replayed all 24 migrations — correctly restoring schema and every migration's own seed/backfill data, but not runtime data created via the API afterward (real payroll runs, e-invoices, extra employees, the manually-set merchant GSTIN, and the dev-only passwords/PINs set via `/dev/set-password` for accounts whose seeded hash is a placeholder). Comment fixed to describe the real behavior and to name the diagnostic (`docker inspect erp-core-go-postgres-1 --format '{{.State.StartedAt}}'`) for next time this happens. All affected dev credentials/config were restored and every Playwright spec (20/20, including two pre-existing Phase 4 specs with a strict-mode locator collision this exposed and fixed) re-verified green afterward. **This same volume reset recurred a second time** in the very next turn (UACL, below) — confirmed it's a real, recurring characteristic of this dev environment (most likely the machine sleeping/Docker Desktop or WSL2 restarting between sessions), not a one-off. Treat it as expected: at the start of any new session, check `docker inspect erp-core-go-postgres-1 --format '{{.State.StartedAt}}'` against how long ago you'd expect the volume to have persisted before trusting any previously-created runtime data (dev passwords/PINs, the merchant GSTIN, payroll runs, e-invoices) is still there.

---

## Cross-cutting: UACL — role/permission administration (closed 2026-09-24, before Phase 6)

Not tied to any one phase — `roles`/`permissions`/`role_permissions`/`user_roles` have existed since Phase 0 and have been the real enforcement mechanism behind every permission check in this codebase, but nothing before this could manage that mechanism: every grant had been seeded directly by a migration file, and there was no way for a merchant to create a custom role (the FRD's own named "Accountant"/"HR Manager"/"Sales Manager" — `pos_frd_complete.md` §12) or give a user a second role without a developer editing SQL.

Closed via `internal/rbac` (`migrations/025_rbac.sql` adds exactly one new permission, `rbac.manage` — no schema change, since the tables already existed): full role CRUD, a permission catalog listing, a full-replace role-permission editor, and real multi-role assignment/revocation (`POST`/`DELETE /rbac/users/{id}/roles`) distinct from `PATCH /hr/employees/{id}`'s single-role onboarding convenience. Gated Merchant-Admin-only, the one surface in the system that stays top-tier even where most operational permissions extend to Branch Manager too.

Two deliberate lockout guards, both live-verified: `DELETE /rbac/roles/{id}` refuses (`409 ROLE_IN_USE`) while any user holds the role, rather than silently cascading the delete into `user_roles` per the schema's own `ON DELETE CASCADE`; and both the permission-replace and role-revoke endpoints refuse (`409 LAST_RBAC_HOLDER`) a change that would leave no one in the merchant able to reach `rbac.manage` at all — the one lockout this system can't recover from through its own API.

`erp-web-admin`'s new `/rbac` screen (role list, a permission checklist per role, and a user↔role assignment panel) was driven end to end by a real Playwright test (`e2e/rbac.spec.ts`): created a custom role, assigned it real permissions, granted it to an existing employee *alongside* their existing role, confirmed via a direct API call that the employee's access genuinely changed (not just a UI checkbox), revoked it, confirmed access reverted, deleted the now-unused role, and confirmed a POS User is denied the whole screen. See `phase0_1_design.md` §3.1a for the full endpoint contract and live-verification account, including a real, unrelated pre-existing test bug this pass exposed and fixed (a bare text search in `promotions.spec.ts` started colliding with the new "Roles & Permissions" sidebar link).

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

**Status: complete except OCR (2026-09-25)** — items 1, 2, 3, 4, 5, and 7 are done (`internal/ai`, `internal/ai/llm`; no new migration for 1/2/4/5 — everything reads `stock_levels`/`sales_orders`/`sales_order_lines`/`sales_order_discounts`, already there since Phase 1). Item 6 (OCR) remains deliberately deferred per the user's own choice.

**Reorder suggestions** (`GET /ai/reorder-suggestions`) compute sales velocity (units/day over a trailing window, default 30 days, finalized orders only) against a *caller-supplied* lead time — this codebase has no per-product/per-supplier lead-time field anywhere (Purchase Management never linked a product to a preferred supplier), so rather than invent a number, the endpoint takes one as a parameter. Surfaces only variants genuinely at risk (below reorder point, or projected to run out before the lead time elapses), with a suggested quantity covering lead-time demand plus a return to the reorder point.

**The recommendation engine** (`GET /ai/recommendations/{variant_id}`) is a classic market-basket "customers who bought this also bought" — for every finalized order containing the source variant, counts how often each other variant co-occurred, ranked by count, with a `P(B|A)` confidence score. Open to any authenticated user (including a POS terminal at checkout), same as reorder suggestions — neither is permission-gated, both are informational reads.

**Two real bugs found and fixed live**, not just in review: a brand-new product with zero stock and an unconfigured (default-0) reorder point was flagged as "below reorder point" at *every* branch, since `0 <= 0` is trivially true — fixed by requiring `reorder_point > 0` first, matching the exact convention `internal/inventory`'s existing low-stock sweeper already established for this reason. And the e2e test's own seed data under-stocked a variant, silently starving a planned third order and skewing the expected confidence score from 2/3 to 2/2 — a test bug, not a product bug, fixed and documented in the spec itself as a reminder.

`erp-web-admin`'s new `/ai-insights` screen (a reorder-suggestions table with branch/lead-time/window controls, and a frequently-bought-together product picker) was driven end to end by a real Playwright test (`e2e/ai-insights.spec.ts`) against the live backend, including both bugs above being caught and fixed before shipping. See `phase0_1_design.md` §3.23 for the full endpoint contract and live-verification account.

**Demand forecasting** (`GET /ai/demand-forecast/{variant_id}`) fits an ordinary-least-squares trend line over a variant's own zero-filled daily sales history and projects it forward, with an honest low/moderate/adequate confidence heuristic based on how many historical days actually had a sale (never "high" — this is a trend line, not a real prediction interval). **Fraud/anomaly detection** (`GET /ai/anomalies`) runs two z-score outlier checks — unusually large manual discounts, and cashiers with an outlier void rate — against this merchant's own population in the window, gated behind the existing `audit.view` permission since it's the only one of the four exposing per-employee behavior data. Both are backend-only so far: no admin UI or e2e test yet, live-verified instead via direct API calls against real seeded data, including hand-derived z-score math matching the API's output exactly and a live check that the `audit.view` permission gate actually blocks a POS User while leaving the other three endpoints open. Full verification narrative in `phase0_1_design.md` §3.23.

**NLP-BI and the AI Copilot are also now done**, closing every Phase 6 "In" item except OCR (still deliberately held off per the user's own choice). Both go through `internal/ai/llm.Client`, one interface with two real backends — `AnthropicClient` (the real Messages API) and `OllamaClient` (self-hosted, see `docker-compose.yml`'s new `ollama` service) — selected per-deployment via `LLM_PROVIDER`, after the user asked for Ollama to be supported alongside the originally-chosen Anthropic rather than committing to one vendor. **NLP-BI** (`POST /ai/ask`) classifies a question into one of five fixed report intents and calls the same `reports.Build*` functions `GET /reports/*` already uses — the LLM never generates SQL and never sees a raw transaction row. **The AI Copilot** (`POST /ai/copilot/chat`) is strictly advisory for v1 as agreed: no tool-calling, no mutation capability, grounded in a small real snapshot (today's sales, low-stock count) rebuilt fresh every request.

**A real, live-caught bug worth flagging:** the first live test against a real Ollama-hosted `llama3.2:3b` model showed it reliably resolving a branch name to the *wrong* branch's id/index — not a formatting bug, a genuine small-model list-position limitation (verified by testing the raw model directly: it correctly matched 2 of 3 branches by name but consistently mis-indexed the 3rd). Fixed by resolving the branch deterministically in Go — a plain case-insensitive match of the merchant's own branch names against the question text — and only falling back to the model's guess when no name is literally present. Re-verified live against all three of a real tenant's branches after the fix: 100% correct. Full account, including every other live-verification step (unsupported-question rejection, snapshot-accuracy check against the database, multi-turn context, both `LLM_UNAVAILABLE`/`LLM_ERROR` failure paths), in `phase0_1_design.md` §3.24.

**`erp-web-admin`'s new `/ai-assistant` screen** (Ask AI + Copilot chat panels, a dependency-free CSS bar chart) has no e2e test yet — a real Playwright spec is a follow-up. One disclosed limitation: the Copilot's *numeric* advice is grounded and accurate (verified against the database), but it has invented plausible-sounding, nonexistent navigation labels when asked where to go in the app — it was never given this app's real sidebar structure, so UI-location claims specifically shouldn't be trusted without checking.

---

## Phase 7 — Wholesale/B2B & Omnichannel

**Size: L**

**In:** formal B2B quotations, wholesale price lists at scale, e-commerce/omnichannel storefront with order sync back into the same inventory.

**Definition of Done:** The same catalog and stock serve both a walk-in retail customer and a B2B wholesale order without double-entry.

**Status: Definition of Done met (2026-09-25).** Scope confirmed with the user first (same "don't unilaterally decide a big call" posture as the e-invoicing GSP and Phase 6 LLM-vendor choices): a real customer-facing storefront is a separate application entirely, explicitly not built this pass. What's built is formal B2B quotations (`internal/quotations`) and wholesale price lists at scale (`internal/pricing`'s new price-list endpoints), plus the one thing that would otherwise block a storefront later — a channel-agnostic backend (`pos_terminals`/`sales_orders` both gained a `channel` column, and every branch now has a virtual "online" terminal to create orders against).

The Definition of Done is true by construction, not just asserted: `ConvertQuotation` opens a cart and adds lines through `internal/sales`' own `CreateCart`/`AddLineToCart` functions — extracted from the existing `CreateOrder`/`AddLine` handlers specifically so a converted B2B quote is indistinguishable from a walk-in POS sale to every downstream system (stock reservation, checkout, accounting, credit limits). Wholesale pricing resolves through one function (`pricing.ResolvePrice`) called from both the POS cart and quotation creation, so it's never computed two different ways.

**Two real bugs found and fixed live:** a converted quotation initially priced its line at retail instead of wholesale, because the new cart never inherited the quote's customer — fixed by threading `customerID` through `CreateCart` itself, re-verified live end to end through a real `send → accept → convert → checkout` flow to a `finalized` sale at the correct wholesale price. Separately, the browser couldn't call the new price-list-item endpoint at all — `internal/httpserver/cors.go` had never allowed `PUT` (nothing had needed it before), caught via a real Playwright browser test, not curl, and fixed by adding it to the allowed-methods list. Full verification narrative, including the transaction-atomicity check on a failed conversion and the existing 10-spec regression suite re-passing afterward, in `phase0_1_design.md` §3.25.

**First completeness audit, same day:** closed two real gaps — `sales_orders.channel` was written but never returned by any order endpoint (fixed across `CreateCart`/`loadOrder`/`recalcOrderTotals`), and `POST /customers` couldn't set `price_list_id` at creation (only `PATCH` could). Also added the permanent Playwright specs (`e2e/price-lists.spec.ts`, `e2e/quotations.spec.ts`) that were previously just a throwaway smoke pass — both drove the full lifecycle through the real browser UI.

**Second completeness audit, next day** (an explicit "make sure every deliverable is done" re-check): found two more gaps of the same shape — a backend capability existed but nothing could reach part of it. `DeleteQuotation` didn't exist at all (a mistaken draft quote had no way to be removed for up to 30 days); added `DELETE /quotations/{id}`, restricted to `draft`. `DELETE /pricing/price-lists/{id}` existed since the first pass but the admin UI never exposed a button for it; added one, surfacing the existing `409 PRICE_LIST_IN_USE` refusal live in the browser. Both new e2e specs extended to cover this (including two real bugs in the test code itself, caught and fixed in the same pass). Full suite: 12/12.

**Deliberately deferred, named rather than silently dropped** (matching `migrations/009_pricing.sql`'s own header from two phases ago): scheduled future-dated price changes, dynamic/time-based pricing, and editing a draft quotation's lines after creation — real, separate builds, none needed for this phase's actual Definition of Done.

---

## Phase 8 — Vertical Expansion

**Size: ongoing, per new industry**

Because the masters were built generic from Phase 1 onward, each new vertical is mostly **configuration**, not new code:

- **Grocery/FMCG:** activate batch/lot + expiry tracking, weighing-scale barcode integration, switch valuation to FIFO for perishables
- **Pharmacy:** activate prescription linkage, stricter expiry compliance, regulatory reporting
- **Apparel:** you're already close from Phase 1's variant matrix — mainly needs seasonal/collection catalog features
- **Jewelry, Steel, Chemicals, Electronics:** attribute-set configuration (purity/weight for jewelry, technical specs for electronics, etc.) — validates the "configurable masters, not industry-specific code" promise in your BRS

**Status: attribute-set configuration done (2026-09-26)** — the user picked this group explicitly (plus configuring Sports, this merchant's own real vertical, rather than only a synthetic example) instead of a shallow pass across all five. Closes an existing gap, not new schema: `attributes`/`attribute_values` existed since Phase 1 specifically for this, but no Go code ever managed them, and `attribute_combo` accepted any key/value with zero validation the entire time. Added the one piece missing — `category_attributes` (which attributes apply to a category, required or not, with a display unit) — plus `internal/catalog.validateAttributeCombo`, called from `CreateProduct`, permissive by default (a category with no declared set behaves exactly like before). New `/attributes` admin screen; New Product now renders real fields (a dropdown for a `select`-type attribute, not free text) once a category with a declared set is picked.

Live-verified across three genuinely independent verticals, not one hardcoded case: **Jewelry** (Purity/Weight, both required — missing/invalid values correctly rejected, valid combo succeeds), **Electronics** (RAM, required — independently proven working), **Sports** (Material, deliberately left *optional* on the real, pre-existing Cricket category from this merchant's own seed data — both with and without a value succeed, proving the required/optional distinction genuinely works both ways). One real bug, self-inflicted by the live verification itself: throwaway test products (created without `cost_price`, so it defaulted to 0) sorted alphabetically ahead of the real catalog and broke `pricing.spec.ts`'s "edit the first row" assumption — root-caused via direct DB inspection and fixed by deleting them (which surfaced a genuine `psql -c` gotcha: multiple semicolon-separated statements in one invocation run as an all-or-nothing implicit transaction, so an earlier "successful" cleanup had silently rolled back). Full regression suite: 13/13. Full account, including the exact `curl` verification sequence, in `phase0_1_design.md` §3.26.

**Status: all five verticals done (2026-09-26)** — the user asked to proceed with the remaining three (Grocery/FMCG, Pharmacy, Apparel) immediately afterward. Built in dependency order: Apparel first (a `collections` table — the one piece missing once Phase 1's variant matrix and this phase's own attribute-set mechanism already covered Size/Color), then Grocery/FMCG (batch/lot + expiry tracking, FIFO consumption, weighing-scale barcodes — the infrastructure Pharmacy needs), then Pharmacy (prescription linkage + a genuinely generic "stricter shelf-life" policy knob any category can use, not a pharmacy-specific branch anywhere in the sales/inventory code).

Grocery/FMCG's design honors the same constraint the roadmap already calls out elsewhere: `stock_levels`' optimistic-locked `version` mechanism (`internal/sales/reservation.go`) stays completely unchanged — batch tracking (`stock_batches`, `product_variants.track_batch`) is a purely additive layer on top, active only for a variant that opts in. Live-verified: a real milk variant received in two batches with different expiry dates via GRN correctly sold FIFO (oldest-expiry-first), correctly refused a sale from an already-expired batch while still selling from a valid one, and correctly restored batch quantity both when a line is deleted and when the 15-minute reservation-expiry sweeper reclaims an abandoned cart (**a real bug found live**: the sweeper originally restored `stock_levels` but not the batch, a genuine stock leak, fixed and re-verified). Weighing-scale barcode decoding (13-digit EAN-13-shaped code, PLU + weight-in-grams) also had **a real bug**: its prefix collided with this codebase's own auto-generated catalog barcodes (both sit in GS1's reserved 20-29 range), misreading an ordinary product barcode as a weight-embedded one — found via a real Playwright test driving the actual GRN screen, fixed by narrowing to a distinct sub-prefix.

Pharmacy reuses the attribute-set mechanism for drug scheduling (a "Drug Schedule" attribute, no new schema) and adds a plain `categories.min_shelf_life_days` any vertical could use — Pharmacy just sets it to 30 days for scheduled drugs, correctly refusing a batch Grocery's own default would have happily sold. Prescription-required checkout enforcement, and an honestly-scoped internal schedule-drug-sales report (explicitly not a government e-filing integration), both live-verified end to end including the OTC exemption path and the `audit.view` permission gate.

**A third real bug** surfaced in test-authoring itself: accumulated e2e test-run debris grew the catalog past the Pricing screen's default page size, intermittently hiding brand-new test products — fixed durably (the screen now requests the backend's own max page size) rather than just cleaned up once. Full regression suite: 16/16. Full account for all three verticals in `phase0_1_design.md` §3.27.

**Completion audit (2026-10-04) — Phase 8 closed.** A "make sure Phase 8 is really done" pass enumerated every write to `stock_levels.on_hand` and found the disclosed Grocery/FMCG limit (adjustments, reconciliation, purchase returns not batch-aware) also covered branch transfers, sale voids and offline-synced sales — plus one real compliance bug: raising a cart line's quantity (`PATCH .../lines/{line_id}`) skipped batch allocation entirely, so expired or short-shelf-life stock could be sold by adding 1 unit and then increasing the quantity. All closed (`migrations/034`, a `stock_batch_movements` ledger; transfers now carry batch/expiry/cost to the destination branch), with batch fields on the three affected web-admin screens, and live-verified with 37 checks including `sum(batches) == on_hand − reserved` after every step. Full account in `phase0_1_design.md` §3.27a. What remains named-but-unbuilt is the same deliberate list as before: government e-filing for scheduled drugs, a configurable weighing-scale barcode format, and apparel markdown automation.

---

## Phase 9 — POS Hold & Recall, Barcode Manual-Entry/Generate Visibility

**Size: M — closed 2026-09-27**

Two user-requested additions to the core POS loop, not part of the original five-phase plan above:

- **Hold & Recall**: a cashier can park an in-progress cart to serve someone else, and recall it later, without losing its lines or (within a configurable window) its stock hold. New `sales_orders.status = 'held'`, deliberately separate from `'cart'` so every existing cart-editing endpoint refuses a held order for free. Extends reservations to `HOLD_DURATION_MINUTES` (default 120) on hold rather than reusing the FRD's 15-minute abandoned-cart window — a hold is an explicit action, abandonment is not. Recall re-validates every line and re-reserves any whose hold didn't survive, surfacing (never silently dropping) any line that genuinely can't get its stock back.
- **A real correctness gap closed along the way, not introduced by this feature**: `Checkout` was found to only ever consume `active` reservations without checking that every line still had one — harmless as a narrow pre-existing race, but Hold/Recall would have made "a cart open far longer than 15 minutes" routine, turning that race into a real, everyday way to finalize a sale without decrementing stock. Closed with a cheap aggregate check (`409 RESERVATION_MISMATCH`) rather than a broader redesign of the (already-disclosed) reservation-to-line matching limitation.
- **Barcode visibility**: `POST /products/variants/{id}/barcodes` already supported manual entry and auto-generate from an earlier phase — the real gap was that the product list (the Pricing page's product table, the only one that exists) never showed or offered either. Closed by joining each variant's primary barcode into `GET /products`.
- **Scope**: backend (`erp-core-go`) + the Flutter POS app (`erp-pos-flutter`) for Hold/Recall's actual UI, since that's a counter action with no web-admin equivalent screen; barcode visibility landed in the web-admin Pricing page, the real "product list."

Live-verified end to end via direct API/DB calls: hold extends reservation expiry correctly; double-hold, checkout-while-held, and double-recall all refuse with the right codes; a forced-lapsed reservation correctly trips `RESERVATION_MISMATCH` at checkout; recalling a cart whose hold outlived its window correctly re-reserves and checks out cleanly afterward. Full account in `phase0_1_design.md` §3.28.

---

## Cross-cutting: Session Expiry Handling & GST-Inclusive Pricing

**Size: M — closed 2026-10-02**

Two items the user asked to be made sure of before further phase work:

- **Session expiry → login page**, in both the web admin and the Flutter POS. Silent refresh while the user is active; back to the login page (with a reason) after a full role-tier inactivity window (POS 15 / Manager 30 / Admin 60 min) or when the refresh token is rejected. Offline POS sessions are never ended for lack of connectivity.
- **GST-inclusive vs GST-exclusive pricing**: per-product flag with a merchant-wide default for new products; line-level tax extraction so an inclusive bill always equals the shelf price exactly; margins on the pre-tax price; GST/e-invoice reports read a generated `taxable_value`. Also fixed a real bug: changing a cart line's quantity silently re-priced a wholesale line at retail.

Full design and verification in `phase0_1_design.md` §3.29.

---

## Phase 10 — Pricing & Quotation Gaps

**Size: M — closed 2026-10-04**

Chosen by the user as the next phase once 0–9 were closed: the three items earlier phases named as deferred (Phase 2's `migrations/009_pricing.sql`, Phase 7's own "Deliberately deferred" line), all `pos_frd_complete.md` §11.

- **Scheduled price changes** — a cost/MRP/selling-price change with a future effective time, applied within a minute by a background sweeper through exactly the same rules as an immediate edit (below-cost guard, price history, search reindex). A change that would sell below cost *on the day* ends `failed` with the reason, never skipped silently. Bulk price updates can be scheduled too.
- **Dynamic pricing** — price rules for happy hours, weekend specials and seasons: a fixed price or ± percentage, per product or category, optionally per branch, evaluated in the branch's own timezone, capped at MRP. A customer's wholesale price list always wins; quotations and offline POS sales don't use rules (both disclosed). Promotions stay as they were — discounts on top.
- **Editing a draft quotation** — add/change/remove lines (including a negotiated price), validity and notes, while still a draft.
- **A real pre-existing bug closed**: converting a quotation re-priced every line at conversion time, silently dropping any negotiated quote price and billing price changes that landed after the customer accepted. Conversion now bills exactly what was quoted.

**Definition of Done:** a manager can schedule a price change and see it apply; set up a happy hour that the POS actually charges; and fix a draft quote without re-entering it — with the converted order billing the agreed numbers. Met: 50 live API/DB checks, unit tests for the pricing arithmetic, and a Playwright spec driving all three screens (4/4 with the related existing specs). Full account in `phase0_1_design.md` §3.30.

**Flagged, not built:** promotions evaluate their own time windows on the database clock (UTC on the stock Docker Postgres image), unlike price rules; and the offline POS can't apply rules or price lists until the Flutter app pulls and evaluates them.

---

## Cross-cutting: Product Editing & Status Enforcement

**Size: M — closed 2026-10-04**

User-requested audit ("make sure product edit is implemented"): it largely wasn't. Products could be created but not maintained — no way to load one for editing, fix a variant's SKU or attributes, add a size later, or clear a category — and there was no edit screen at all. Discontinuing a product also did nothing: the till kept scanning and selling it, while the product list hid it so it could never be reactivated. Now: full product + variant editing and add-variant (prices still go through the pricing endpoint's guard and history), tenant-checked links, attribute re-validation on category change, audit trail and search reindex on every edit, and one shared "sellable" rule enforced at barcode scan, cart, and quotations. New Products and Product edit screens in the web admin. Full account in `phase0_1_design.md` §3.31.

---

## Phases 11–16 — UI/UX Modernization & SaaS Platform Completion (planned 2026-10-10)

**Status: planned, not started.** Added at the user's request after a UI/UX review of both clients against a new consolidated requirements brief (POS + Web Admin + multi-tenant SaaS). This block is **additive only**: it doesn't reopen, re-scope or re-sequence Phases 0–10, and it doesn't change any decision recorded above. That includes the non-negotiables in `../CLAUDE.md` (custom auth, shared-DB RLS multi-tenancy, `NUMERIC(14,2)` money, optimistic-locked reservations, idempotency keys on retryable writes).

**Design source.** The UI/UX design (tokens, component kit, sitemap for both apps, high-fidelity screens for POS billing/payment/open-price/approval/sales history/phone-offline and admin list/edit/labels/journal/subscription, the states checklist, breakpoints and shortcuts) is published as the "ADR ERP Design System" page (https://claude.ai/artifact/352v1TM8GCN7d5F4nh9p84; private to its owner until shared). It carries out `tech_stack_decision.md` §4's existing "Shared design system" row: one token set, shadcn/ui on the web, and a Flutter theme mirrored from the same tokens. It isn't a new stack decision. Everything a phase below depends on from that design is restated here, so this roadmap stays self-contained if the link is unavailable.

### Why these phases are sequenced this way

1. **Restyle before you rebuild.** Phase 11 changes no logic, so the existing 20+ Playwright specs and the Flutter test suite prove it broke nothing. Every later phase then builds on one component kit instead of restyling 50 screens twice.
2. **POS before admin.** `pos_frd_complete.md` Part A's ≤6-second scan-to-receipt target and the FRD's "offline-first POS" objective are the product's core promise. Phase 1's own note says it is "the only phase that really matters for validating the business."
3. **Backend gaps travel with the screen that needs them**, the same "UI built in this same pass" pattern every phase since Phase 2 has followed. No endpoint is built without a caller, and no screen is built against an endpoint that doesn't exist.
4. **SaaS subscriptions last.** They're the only item here with no existing schema at all (FRD §19 was never started). Plan limits also need the terminal, user and branch management surfaces from Phases 11–14 to exist before there's anything to limit.

### Requirement traceability: new brief → existing documents

Brief section numbers refer to the 2026-10-10 consolidated requirements brief. "Existing" means already built and verified per the phase status blocks above.

| Brief § | Requirement | Existing source of truth | Already built | Gap closed in |
| --- | --- | --- | --- | --- |
| 1 | Modern, consistent UI across POS + admin; responsive; accessible; loading/empty/error/confirm/unsaved states | `tech_stack_decision.md` §4 (shared design system); FRD Part A performance targets | Functional screens only; framework-default styling; no confirm or unsaved-change patterns | 11, 12 |
| 2 | Master CRUD with activate/deactivate, dependency-safe delete, server-side paging/search/sort/filter, record counts | FRD §10 (catalog, lifecycle, bulk ops), §9 supplier mgmt, §1 hierarchy; `phase0_1_design.md` §3.31 (one shared "sellable" rule) | Most masters have create/list; paging only on some endpoints (products, customers, audit, notifications, search); no category/brand/unit list endpoints (known gap in `erp-web-admin/CLAUDE.md`) | 11 |
| 3 | Product lookup/update in POS, permission-gated price/stock edits, sync to terminals | FRD §2 stock adjustments, §11 pricing, §13 search; Phase 2 Pricing (`pricing.manage`), Phase 1 adjustments (`inventory.adjust`); `/sync/pull` (§3.5) | Backend endpoints and permissions exist; POS has search + scan only | 13 |
| 4 | Barcode labels: EAN-13/UPC-A/Code 128/QR, templates, sizes, preview, bulk, printer paths | FRD §12 (types, 4 predefined templates, WYSIWYG editor, Zebra/TSC/Brother/Epson/DYMO, ESC/POS/ZPL/TSPL, bulk + GRN printing) | `internal/printing` ESC/POS, 2 of 4 templates (Standard, Compact), EAN-13 + Code 128; **no client calls the label endpoint** | 13 (POS), 14 (admin studio) |
| 5 | Open-price items with min/max, supervisor approval, audit | Not in the FRD. Closest: FRD §3 "Bill Modification" (manager approval + reason + audit) and §5 "Manual Discount Authorization" tiers | Not modelled | 13 |
| 6 | Sales creation + history + returns/refunds in admin, same rules as POS | FRD §3 (payments, return & exchange, bill modification), §15 A (sales reports); Phase 7's `CreateCart`/`AddLineToCart` extraction (one checkout path) | No admin cart; no order list endpoint (only `GET /sales/orders/held`); void exists, returns/refunds don't | 14 |
| 7 | POS sales history, filters, summaries, reprint, returns, cashier-wise, offline-aware | FRD §15 A (cashier-wise, payment-method-wise), §16 (cash recon); Phase 1 basic reports | `GET /reports/daily-sales`, `/eod-cash`; receipt + reprint per order; no history list | 13 |
| 8 | Journal entries with draft → approve → post → reverse, periods, export | FRD §8 (double-entry, chart of accounts); Phase 2 Ledger & Accounting | Manual JV posts immediately, no approval workflow (deliberate Phase 2 simplification, recorded above); auto-journaling already idempotent | 14 (workflow), 13 (POS petty-cash view; scope decision below) |
| 9 | Granular RBAC: module/action/data scope, approval policies, UI hides/disables, backend enforces | FRD §18 (roles, feature/action/data-level permissions); UACL cross-cutting block above | Backend enforcement + role admin done; UI only reacts to a `403` after the fact; no `/me` permissions endpoint; data-level (own-branch) scoping limited (Phase 2 "Known simplification") | 11 (`/me`), 15 (data scope) |
| 10 | SaaS subscription tiers, limits, trials, renewals, grace, usage dashboards, no loss of legally required data | **FRD §19** (Freemium / Starter / Professional / Enterprise, per-POS pricing, limits, retention, add-ons); FRD Part A "Tiered subscription" objective | Not modelled (no plan/entitlement tables) | 16 |
| 11 | Multiple terminals per branch, terminal settings, shifts, cash management | FRD §1 hierarchy, §18 "Device binding for POS (registered devices only)", §16 cash recon | `pos_terminals` table, no API; POS hardcodes the seed terminal (`TODO(phase 2)` in `session.dart`); PIN device binding exists; EOD cash recon exists, no shift open/close | 12 (pairing), 13 (shifts) |
| 12 | Offline sales, idempotent sync, conflict states, clear indicators | FRD Part A "Offline-first POS"; `phase0_1_design.md` §3.5 (idempotent `/sync/push`, oversell → `NEGATIVE_STOCK` rule), §6.6 | Fully built for cash sales; live-only list documented in `erp-pos-flutter/CLAUDE.md` | 12 (UI states), 13 (sync queue screen) |
| 13 | Performance, scalability, SLOs, load testing | FRD Part A (≤6 s transaction, ≤100 ms search, ≤3 s dashboard, 99.9% uptime, 10,000 txn/hour); `tech_stack_decision.md` §9 (k6/Locust, Flutter `integration_test` with offline transitions) | Functional verification only; no load tests on file | Every phase (UI budgets below); 16 (load test gate) |
| 14 | Custom report template engine | FRD §15 "Custom Builder: drag-and-drop (select fields, filters, groupings)", export PDF/Excel/CSV/JSON, scheduling; FRD §19 places Custom builder in **Professional** | Fixed reports only; NLP-BI routes to 5 fixed report intents (Phase 6) | 16 |
| 15 | Dependency map + business rules | `phase0_1_design.md` §1 ER overview + §2 key decisions | Implicit in code (FK `ON DELETE` rules, `409 ROLE_IN_USE`, `409 PRICE_LIST_IN_USE` style refusals) | 11 (dependency-count endpoints + doc) |
| 16 | Deliverables (FRD, matrices, sitemap, designs, schema, API, tests, deployment) | `pos_frd_complete.md`, `phase0_1_design.md`, `tech_stack_decision.md`, this roadmap, the design page above | FRD, ER, API contract, roadmap exist and are kept current | Each phase adds its own `phase0_1_design.md` §3.x entry, as every phase has |

### Conflicts and open decisions (need the user's call, not decided unilaterally)

These follow the same "don't unilaterally decide a big call" posture already used for the GSP vendor, the LLM vendor and the storefront scope.

1. **Subscription tier names and limits.** The brief's example names ("Basic, Standard, Premium") differ from **FRD §19's four tiers (Freemium, Starter, Professional, Enterprise)**. This plan follows FRD §19 as the source of truth. The design page's subscription mock-up is illustrative until this is confirmed.
2. **Data retention vs statutory retention.** FRD §19 gives Freemium "30-day retention" and Starter "1-year retention". Phase 4's audit trail is deliberately indefinite ("7-year retention" requirement), GST records carry their own statutory retention, and the brief says subscription limits must never compromise legally required data. **Proposed:** tier retention limits only how far back the *UI and reports* reach. Invoices, ledgers, GST data and the audit log are never deleted by a plan change and stay exportable read-only. Needs confirmation.
3. **Open-price items** are new scope, not in the FRD. Proposed model: a product-level `is_open_price` flag with optional min/max, default description editable at billing, an approval threshold, and approver/time/reason stored on the sale line plus `audit_logs`. Inventory treatment (a stock-tracked "misc" variant vs non-stock) needs a decision.
4. **Journal entries inside the POS** (brief §8). FRD §8 places accounting in the back office. Proposed: POS gets a petty-cash/cash-in-out journal only, gated by permission. Full JV authoring and approval stay in the admin. Needs confirmation.
5. **Offline scope widening.** Today discounts, promotions, coupons, loyalty, void and customer attach are live-only (`erp-pos-flutter/CLAUDE.md`). The design proposes allowing discounts up to the 5% no-approval tier offline, and flagging (not blocking) sales priced from a cache older than 24 hours. Both change business rules. Neither ships without sign-off, and neither changes §3.5's existing oversell rule.
6. **JV approval workflow.** Phase 2 deliberately chose "post immediately, no approval workflow". Adding draft → approve → post (brief §8) makes that a configurable merchant setting, with the default left as today's behaviour.
7. **Data isolation Option A** (FRD §1, separate DB per merchant) stays out of scope. Shared-DB RLS is a recorded non-negotiable. The SaaS console in Phase 16 is built on the shared model.

### Cross-phase UI performance budgets

Measured on a mid-range Android tablet (POS) and a mid-range laptop on throttled "Fast 3G" (admin). Each budget derives from FRD Part A's targets; none replaces them.

| Interaction | Budget (p95) | Derived from |
| --- | --- | --- |
| Scan → line visible in cart (online) | ≤ 300 ms | FRD ≤6 s scan-to-receipt |
| Scan → line visible in cart (offline, local cache) | ≤ 150 ms | same |
| Pay → receipt shown (cash, online) | ≤ 1.5 s | same |
| Product search results (POS + admin) | ≤ 300 ms rendered | FRD ≤100 ms server-side search |
| Admin list page, any master, server-paged | ≤ 1 s | FRD ≤3 s dashboard refresh |
| Sync of a 50-sale offline backlog | ≤ 30 s, zero duplicates | §3.5 idempotency contract |

---

### Phase 11 — Design System Foundations & Admin List/Form Kit (design UI-0 + UI-1)

**Size: L | Goal: Both apps share one visual language, and every admin master screen behaves the same way.**

**In:**

- **Tokens in both clients.** In `erp-web-admin/app/globals.css`, replace the greyscale shadcn defaults while keeping the same variable names, so existing components pick them up unchanged. Add `pay`, `warn`, `plum`, `teal` status colors and dark mode. In `erp-pos-flutter`, add `lib/ui/theme.dart` (`ThemeData` + a `ThemeExtension` with the same tokens). Geist is **bundled as an asset**, not fetched, so an offline terminal never falls back to another font.
- **One status language.** Every sale, sync, journal, master-record and subscription state has one fixed color and label in both apps (design page §03).
- **Admin shell.** The flat 42-link sidebar becomes 11 groups, adding icons, breadcrumbs, a branch selector and a ⌘K command palette. **Every existing route keeps its URL**, so bookmarks and all existing Playwright specs remain valid. "New Product" moves from the sidebar to a page action.
- **List/form kit (web):** `DataTable` (server paging, page size, sort, column visibility, row selection + bulk bar, record count), `FilterBar` with filters in the URL query string, `EmptyState`, `Skeleton`, `ConfirmDialog`, `DependencyDialog`, `useUnsavedChanges` (route-leave guard + sticky save bar), `StatusChip`, `MoneyText`.
- **Backend, in the same pass:**
  - A standard list convention on every master list endpoint: `q`, `status`, `sort`, `dir`, `page`, `limit` in; the endpoint's existing collection key plus `total`, `page`, `limit` out (`internal/httpx/list.go`). *As built:* the key is not renamed to `items`, and an endpoint that returned every row still does when no `page`/`limit` is passed, so existing pickers and clients are unaffected.
  - Lifecycle for categories, brands and tax slabs: edit, activate/deactivate, delete only when unused (`migrations/036`). *Correction:* their list endpoints already existed since §3.19; the "no category/brand list endpoint" note in `erp-web-admin/CLAUDE.md` was stale. Units of measure have no table yet and stay out of Phase 11.
  - `GET /me/permissions`, so the UI can disable an action with a reason *before* a `403`. The backend stays the enforcer; this changes no gate.
  - Per-master dependency counts behind `DependencyDialog` (e.g. a product's sales lines, bills, price lists, promotions), reusing the existing `409 *_IN_USE` refusals instead of replacing them.
- **Migrate master screens** onto the kit one at a time: products, customers, suppliers, branches, categories, brands, units, tax, price lists, promotions, coupons, employees, roles.

**Deferred:** POS layout changes (Phase 12), new business flows.

**Definition of Done:**

- Every existing Playwright spec and Flutter test passes unchanged.
- Every master list has server paging, search, sort, filters and a record count.
- Delete on a referenced record shows its dependencies and offers deactivate.
- Unsaved forms warn before leaving.
- Screenshot baselines exist for each migrated screen in light and dark.
- An axe accessibility scan is clean.
- A keyboard-only create → edit → deactivate test passes.

**Status: in progress (started 2026-10-10).** Done and verified:

- **Tokens in both clients.**
  - Web: `app/globals.css`, with dark mode via `next-themes` (light by default). This also fixed a self-referencing `--font-sans` that had silently kept Geist from ever applying.
  - Flutter: `lib/ui/theme.dart` (`PosColors` theme extension) with Geist bundled under `assets/fonts/` (OFL). This also fixed the POS app-bar actions, whose hard-coded white icons sat on a light Material 3 app bar.
  - 55 admin screens' hard-coded palette classes now map to tokens.
- **Admin shell:** 11 grouped sidebar sections (every label and URL unchanged), breadcrumb, Ctrl/⌘+K screen palette, theme switch, mobile drawer.
- **Backend:** list convention, master lifecycle, dependencies and `/me/permissions` — see `phase0_1_design.md` §3.32 (29 live API checks, plus parser unit tests).
- **Web kit:** `DataTable` (URL-synced search/filter/sort/paging, skeleton/empty/no-results/error states), `StatusChip`, `PageHeader`, `Modal`/`AlertModal`, `DeleteWithDependencies`, `useUnsavedChanges`, `PermissionsProvider`.
- **Screens on the kit:** new Categories, Brands and Tax Slabs screens; Suppliers, Products, Customers and Branches migrated (server search/filter/sort/paging in the URL). Supplier and branch editing via a shared `EditDialog`.
- **Edit semantics fixed (2026-10-10, part 2):** supplier and branch PATCH now treat an omitted field as "keep" and `""` as "clear". Before, nothing could be cleared, and a supplier's credit limit could never return to 0. Branch timezones are validated against Postgres's zone list, the same source price rules use.
- **Accessibility:** `e2e/phase11-a11y.spec.ts` runs an axe WCAG 2.1 A/AA scan of the kit screens in light and dark, plus a keyboard-only create/edit/deactivate test. The scan found three light-theme status colors below 4.5:1 on their chip backgrounds (green 4.28, amber 4.14, red 4.45). They were darkened to ≥4.7:1 in both clients. Writing the keyboard test also exposed two real gaps, both fixed: no skip link (a keyboard user had to Tab through ~75 sidebar links on every page), and dialogs not returning focus to the control that opened them.
- **e2e:** 13 Phase 11 tests pass. Full suite: 41 pass, 7 fail. Six are the known environment failures listed below; the seventh, `barcode.spec.ts`, had a timing race in the spec (it read the barcode before Generate had saved), now fixed and passing 3/3.

- **Remaining masters (part 3):**
  - Employees: server-paged list, newest first, plus an exit confirmation that says what exiting does (login disabled, irreversible from the UI).
  - Price Lists: delete now asks first.
  - Roles & Permissions: permissions grouped by module with "Select all"; an unsaved-change count and a guard when switching roles; delete confirmation; revoke with Undo.
  - Promotions & Loyalty: a section jump bar.
- **Product picker:** a shared, server-search `ProductPicker` replaces "load the first 200 products" in Price Lists, Promotions and Inventory Reconciliation. Items past 200 were silently unreachable on a real catalog, and this was the actual cause of the inventory-reconciliation spec failure.
- **Category picker:** Promotions' category scope now uses a real picker. It used to ask for a pasted category UUID.
- **Seed PIN and spec race:** restoring the seed manager PIN (`1234` for Arjun, compose seed DB only, via `/dev/hash-password`) let the two PIN-approval specs run for real. That exposed a latent race in `inventory-reconciliation.spec.ts`: it waited on a result badge that could still show the *previous* submission. It now waits for the form to clear, and passed 5/5.
- **Full suite:** 46 pass, 4 fail. All four are environment-only: `pricing` (copy clash from `9c786bc`), `grocery` and `payment-gateway-reconciliation` (accumulated seed-run data), and `einvoice` (merchant GSTIN wiped by a seed reset).

**Still to do in Phase 11:** light/dark screenshot baselines, which need fixed fixture data rather than the accumulating seed DB or they will flap. Coupon lists load unpaged; fine today, but single-use campaign coupons will need the list convention. Branch deactivation stays out of the UI until Phase 12 defines what an inactive branch may do. Units of measure need a table first.

**e2e environment notes, found while verifying (not caused by Phase 11):**

- Since `fd15e48` the compose `api` service points at the *host* Postgres (merchant `adr_sports_world`), while every spec targets the `acme-sports` seed data in the compose database. Phase 11 was verified with a throwaway API container on the seed database, with the developer's own API stopped only for the run and restored afterwards.
- `da24e81` removed the login screen's pre-filled email, which 11 spec logins silently relied on. Those specs now fill the email explicitly.
- The pre-filled merchant code now comes from `NEXT_PUBLIC_DEFAULT_MERCHANT_CODE` (unset = `adr_sports_world`, so nothing changes day to day). Run e2e with it and `E2E_MERCHANT_CODE` set to `acme-sports`.
- Six existing specs still fail for reasons outside this phase:
  - `pricing.spec.ts`: strict-mode clash with "customer pays ₹…" text added in `9c786bc`.
  - `grocery.spec.ts` and `payment-gateway-reconciliation.spec.ts`: loose matchers collide with leftovers from earlier runs.
  - `inventory-reconciliation.spec.ts`: its product picker only loads the first 50 products, and accumulated test products push the seed bat off that page. Fixed for good when that picker moves to the kit.
  - `cash-reconciliation.spec.ts`: depends on a manager PIN set by hand during earlier backend verification, which a seed-volume reset has since wiped.
  - `einvoice.spec.ts`: depends on the merchant GSTIN, likewise wiped by a reset.
- Specs that read "today" are also unreliable between 00:00 and 05:30 IST (UTC and IST dates differ), as `01875f5` already found for the pharmacy spec.
- **Migration 036 has been applied to the compose seed database only.** The host development database needs `psql -f migrations/036_master_data_lifecycle.sql` before an API built from this code runs against it (the running container is still on the previous image).

### Phase 12 — POS Billing Redesign & Terminal Pairing (design UI-2)

**Size: L | Goal: A cashier can complete a normal sale with scanner + keyboard alone, on any supported screen size, and always knows the terminal's sync state.**

**In:**

- Split `lib/screens/pos_screen.dart` (813 lines) into `StatusBar` (terminal, shift, held count, sync pill, cashier), `ProductGrid` (category chips + tiles with stock), `CartPanel` (qty steppers, selected-line actions), and `PaymentSheet` (tenders, quick cash, change due). Coupon, promotions and loyalty move into the payment step.
- Function-key shortcuts via Flutter `Shortcuts`/`Actions`: F2 search, F3 open price (enabled in Phase 13), F4 customer, F7 discount, F8/F9 hold/recall, F12 pay, `+`/`−`/`Del` on the selected line.
- Responsive layouts at the design's breakpoints: phone (<600), tablet portrait, landscape, ≥1280. The 1024×768 all-in-one terminals fall in the landscape layout.
- Offline UI states: amber sync pill, offline banner, and unavailable tenders shown disabled *with the reason* (the rules already in `erp-pos-flutter/CLAUDE.md`, not new ones).
- Shared `ApprovalPinPad`, used first for the existing manual-discount approval tiers (FRD §5).
- **Terminal registration/pairing**, closing `session.dart`'s `TODO(phase 2)` and FRD §18's "registered devices only". Backend: `POST/GET/PATCH /pos-terminals` (activate/deactivate, name, settings) plus a one-time pairing code. The POS stops hardcoding `kSeedBranchId`/`kSeedPosTerminalId`. Branch-level terminal management screen in the admin.

**Deferred:** new sale flows (Phase 13).

**Definition of Done:**

- Flutter golden tests at 360, 800 and 1280 widths, light and dark.
- A Flutter `integration_test` completes a full sale without pointer input (`tech_stack_decision.md` §9).
- The scan → cart budget above is met on a real mid-range tablet.
- Two paired terminals in one branch sell concurrently against the same stock without double-reserving (existing optimistic locking, now exercised from two real devices).
- On-device verification closes Phase 1's still-open hardware item where hardware is available.

**Status: done (2026-10-10), except the on-device items noted below.**

- **Terminals and pairing (backend, `migrations/037`, `internal/terminals`):**
  - Terminal list, create, rename, activate/deactivate.
  - Pairing codes: 8 characters from an alphabet with no look-alike characters, live 15 minutes, single use, stored only as a SHA-256 hash.
  - Device reset.
  - An unauthenticated pairing endpoint where every failure returns the same 401, so it can't be used to probe merchants or codes.
  - `terminal.manage` granted by role name across all merchants.
  - PIN login is refused when the terminal's branch is inactive. This is what deactivating a branch now does.
  - Discounts accept the approver's employee code (`authorized_by_code`).
- **Admin:** a POS Terminals screen (pair with a code and countdown, unbind, activate/deactivate) and a branch deactivate toggle whose confirmation says the branch's tills close.
- **POS pairing:**
  - The app no longer hardcodes the seed terminal. It boots into a pairing screen until paired, then logs in for its own terminal.
  - The login screen shows the terminal and branch, fills in the merchant code, and offers "Unpair this device" (signed-out only).
  - `--dart-define=USE_SEED_TERMINAL=true` keeps the seed terminal for development.
- **POS billing redesign:** `pos_screen.dart` (813 lines, one file) is split into parts:
  - a status bar
  - a product grid (category chips online; offline it searches the device's catalog cache by name/SKU, with cached stock)
  - a cart panel (quantity steppers, a selected line)
  - a payment sheet (cash given with quick amounts and change due, card/UPI reference, credit, split payments, and coupon/promotions/loyalty applied just before charging)
  - a shared `ApprovalPinPad`
  - the existing dialogs

  F2/F4/F7/F8/F9/F12, `+` `−` `Del` and Esc drive a whole sale. Layouts cover phone (<640 px, with a cart bar), tablet portrait (stacked) and ≥1000 px (two panes). Offline, the screen shows a banner, and live-only actions and the credit tender are disabled with the reason.
- **Two real bugs caught by the new tests:**
  - The scan field was disabled while an item was being added, which dropped its focus, so the next item from a scanner gun went nowhere.
  - The payment sheet looked up the session from its own route, so it only worked when the provider sat above `MaterialApp`.

  Both are fixed and covered.
- **Tests:**
  - Go unit tests for pairing-code generation and hashing.
  - 13 live API checks plus discount-by-code (compose seed DB).
  - `e2e/phase12-terminals.spec.ts` (3 tests), plus Phase 11 and multi-branch re-run: 18/18.
  - Flutter: 26 pass. These include a keyboard-only offline cash sale, offline UI states, offline search, pairing and boot tests, and goldens at 390, 800 and 1280 px. The live dialog test was updated for the screen's on-open grid load and passes against the seed backend.

**Not done, by design or environment:**
- **On-device timing:** the scan-to-cart timing budget on a real mid-range tablet, and two physical terminals selling concurrently. There's no Android/iOS hardware here, the same limitation Phase 1 records. Concurrency is covered by the existing optimistic-locking tests, not by two devices.
- **`integration_test`:** the keyboard-only sale runs as a widget test against real SQLite, not as an `integration_test` on a device, for the same reason.
- **Credit live test:** `api_client_live_test.dart`'s credit-payment test fails on data drift. It sets a ₹5,000 credit limit, but the seed bat's price has drifted to ₹15,000 through earlier pricing specs, and the backend correctly answers `CREDIT_LIMIT_EXCEEDED`.
- **Migrations:** `036` and `037` must be applied to the host development database before an API built from this code runs against it.

### Phase 13 — New POS Flows: Open Price, Sales History, Returns, Shifts, Labels, Sync Queue (design UI-3)

**Status: in progress (2026-10-10).** Decisions taken at the start:
- open-price items use normal stock
- whether returns need approval is a per-merchant setting, defaulting to a manager's PIN
- the POS journal is cash in/out only

Done so far:
- **Sales history and summary:** `GET /sales/orders` and `/sales/summary` (`phase0_1_design.md` §3.34) filter on branch-local dates.
- **POS Sales history & returns screen (F6):**
  - a day summary, scoped to this till or the whole branch
  - period, status and search filters
  - this device's queued offline sales, marked as waiting to sync
  - a receipt view to reprint any sale
- **Returns and refunds** (`migrations/038`, §3.34):
  - partial or full returns
  - a per-line refund that adds up exactly to what was paid
  - restock back to the batches the sale consumed (optional, for damaged goods)
  - a mirror `sales_return` journal entry
  - credit refunds, but only up to the unpaid credit
  - safe to replay with the same idempotency key
  - order-row locking
  - statuses `partially_refunded` and `refunded`
- **POS return screen:**
  - quantity steppers, reason chips, refund method and an optional reference
  - a restock switch
  - manager code plus PIN on the shared pad when the policy requires it
  - one idempotency key per screen, so a retry never refunds twice
- **Admin Return Policy page** (`sales.settings`): approval on or off, and an optional return window.
- **Tests:**
  - 32 live API checks
  - Flutter widget tests for history plus an approval-retry return and for offline queued sales, and a live client test
  - `e2e/phase13-return-policy.spec.ts`
  - the a11y sweep now covers Terminals and Return Policy

Still to do in this phase: open-price items, shifts (open, cash in/out, X/Z close), the sync queue screen, product lookup and labels from the POS. **Migration `038` must be applied to the host database.**

**Size: XL | Goal: The POS covers the counter's whole day, from open shift to close, not just a single sale.**

**In:**

- **Open-price items** (decision 3 above). Backend fields + validation + approval capture on the line + `audit_logs`; keypad dialog in the POS. Receipts, history and reports show the entered price and approver.
- **POS sales history.** Backend: paged `GET /sales/orders?terminal_id=&cashier_id=&status=&payment_method=&from=&to=`, plus a terminal/day summary (gross, discounts, tax, net, per-tender split) reusing the `reports` package's existing aggregation. POS shows filters, detail, reprint, and local `OFF-` sales from SQLite marked as queued until synced.
- **Returns & refunds** (FRD §3 "Return & Exchange", hybrid immediate vs approval, configurable). Return and refund statuses are distinct from void, and stock and ledger reversal reuse the existing void posting pattern (mirror entry, never delete). Returning an unsynced offline sale is refused with a clear message.
- **Shifts.** Open with a float, cash in/out, close with an X/Z report. Close feeds the existing Phase 4 cash reconciliation instead of duplicating it.
- **Product lookup in POS.** Stock by branch, price edit (`pricing.manage`), stock adjust (`inventory.adjust`). Same endpoints and guards as the admin; past sales are never re-priced.
- **Labels from POS.** Calls the existing `GET /products/variants/{id}/label`. Adds FRD §12's two missing predefined templates (Detailed 100×50 with batch/expiry; QR 50×50) server-side.
- **Sync queue screen.** Pending, synced, failed and needs-review items, with retry. A `NEGATIVE_STOCK` oversell from §3.5 surfaces here and in the admin as "needs review" (the existing rule, now visible).
- **Petty-cash journal in POS**, if decision 4 is confirmed.

**Deferred:** admin-side equivalents (Phase 14).

**Definition of Done:**

- Offline sale → reconnect → exactly one server sale and one ledger posting, verified with a forced retry storm (the §3.5 idempotency contract, now proven from the device).
- A return posts the exact mirror journal entry.
- Shift close reconciles to the cash-recon figure.
- Open price outside min/max is refused, and a value above the approval threshold is refused without a valid supervisor PIN.
- Every new endpoint's error codes are added to `phase0_1_design.md` §3's catalogue.

### Phase 14 — Admin Sales, Label Studio, JV Workflow (design UI-4)

**Size: L | Goal: The back office can sell, refund and print labels with exactly the same rules as the till.**

**In:**

- **Admin new-sale screen** built on the existing `CreateCart`/`AddLineToCart`/`Checkout` path (Phase 7's extraction), so pricing, tax, promotions, credit limits and stock reservation are the *same code*, not a parallel implementation. It runs on the `channel` model Phase 7 introduced.
- **Admin sales history + detail**: items, payments, tax breakdown, discounts, cashier, branch, terminal. Includes authorized cancel, return, refund and post-finalization correction (FRD §3 "Bill Modification": manager approval + reason + full audit).
- **Label studio.** Product/GRN selection with quantities (FRD §12 bulk printing, "print entire GRN"), size presets plus custom, field toggles, live preview with a scannability check, and three explicitly distinct print paths:
  - direct thermal: ESC/POS today, with ZPL/TSPL added per FRD §12;
  - system print dialog at the exact page size;
  - browser A4 sticker-sheet PDF.
  The FRD's WYSIWYG drag-and-drop editor is scoped as a follow-up unless the user pulls it in.
- **JV workflow as a merchant setting** (decision 6): draft → submitted → approved → posted → reversed, with period lock, paging, search and CSV/PDF export. Auto-generated entries stay read-only and link to their source document.

**Definition of Done:**

- A shared fixture cart totals identically to the paisa in POS and admin.
- Labels scan on a real 203-dpi thermal printer at every preset size (or this is recorded as environment-blocked, the same way Phase 1's hardware item is).
- Posted JVs can only be reversed, never edited (enforced server-side and tested).

### Phase 15 — Data-Scoped RBAC & Approval Policies

**Size: M | Goal: FRD §18's data-level permissions ("own branch vs all branches") are real, and approval thresholds are configurable without code.**

**In:**

- Branch-scoped access for password sessions. This closes Phase 2's recorded "Known simplification": only PIN-login carries a `branch_id` claim today.
- Configurable approval policies (price override, discount tiers, refund, stock adjustment, JV posting) stored per merchant and read by the existing inline-PIN pattern.
- Admin UI for both.
- `GET /me/permissions` gains data scope.

**Definition of Done:** A Branch Manager sees and acts on only their branch's data across every list, report and export, verified server-side per endpoint, not just in the UI.

### Phase 16 — SaaS Subscriptions, Entitlements & Report Builder (design UI-5)

**Size: XL | Goal: FRD §19's tiered subscription model is enforced by the backend, and FRD §15's custom builder exists for the tiers that include it.**

**In:**

- **Plans and entitlements as data, not code.** Plans, features, limits (branches, POS, products, transactions/month, retention) and add-ons per FRD §19, editable by platform staff without a deploy. Enforcement lives in Go middleware next to the existing permission middleware. The UI only reflects it (lock icon + preview page, never a 404).
- **Subscription lifecycle:** trial, active, grace period, expired. Expiry means read-only, never deletion (decision 2). Includes upgrade/downgrade (a downgrade over a limit blocks *new* creation and never deactivates existing records), billing records and payment status. A real payment-gateway/billing vendor is a separate vendor decision, handled like the GSP stub: interface plus stub first.
- **Merchant usage dashboard + SaaS (platform staff) console** for merchants, plans and billing. It runs on the shared-DB RLS model (decision 7) behind a separate platform-staff role.
- **Report template engine** (FRD §15 Custom Builder): field and column picking from an allow-listed semantic layer per domain (sales, inventory, products, payments, tax, journals, branches, terminals). Supports filters, grouping, aggregations, saved/duplicated templates and export (CSV/Excel/PDF), with RBAC and branch scope applied server-side. Heavy reports run as background jobs. Query cost limits mean a template can never issue unbounded SQL. As with NLP-BI (Phase 6), users never write SQL.
- **Load-test gate** (`tech_stack_decision.md` §9: k6/Locust) at 10,000 txn/hour across multiple tenants and terminals before calling the SaaS model production-ready.

**Definition of Done:**

- Exceeding a plan limit is refused server-side with a clear upgrade message.
- An expired tenant can still read and export every invoice, ledger, GST report and audit row.
- A report template can't return data outside the caller's branch scope.
- The load test meets FRD Part A's targets with the results recorded.

### QA approach applied to every phase above

- **States checklist.** Loading, empty, no-results, error, forbidden, plan-locked, offline/stale and unsaved/conflict (design page §08) are part of each screen's acceptance, not polish.
- **Verification as before.** Same verification bar as Phases 0–10: live against the docker-compose backend, not mocked. Playwright for the admin, Flutter widget/golden/`integration_test` for the POS, and every real bug found live recorded in that phase's `phase0_1_design.md` §3.x entry.
- **Regression.** No phase is marked done while any pre-existing spec or test is red.

---

## Solo-Builder Notes

A few honest calls worth making as you execute this, given it's 1-2 people:

- **Don't build what you can configure or buy.** Use existing GST-calculation logic patterns, existing e-invoice/e-way-bill integration SDKs (several Indian GSP providers offer these) rather than building NIC portal integration from scratch in Phase 4. Use `shadcn/ui` + Tailwind component libraries rather than designing a UI kit from zero.
- **Auth is the one area worth revisiting.** You chose custom-built auth to keep a future Keycloak/managed-IdP swap easy — that's the right long-term call, but it's also one of the highest-effort, highest-risk-if-wrong pieces (MFA, SSO, session security) to hand-build solo. If Phase 0/1 velocity is suffering, dropping in Keycloak now (it's self-hosted and fits your local-first requirement just as well) and building the thin custom layer on top of it — rather than fully custom underneath — is a reasonable mid-course correction. Worth revisiting once you're actually in it, not a decision to force now. **(User's explicit answer, on record: keep custom-built auth as-is — "I need all this in my control in my service." Do not re-raise this unless the user brings it up again.)**
- **Phase 1 is the only phase that really matters for validating the business.** Everything from Phase 2 onward can slip without risk; if Phase 1 doesn't prove a real store can run on this, the rest of the roadmap is moot. Resist the pull to add "just one more module" before Phase 1 is genuinely done.

---

## Summary Timeline (dependency order, not calendar)

| Phase | Focus                                                | Size    | Status                                                                                                                                                                                   |
| ----- | ---------------------------------------------------- | ------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 0     | Walking skeleton                                     | S       | Done                                                                                                                                                                                     |
| 1     | Core retail loop (real MVP)                          | XL      | Complete except on-device hardware verification, which is environment-blocked (no physical Android/iOS device or network thermal printer available), not a code gap — see above            |
| 2     | Purchase, Accounting, Multi-branch                   | L       | Fully complete — Purchase, Accounting, Multi-Branch, Pricing, Search all done, see above                                                                                                 |
| 3     | CRM, Loyalty, Promotions                             | M       | Done — see above                                                                                                                                                                         |
| 4     | GST compliance depth, Audit                          | L       | Fully complete — Credit Facility, GST Returns, Bank/Payment Gateway/Inventory Reconciliation, audit trail hardening, and E-Invoicing/E-Way Bill (vendor-agnostic stub GSP) all done, see above |
| 5     | HR & Payroll                                         | L       | Complete — employee records (extends `users`, not a new table), attendance/shifts, versioned salary structures, merchant-configurable statutory config, commission engine, payroll run with challan summary, see above |
| 6     | AI Platform v1                                       | L       | Complete except OCR (deliberately deferred) — reorder suggestions, recommendation engine, demand forecast, anomaly detection (all real math/SQL, no trained model), NLP-BI, and the AI Copilot (both via a pluggable Ollama/Anthropic `LLMClient` interface) all done, see above |
| 7     | Wholesale/B2B, Omnichannel                           | L       | Definition of Done met — B2B quotations, wholesale price lists, and a channel-agnostic backend all done; a real storefront app is a deliberately separate, unbuilt follow-up, see above |
| 8     | Vertical expansion (grocery, pharmacy, apparel, ...) | Ongoing | Complete (audited 2026-10-04) — all five verticals done and live-verified (Jewelry/Electronics/Sports via attribute-sets; Apparel via collections; Grocery/FMCG via batch/expiry tracking, now batch-aware on every stock path; Pharmacy via prescription linkage + shelf-life policy) — see above |
| 9     | POS Hold & Recall, barcode visibility                | M       | Done — backend + Flutter POS UI for Hold/Recall, barcode manual-entry/generate visible on the web-admin product list, see above |
| —     | Session expiry handling, GST-inclusive pricing       | M       | Done — both clients return to login on expiry (silent refresh while active); per-product inclusive/exclusive GST pricing with a merchant default, see above |
| —     | Product editing & status enforcement                 | M       | Done — edit product/variants, add variant, enforce discontinued/inactive at scan, cart and quotations, Products + edit screens, see above |
| 10    | Pricing & quotation gaps                             | M       | Done — scheduled price changes (incl. bulk), dynamic price rules (happy hours/weekend/seasonal, branch timezone, MRP cap), draft-quotation editing, and quote conversion now bills the quoted price, see above |
| 11    | Design system foundations, admin list/form kit       | L       | Nearly done (2026-10-10) — tokens, shell, kit, master lifecycle, dependencies, `/me/permissions`, a11y + keyboard tests; every planned master screen on the kit; server-search product picker. Open: visual baselines (need fixture data), coupon paging, see above |
| 12    | POS billing redesign, terminal pairing               | L       | Done (2026-10-10) — terminal management + one-time pairing codes, inactive branch closes its tills, POS pairing screen, billing redesign (grid/cart/payment sheet, F-keys, 3 layouts, offline search), approver by employee code; on-device timing/concurrency not verifiable here, see above |
| 13    | New POS flows                                        | XL      | Planned — open price, sales history, returns/refunds, shifts, POS product/label tools, sync queue, see above |
| 14    | Admin sales, label studio, JV workflow               | L       | Planned — admin sale on the shared checkout path, sales history/refunds, label studio (ESC/POS + ZPL/TSPL + system + PDF), configurable JV approval, see above |
| 15    | Data-scoped RBAC, approval policies                  | M       | Planned — branch-scoped password sessions, configurable approval thresholds, see above |
| 16    | SaaS subscriptions, entitlements, report builder     | XL      | Planned — FRD §19 tiers enforced server-side, read-only on expiry (never delete), SaaS console, report template engine, load-test gate; 7 open decisions listed above |
