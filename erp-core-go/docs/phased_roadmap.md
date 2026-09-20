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

**Status: functionally complete against this phase's Definition of Done.** Every item on the original "Still open" list is now closed except a standalone reservations endpoint nothing actually needs yet, a forgot-password flow, and two verification steps that are environment-blocked rather than code gaps (see bottom of this section). Built: catalog barcode lookup, full cart → add-line → discount → delete-line → checkout with optimistic-locking stock reservation, idempotent checkout, session-tiered auth (login/PIN-login/refresh/logout), failed-login lockout, the reservation-expiry sweeper, inventory adjustments with audit trail, and basic reports (see `phase0_1_design.md` §6 for the full list and how each was verified live against this repo's own `docker-compose.yml`).

**§6.1 of that doc is worth reading before touching anything else here**: the hardening pass found that `docker-compose.yml`'s `api` service was connecting to Postgres as a superuser (`app_user`, via `POSTGRES_USER`), which silently disabled every RLS policy in the system since Phase 0 — the multi-tenancy guarantee this whole codebase is built around had never actually been enforced by the database in this stack. Fixed (a dedicated non-superuser `erp_app` role, `migrations/004_least_privilege_app_role.sql`) and re-verified live, but **if you've deployed this anywhere outside the shipped docker-compose, check that deployment's DB role the same way** — this class of bug (an admin/master role standing in for the app's own role) isn't specific to Docker.

**A second hardening pass then closed everything else that didn't require a new architectural decision** — general permission-code enforcement, `PATCH`/`customer`/`receipt`/`void` endpoints, pre-tax GST discount treatment, and PIN-login UI in Flutter (see `phase0_1_design.md` §6.5 for what closed and how each was verified live).

**Offline sync — the last item on the previous open list — is also closed** (`/sync/push`/`/sync/pull`, and a `sqflite`-backed local store in the Flutter app; see `phase0_1_design.md` §6.6). One real gap in how it was verified: there's no Android/iOS device or emulator in this environment, so the offline flow was verified by a real SQLite-backed test (`sqflite_common_ffi`) plus static analysis, not by clicking through the app on a device — **do that before trusting this in front of a real cashier.**

**Barcode & label generation, printer integration — closed** (`internal/printing`, `internal/catalog/ean13.go`/`barcode_assign.go`; see `phase0_1_design.md` §3.8 for the full endpoint reference). Found still-open during an explicit "confirm Phase 1 is 100% before starting Phase 3" audit the user asked for — this item was named in Phase 1's own Definition of Done ("receipt-printing") and hadn't actually been built, so it was closed before Phase 3 started rather than carried forward as debt:

- `POST /products/variants/{id}/barcodes` — the FRD's "Hybrid: use supplier barcode if exists, else auto-generate" (`pos_frd_complete.md` §12). Auto-generated codes are sequential EAN-13s in GS1's reserved in-store-use prefix range (20-29), which can never collide with a real product's actual assigned barcode.
- `GET /products/variants/{id}/label?template=standard|compact` and `GET /sales/orders/{id}/receipt/print` — a real ESC/POS driver (`internal/printing`), not a stub, chosen as the roadmap's named "one printer integration" since a single ESC/POS thermal printer commonly serves both roles on a small retail counter.

Verified without physical printer hardware the same way offline sync was verified without a physical device: `internal/printing.Decode` parses generated byte streams back into structured commands, so unit tests assert the actual bytes a real printer would receive (barcode symbology byte + data, every text run) are correct — not just that the code compiles and runs. Beyond the unit tests, live-verified against the real backend: printed a real finalized order's receipt and hex-dumped it (content and ESC/POS framing both correct); fetched both label templates for the seed variant and hex-dumped them (confirmed the exact `GS k` barcode command — symbology byte `0x43` = EAN-13, correct length, correct 13-digit code); verified all four barcode-assignment paths (auto-generate, supplier-provided, invalid EAN-13 check digit rejected, duplicate code rejected with `409 BARCODE_EXISTS`); full Go test suite and all 4 Playwright specs still pass. EAN-13 checksum math is checked against GS1's own published worked examples, not just internally-consistent arithmetic. **On-device verification against real printer hardware remains open** — same caveat already on file for offline sync.

**Still open:**
1. Standalone `POST/DELETE /inventory/reservations` (only relevant outside the cart flow, which doesn't need them today)
2. Password-reuse history, a real forgot-password flow (superseding the dev-only `/dev/set-password` tool)
3. On-device verification of the offline sync flow (see above) and of the new printer integration — the two pieces of this phase not exercisable on actual hardware in this environment

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
- `POST /branch-transfers` → `approve`/`reject` (permission-gated: `branch_transfer.approve`, Branch Manager/Merchant Admin only) → `dispatch` (all-or-nothing stock check against *available*, not just on-hand, stock at the source) → `complete` (adds stock at the destination; supports a per-line `received_quantity` override for transit discrepancies) → `cancel` (before dispatch only)
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

**Bug found immediately after shipping this, by the user searching a real product name and getting zero results:** the `opensearch` service in `docker-compose.yml` had no volume — every container recreation started it as a brand-new empty node, silently discarding every indexed document while the API kept running normally against everything else. `EnsureIndex` recreated the (now-empty) index on the next API boot without complaint, so nothing *looked* broken until a real search came back empty. Fixed two ways, not just one: (1) `opensearch-data` is now a named, persisted volume, same treatment `pgdata` already had, so an ordinary restart no longer loses anything; (2) `search.BackfillAllTenants` (`internal/search/index.go`) runs automatically at API startup whenever the index comes up with zero documents, rebuilding every tenant's slice from Postgres unattended — a real disaster-recovery/fresh-environment case the volume fix alone wouldn't cover. Verified live: recreated the `opensearch` container to reproduce the empty-index bug, confirmed the exact failure (`"SG Cricket Bat"` search returning zero results), then rebuilt with both fixes and confirmed the startup log showed the automatic backfill running and the search succeeding immediately after, and that the index survives a subsequent `opensearch` restart.

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

**Status: in progress. Sub-area 1, Customer Management, is built and verified live** (`migrations/011_customers.sql`, `internal/customers/`) — B2C/B2B profiles and auto-segmentation, going first because Promotions & Loyalty and expanded Notifications (this phase's next two sub-areas) both need real customer profiles/segments to target:

- `POST/GET/PATCH /customers`, `GET /customers/{id}` — full CRM registration (mandatory name/phone/email per the FRD, B2B additionally requiring a GSTIN), search/filter by name/phone/email/customer_type/segment, and a detail view with consolidated cross-branch purchase history.
- Auto-segmentation (VIP/Regular/New/Dormant) computed on read from real `sales_orders` history, matching `pos_frd_complete.md` §6's exact thresholds — see `phase0_1_design.md` §3.10 for the full rule and how each branch was verified live.

**Deferred, matching this sub-area's own scope** (see the migration's header comment): credit facility/B2B payment terms (bundled into Phase 4's accounting depth, per this phase's own "Deferred" line above), loyalty points and customer-level discounts (the next sub-area, Promotions & Loyalty, not this one).

**UI, built in this same pass:** `erp-web-admin`'s Customers screen (search/filter list with segment badges, a B2C/B2B registration form with conditional GSTIN validation, and a detail page with inline profile editing and purchase history) — verified with a real Playwright test (`e2e/customers.spec.ts`).

**Sub-area 2, Promotions & Loyalty, is also built and verified live** (`migrations/012_promotions_loyalty.sql`, `internal/promotions/`, `internal/loyalty/`) — every hierarchy item `pos_frd_complete.md` §5 names except coupon payment-method restriction (deferred — payment method isn't known until checkout, after discounts already apply; see the migration's header comment) and "Bundles" (already covered by Phase 1's composite-product model, not a new promotion type):

- `POST/GET/PATCH /promotions` — percent/fixed/BOGO/volume/min_value promotion types, product/category/order-scoped application, `target_segment` (folds hierarchy item 4, "customer-level discounts," into promotions rather than a separate mechanism), exclusive-vs-stackable, and an active date/day-of-week/time-of-day window (covers "time-based/happy hours" without a separate promo_type). Gated by a new `promotions.manage` permission.
- `POST/GET/PATCH /coupons` and `POST /sales/orders/{id}/coupons` — usage caps (total and per-customer), validity window, minimum purchase, branch, and channel constraints, all enforced at apply-time; one coupon per order.
- `POST /sales/orders/{id}/promotions/apply` — evaluates and auto-applies eligible promotions against a cart, respecting each promotion's stacking rule.
- `GET/PATCH /loyalty/config`, `GET /customers/{id}/loyalty`, `POST /sales/orders/{id}/loyalty/redeem` — configurable earn/redeem rates, a "rolling" whole-balance expiry (computed on read, no sweeper), and automatic point-earning on checkout finalize that correctly skips itself when the same order already redeemed points (the FRD's "cannot earn and redeem in same transaction").

The one piece of real engineering this sub-area needed beyond CRUD: every layer (promotional, coupon, customer-level, loyalty redemption, and — now updated — manual) applies through one shared function, `sales.ApplyDiscountLayer`, which distributes a resolved rupee amount across lines' *current remaining* taxable value and adds it to `discount_amount`, so multiple layers stack instead of one overwriting another. This changed `POST /sales/orders/{id}/discounts` (manual, hierarchy item 6, previously the only discount source and the only one Phase 1 built) from overwrite to additive — a single manual-only discount still behaves identically (0 existing + share = share), but a cart that also has a promotion/coupon/redemption applied now correctly keeps all of them.

Verified live end to end against the seeded merchant and cricket bat: a category-scoped 10% exclusive promotion auto-applied, a flat-₹100 coupon stacked on top of it (not overwriting it), a second coupon attempt on the same order correctly rejected, a manual 5% discount stacked on top of both, checkout finalized correctly, the customer earned the correct floor-divided loyalty points, a second order redeemed those points for the correct rupee discount and checked out, a follow-up balance check confirmed that redeeming order did *not* also earn, a `buy 2 get 1 free` BOGO promotion against 3 units discounted exactly one unit's price, and a coupon with an unreachable minimum purchase correctly rejected a small order. `promotions.manage` confirmed denying a POS User with `403 FORBIDDEN`. See `phase0_1_design.md` §3.11 for the full endpoint reference and exact verified figures.

**UI, built in this same pass:** `erp-web-admin`'s Promotions & Loyalty screen (`/promotions`) — promotion CRUD (a product picker sourced from `GET /products`, since there's still no category-list endpoint to build one from), coupon CRUD, and a loyalty settings form. Deliberately CRUD/config only — this app has no cart/checkout UI (that's `erp-pos-flutter`'s job), so *applying* a promotion/coupon/redemption to a live sale isn't exposed here, matching the same boundary Pricing's screen already draws. A customer's loyalty balance/ledger is shown read-only on their existing detail page instead of a new one, since it's customer-scoped state, not merchant-wide config. Verified with a real Playwright test (`e2e/promotions.spec.ts`) that creates and deactivates a promotion and a coupon, confirms a POS User is denied writing, and — since the UI itself can't produce a finalized sale — seeds one real checkout via a direct API call to confirm the customer detail page's loyalty section renders the resulting earned points and ledger row correctly.

**Not yet started:** Notifications (expanded) — SMS/WhatsApp for receipts, low-stock alerts, payment reminders (this phase's remaining "In" item).

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
| 2 | Purchase, Accounting, Multi-branch | L | In progress — Purchase Management & Ledger/Accounting done, see above |
| 3 | CRM, Loyalty, Promotions | M | In progress — Customer Management & Promotions/Loyalty done, see above |
| 4 | GST compliance depth, Audit | L | Not started |
| 5 | HR & Payroll | L | Not started |
| 6 | AI Platform v1 | L | Not started |
| 7 | Wholesale/B2B, Omnichannel | L | Not started |
| 8 | Vertical expansion (grocery, pharmacy, apparel, ...) | Ongoing | Not started |
