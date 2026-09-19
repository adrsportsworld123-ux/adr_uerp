# Phase 0 / Phase 1 — Technical Design: Schema & API Contracts

**Companion file:** `phase0_1_database_schema.sql` (full DDL — tested against a live PostgreSQL 16 instance, including the row-level-security policies, before being handed to you). In this repo, that DDL is `migrations/001_schema.sql` — the two are identical.

---

## 1. Entity Relationship Overview

```mermaid
erDiagram
    MERCHANTS ||--o{ BRANCHES : has
    BRANCHES ||--o{ POS_TERMINALS : has
    MERCHANTS ||--o{ USERS : employs
    USERS }o--o{ ROLES : "assigned via user_roles"
    ROLES }o--o{ PERMISSIONS : "granted via role_permissions"

    MERCHANTS ||--o{ CATEGORIES : defines
    MERCHANTS ||--o{ PRODUCTS : owns
    PRODUCTS ||--o{ PRODUCT_VARIANTS : has
    PRODUCT_VARIANTS ||--o{ BARCODES : has
    PRODUCT_VARIANTS }o--|| TAX_SLABS : "taxed via product"

    BRANCHES ||--o{ SALES_ORDERS : records
    POS_TERMINALS ||--o{ SALES_ORDERS : creates
    USERS ||--o{ SALES_ORDERS : "cashier on"
    CUSTOMERS ||--o{ SALES_ORDERS : "placed by"
    SALES_ORDERS ||--o{ SALES_ORDER_LINES : contains
    PRODUCT_VARIANTS ||--o{ SALES_ORDER_LINES : "sold as"
    SALES_ORDERS ||--o{ PAYMENTS : "paid via"

    BRANCHES ||--o{ STOCK_LEVELS : tracks
    PRODUCT_VARIANTS ||--o{ STOCK_LEVELS : "stocked as"
    PRODUCT_VARIANTS ||--o{ STOCK_RESERVATIONS : "reserved as"
    SALES_ORDERS ||--o{ STOCK_RESERVATIONS : holds
    PRODUCT_VARIANTS ||--o{ STOCK_MOVEMENTS : "moved as"
    PRODUCT_VARIANTS ||--o{ SERIAL_NUMBERS : "tracked as"
```

---

## 2. Key Design Decisions

### 2.1 Multi-tenancy: shared DB + Postgres RLS
Every tenant-scoped table carries `merchant_id`, and Postgres Row-Level Security enforces isolation at the database layer — not just in application code. The Go core sets the tenant context as the *first* statement of every transaction, immediately after validating the JWT — via `set_config()`, not `SET LOCAL ... = $1` directly, because Postgres's `SET` command doesn't accept bind parameters over the extended query protocol (the driver would either reject it or you'd be tempted to string-concatenate the tenant ID in, which is an injection risk for a value that ultimately comes from a JWT claim):

```sql
SELECT set_config('app.tenant_id', $1, true);  -- true = local to this transaction, like SET LOCAL
```

**This was verified against a live Postgres instance**, including the important failure mode: if a request handler ever forgets to set `app.tenant_id`, every RLS-protected query on that connection **errors out** rather than silently returning all tenants' data. That's a deliberate fail-closed property. The Go implementation (`internal/db/db.go`) makes this structurally impossible to skip: a single `DB.WithTenant(ctx, tenantID, fn)` wrapper is the only sanctioned way any handler touches a tenant-scoped table, and a request with no tenant context gets an error, never a row — confirmed with a cross-tenant negative test during implementation, not just asserted.

**A related gap found while implementing the login handler:** the original schema had no way to resolve *which* tenant a login belongs to before querying for the user, since `users.email` is only unique per-merchant, not globally. Fixed by adding `merchants.code` (a short, human-typeable tenant key) — login now resolves the merchant by code first, then runs the user lookup inside that tenant's `WithTenant` context. This is reflected in the current `migrations/001_schema.sql`.

### 2.2 Money as `NUMERIC`, never `FLOAT`
All prices/totals are `NUMERIC(14,2)` — floating point has no place anywhere near GST calculations or ledger entries; rounding drift there is a compliance and trust problem, not a cosmetic one.

### 2.3 Stock reservation: `stock_levels` (aggregate) + `stock_reservations` (individual holds) + `stock_movements` (append-only ledger)
Three tables, not one, because they answer different questions:
- `stock_levels` — "how much is available right now" (the fast read path for the POS scan-to-cart flow)
- `stock_reservations` — "what's currently held in someone's cart," with a hard `expires_at` (15 minutes per the FRD) that a background sweeper job releases
- `stock_movements` — the immutable audit trail of every change, which `stock_levels` is really just a materialized, `version`-guarded cache of

The `version` column on `stock_levels` is your optimistic-locking token: a reservation attempt reads the current version, and the `UPDATE` includes `WHERE version = $read_version`, retrying on conflict rather than blocking. Under real concurrent-cart load this scales far better than row locks.

### 2.4 Sales order lifecycle: `cart → finalized → voided/refunded`
A `sales_orders` row is created the moment a cashier starts a transaction (status `cart`), not just at checkout. This matters for two things in your FRD: bill modification (pre-finalization edits are just updates to a `cart`-status order) and offline resilience (if the POS goes offline mid-sale, the cart already exists locally and syncs once connectivity returns).

### 2.5 Idempotency is designed in from the start, not bolted on
`sales_orders.idempotency_key` (client-generated, unique per merchant) exists specifically because the POS is offline-first: a device might retry a checkout call after a dropped connection without knowing whether the first attempt actually landed. The server treats a duplicate idempotency key as "already done" and returns the original result rather than double-charging or double-decrementing stock. `device_created_at` vs `synced_at` are kept separate so you can always tell the true order of events on the device versus when the server actually heard about it — essential for correct conflict resolution during sync.

### 2.6 Variants over a rigid size/color schema
`product_variants.attribute_combo` is JSONB (`{"Size":"M","Color":"Red"}`) rather than fixed `size`/`color` columns, because your BRS's whole premise is configurable masters across industries — jewelry needs purity/weight, electronics needs wattage/warranty period, sports needs size/color. The `attributes`/`attribute_values` tables define what's configurable per merchant; `attribute_combo` stores the actual combination per SKU. This is the concrete mechanism behind "configuration, not industry-specific code."

---

## 3. API Contracts (Phase 0/1 scope)

**Conventions:**
- Base path: `/api/v1`
- Auth: `Authorization: Bearer <jwt>` on every endpoint except `/auth/login` and `/auth/pin-login`. The JWT carries `merchant_id`, `branch_id` (nullable), `user_id`, and `roles` as claims.
- Error shape (consistent across all endpoints):
```json
{ "error": { "code": "STOCK_UNAVAILABLE", "message": "Requested quantity exceeds available stock", "details": {} } }
```
- All monetary fields are strings in JSON (e.g., `"149.00"`), not floats — mirrors the DB's `NUMERIC` choice and avoids client-side floating-point rounding.

**Error code reference (every code actually in use as of this build — kept in sync with the code, not aspirational):**

| Code | HTTP status | Where | Meaning |
|---|---|---|---|
| `INVALID_REQUEST` | 400 | any handler | Body didn't parse, or a required field was missing/invalid |
| `MISSING_TOKEN` | 401 | any protected endpoint | No `Authorization: Bearer` header present |
| `INVALID_TOKEN` | 401 | any protected endpoint | Token present but invalid, malformed, or expired |
| `INVALID_CREDENTIALS` | 401 | `/auth/login` | Merchant code, email, or password didn't match (never says which, deliberately) |
| `ACCOUNT_INACTIVE` | 403 | `/auth/login` | User row exists but `status != 'active'` |
| `ACCOUNT_LOCKED` | 403 | `/auth/login` | `locked_until` is in the future (lockout *checking* is live; lockout *triggering* on repeated failures is not yet wired — see §4/roadmap) |
| `PRODUCT_NOT_FOUND` | 404 | `GET /products/barcode/{code}` | No barcode row matches |
| `NOT_FOUND` | 404 | sales endpoints, `/dev/set-password` | Order, line, or (merchant_code, email) pair doesn't resolve |
| `ORDER_NOT_EDITABLE` | 409 | add-line, delete-line, discounts, checkout | Order isn't in `cart` status anymore (already finalized/voided) |
| `STOCK_UNAVAILABLE` | 409 | add-line, checkout | Requested quantity exceeds what's available, or stock changed since the item was added (surfaced, never silently oversold) |
| `PAYMENT_MISMATCH` | 400 | checkout | Sum of `payments[].amount` doesn't cover `grand_total` |
| `DISCOUNT_NOT_AUTHORIZED` | 403 | `POST /sales/orders/{id}/discounts` | Requested discount % exceeds what the presented `authorized_by`/`authorized_pin` permits for their role tier |
| `DEVICE_MISMATCH` | 403 | `POST /auth/pin-login` | Terminal is already device-bound and the presented `device_fingerprint` doesn't match |
| `TERMINAL_UNAVAILABLE` | 403 | `POST /auth/pin-login` | `pos_terminals.status != 'active'` |
| `FORBIDDEN` | 403 | `POST /inventory/adjustments` | Caller's role isn't Branch Manager or Merchant Admin |
| `INTERNAL_ERROR` | 500 | any handler | Unexpected failure (DB error, etc.) — message is intentionally generic; check server logs for detail |

### 3.1 Auth

| Method & Path | Purpose |
|---|---|
| `POST /auth/login` | `{ merchant_code, email, password }` → `{ access_token, refresh_token, expires_at, expires_in, user_id, roles }` — **as actually shipped**, flatter than originally sketched (`user_id`/`roles` fields rather than a nested `user` object; kept flat to avoid a client-breaking reshape once real clients existed). `expires_in` (seconds) reflects the role-tiered TTL below, not a fixed value. |
| `POST /auth/pin-login` | `{ pos_terminal_id, employee_code, pin, device_fingerprint }` → same token shape. Device binding: if the terminal already has a `device_fingerprint` on file, the request's must match (`403 DEVICE_MISMATCH`); if unbound, the first successful PIN login binds it (only on success, never on a failed attempt). |
| `POST /auth/refresh` | `{ refresh_token }` → new token shape (same as login). Rotates the refresh token on every use — the old one is revoked, a new one issued — so a stolen-and-reused token is detectable (its next refresh attempt fails, already revoked). |
| `POST /auth/logout` | `{ refresh_token }`, authenticated — revokes the presented refresh token. Requires the token's owning user to match the caller's access-token claims (defense in depth beyond the refresh token itself). |

**Session timeout tiers** (`internal/authn/session_tiers.go`), per `pos_frd_complete.md`'s "Timeout (Recommended)": role name (case-insensitive) → access-token TTL — `POS User` 15 min, `Branch Manager` 30 min, `Merchant Admin` 60 min, unrecognized role 15 min (the safe default, never the longer 24h ceiling `TokenIssuer.maxAccessTTL` still allows as an operator-configured hard cap). A user with multiple roles gets the longest matching tier. This replaces the flat 24h token every login used to issue regardless of role — found during the Phase 1 gap analysis (the FRD's tiering was never wired up).

### 3.2 Catalog

| Method & Path | Purpose |
|---|---|
| `GET /products?category_id=&brand_id=&q=&page=&limit=` | Paginated catalog browse (admin/back-office use; POS uses barcode/search below) |
| `GET /products/{id}` | Full product + variants |
| `GET /products/barcode/{code}` | **The POS scan endpoint.** Resolves a scanned barcode straight to `{ product, variant, current_price, tax }` in one call — this is the ≤6-second flow's first hop, so it's a single indexed lookup, not a join-heavy query. |
| `POST /products` | Create product + initial variant(s) (admin) |
| `PATCH /products/{id}` | Update product/variant fields |

### 3.3 Inventory

| Method & Path | Purpose |
|---|---|
| `GET /inventory?branch_id=&variant_id=` | `{ on_hand, reserved, available }` — **built** (`internal/inventory/handlers.go`) |
| `POST /inventory/reservations` | **Not built as a standalone endpoint.** `POST /sales/orders/{id}/lines` reserves stock as part of adding a cart line (see §3.4) — that's the only reservation path Phase 1 actually needed. A standalone reservation endpoint (for a use case outside the cart flow) is still open if one turns out to be needed. |
| `DELETE /inventory/reservations/{id}` | **Not built as a standalone endpoint** — same reasoning; `DELETE /sales/orders/{id}/lines/{line_id}` (§3.4) releases a line's reservation as part of removing it from the cart. |
| `POST /inventory/adjustments` | `{ variant_id, branch_id, quantity_delta, reason }` — **built.** Writes a `stock_movements` row and an `audit_logs` row (before/after `on_hand`). Authorization: requires the caller to hold `Branch Manager` or `Merchant Admin` (checked by role name in `internal/inventory/handlers.go`) — a simpler interim gate than the schema's `permissions`/`role_permissions` model, which no handler in this codebase enforces yet (see §6.3). |

### 3.4 Sales (the core POS transaction flow)

| Method & Path | Purpose |
|---|---|
| `POST /sales/orders` | Opens a new cart. Body: `{ branch_id, pos_terminal_id, idempotency_key }` → `{ order_id, status: "cart" }` |
| `POST /sales/orders/{id}/lines` | `{ variant_id, quantity }` — adds a line, **automatically creates a stock reservation** in the same call |
| `PATCH /sales/orders/{id}/lines/{line_id}` | Update quantity/discount on a line (pre-finalization edit) — **not built.** `POST .../discounts` covers order-level discount; a quantity-edit-in-place is still open (today the workaround is delete + re-add). |
| `DELETE /sales/orders/{id}/lines/{line_id}` | Remove a line, releases its reservation — **built.** Matches the active reservation by (order, variant, quantity) since there's no direct FK from a line to its reservation — a known simplification if the same variant is ever added as two separate lines in one cart (see the handler's doc comment). |
| `POST /sales/orders/{id}/customer` | Attach `{ customer_id }` or inline walk-in details — **not built.** |
| `POST /sales/orders/{id}/discounts` | `{ type: "manual"|"coupon", value, authorized_by, authorized_pin, reason }` — **built** (`internal/sales/discounts.go`). Enforces the FRD's tiers server-side: 0-5% no approval, 5-15% needs a Branch Manager's PIN, 15-25% a Merchant Admin's PIN. The FRD specifies OTP for the top tier; there's no notification channel yet to deliver one (Phase 3 territory), so PIN verification is used for both approval tiers as a documented, equivalent-strength substitute. Discount is distributed proportionally across existing lines and applied **after** tax_amount was already computed (a post-tax discount, not a GST-taxable-value reduction) — a known simplification, not a blocker for the authorization mechanism itself. |
| `POST /sales/orders/{id}/checkout` | `{ payments: [{method, amount}] }` — **the critical transaction, built.** Validates payment total ≥ grand total, converts reservations to a confirmed `stock_movements` sale entry, decrements `stock_levels`, sets `status = finalized`. Idempotent **by order status, not a request-level `idempotency_key`**: a `cart`-status order accepts checkout once; a `finalized` order returns its existing result on replay rather than reprocessing. (The `idempotency_key` shown in earlier drafts of this contract is what `POST /sales/orders` uses to dedupe the *open-cart* call, not checkout itself — checkout doesn't need its own key because the order's status transition already makes it safe to retry.) |
| `GET /sales/orders/{id}` | Full order detail — **closed.** `loadOrder()` now joins `sales_order_lines` + `product_variants` (name/sku) into the response's `lines` array, alongside the existing order-level aggregates. The Flutter client no longer tracks cart lines locally per-device as a result. |
| `GET /sales/orders/{id}/receipt` | Print-ready receipt payload — **not built** (depends on the still-open barcode/label/printer integration item). |
| `POST /sales/orders/{id}/void` | Reverses a finalized order; requires manager authorization + `reason` — **not built.** |

### 3.5 Sync (offline-first — the piece that makes the FRD's core promise real)

| Method & Path | Purpose |
|---|---|
| `POST /sync/push` | Batch upload from a POS device that was offline: an array of orders/payments created locally, each carrying its own `idempotency_key` and `device_created_at`. Server processes each independently; a duplicate `idempotency_key` is a no-op, not an error — devices should always be able to retry a batch safely. |
| `GET /sync/pull?since=<timestamp>&branch_id=` | Incremental catalog, price, and stock-level changes for the device's local cache, so a POS that's been offline for hours can catch up without re-downloading the whole catalog |

**Conflict rule for Phase 1:** last-writer-wins on `stock_levels` isn't safe (two offline devices could both think 1 unit is available). Instead, `/sync/push` re-runs the same reservation check server-side that `/inventory/reservations` would: if a synced sale would oversell, it's accepted (the sale already physically happened at the register) but flags `stock_levels.on_hand` negative and raises a `NEGATIVE_STOCK` alert for manual reconciliation — never silently reject a sale that already happened in the real world.

### 3.6 Dev-only tooling (not part of the product API surface — see §5.3)

| Method & Path | Purpose |
|---|---|
| `POST /dev/hash-password` | `{ password }` → `{ hash }`. Pure bcrypt computation, no DB access. Only registered when `DEV_AUTH_TOOLS_ENABLED=true`. |
| `POST /dev/set-password` | `{ merchant_code, email, new_password }` → `{ status, user_id, email }`. Sets one user's password directly. Only registered when `DEV_AUTH_TOOLS_ENABLED=true` — **must never be true outside a local/dev environment** (see §5.3 for why). |

### 3.7 Reports (added during the Phase 1 hardening pass — not in this doc's original scope)

| Method & Path | Purpose |
|---|---|
| `GET /reports/daily-sales?branch_id=&date=` | Order count + subtotal/discount/tax/grand totals for finalized orders on that branch/date, plus a per-payment-method breakdown. |
| `GET /reports/stock-summary?branch_id=` | `on_hand`/`reserved`/`available` per variant for a branch, joined with product name/sku. |
| `GET /reports/eod-cash?branch_id=&date=` | Cash-method payment total + count for finalized orders on that branch/date — the roadmap's "EOD cash reconciliation." |

Follows the same conventions as everything else (`WithTenant`, `{"error":{...}}` shape, `NUMERIC` as string). Not in §3's original contract table since "Basic reports" was scoped at the roadmap level, not endpoint-by-endpoint, before this pass.

---

## 4. What Phase 0 concretely delivers against this design

1. Run `migrations/001_schema.sql` against your local Postgres (already verified to execute cleanly, RLS included).
2. Go core: a thin `db` package implementing the `WithTenant(ctx, tenantID, fn)` wrapper described in §2.1, a real `/auth/login` handler, and one real business endpoint — `GET /products/barcode/{code}` — since it's simple, exercises the full tenant/RLS path, and is genuinely useful in Phase 1.
3. Flutter app: login screen → barcode scan screen hitting those two real endpoints.
4. This gives you an honest walking skeleton: real auth, real tenant isolation, real data — just one thin vertical slice, before building out the rest of §3.

**Status: built — including the full cart → checkout flow.** The Go service described above (`erp-core-go`) exists, with its own README covering exactly what was and wasn't possible to verify in the network-restricted sandbox it was originally built in (short version: every SQL statement it runs — auth, barcode lookup, and the entire `sales` package's create-order/add-line/checkout/get-order flow — was verified against a live, seeded, RLS-enabled Postgres instance, including the optimistic-locking stock-reservation retry logic and the idempotent-checkout replay path; the Go code itself was type-checked with `go build`/`go vet` against faithful local stubs of its four dependencies, since the real module proxy was blocked by that sandbox's network policy). **Now that this repo has moved to a machine with normal network access, `go mod tidy && go build ./... && go vet ./... && go test ./...` should be run for real as the first thing — that's the one verification step the original sandbox genuinely could not do.** Several real bugs were caught and fixed during the original build: a missing `context` import, `NUMERIC`-to-`string` scans that needed explicit `::text` casts, and a potential panic slicing a malformed `branch_id`.

Not yet built: `DELETE` on a cart line, manual/authorized discounts, the reservation-expiry sweeper, and the `/sync/push`/`/sync/pull` endpoints — the API contracts for these are already specified in §3 above, ready to implement against the same `WithTenant`/optimistic-locking patterns. See `phased_roadmap.md`'s Phase 1 status for the full open list, in dependency order.

## 5. Two real bugs found during the user's own first login attempt (post-Phase-0 hardening)

Both were caught from the user's own testing against the running service, not from sandbox verification — which is exactly why "verify before proceeding" is the right instinct to keep going forward in Claude Code too. Both are fixed and verified.

**5.1 CORS blocked every browser-based login (Flutter web) before it reached the server.** The router had no CORS middleware, so a browser's preflight `OPTIONS /api/v1/auth/login` fell through to chi's default `405 Method Not Allowed`. It is a browser-only mechanism (same-origin policy), so it never would have affected a native Android/iOS/desktop build, only `flutter run -d chrome`. Fixed with `internal/httpserver/cors.go`, wired first in the router. Verified with a real, dependency-free test (`internal/httpserver/cors_test.go`) that first reproduces the exact 405 against a bare `net/http` mux, then confirms the fix turns the preflight into `204` with correct headers while a real `POST` still reaches the handler. `Access-Control-Allow-Origin: *` is intentionally permissive for local dev only — safe here because this API is Bearer-token, not cookie, authenticated — and should be tightened to real origins before any non-local deployment.

**5.2 There is no working "default password."** `migrations/002_seed.sql`'s `password_hash` for `ravi@acme-sports.test` was always a placeholder string, not a real bcrypt hash of any password. Generating a real hash originally required a working bcrypt implementation the sandbox couldn't obtain through any legitimate channel — superseded by §5.3 below, which is the actual fix to use now.

**5.3 Follow-up: a public, no-auth password-generation/-setting API, added on request, gated behind a flag.** Built as two endpoints (`internal/authn/dev_handlers.go`, contract in §3.6): `POST /dev/hash-password` (stateless — hashes a string you already know, touches no account) and `POST /dev/set-password` (resolves `merchant_code` → tenant, then updates that user's `password_hash` inside the same `WithTenant`/RLS-scoped transaction every other write in this codebase uses).

`set-password` with no auth is a real account-takeover primitive once this has real tenants online — anyone who can reach it and guess a `merchant_code`+`email` can silently take over that account. Both endpoints are compiled in but only *registered* when the service starts with `DEV_AUTH_TOOLS_ENABLED=true`; if that flag is unset or `false`, the routes don't exist at all (a plain `404`, not "exists but refuses"). The shipped `docker-compose.yml` sets it `true`, since that file is your local environment by definition — **it must stay `false`/unset in any config that isn't your own machine.** The real fix for "users forgot their password" once this has real merchants — an emailed, single-use, time-limited reset token — is a Phase 1+ item, not replaced by this.

Verified against a live, seeded Postgres instance (`erp_dev`) by hand-replaying the exact statement sequence `SetPasswordHandler` runs in `psql`: confirmed the update persists and is readable back inside the right tenant context; confirmed an unknown email returns zero rows (maps to the handler's `404 NOT_FOUND`, not an error); and — the important negative case — confirmed that setting the *wrong* tenant context before the same `UPDATE ... WHERE email = ...` against a real, existing email still returns zero rows, i.e. RLS scopes this endpoint's write even if merchant-resolution were ever bypassed by a future bug. The whole module (including the two new handlers) built, `go vet`-ed, and passed `go test ./...` clean against the local dependency stubs — the real build is the first thing to confirm now that this has moved to a machine with normal network access.

---

## 6. Phase 1 hardening pass — what closed, and one finding more serious than anything it was looking for

Triggered by a gap analysis comparing this repo, `erp-pos-flutter`, and the docs against each other. `go build`/`go vet`/`go test` and `flutter analyze`/`flutter test` all ran clean against real dependencies for the first time (previous verification, described above, was necessarily stub-based or manual-SQL-replay). Every item below was also exercised live against this repo's own `docker-compose.yml` stack, not just compiled.

### 6.1 The critical finding: the API was connecting to Postgres as a superuser, which silently disabled every RLS policy in the system

`docker-compose.yml`'s `postgres` service sets `POSTGRES_USER=app_user`; the official `postgres` Docker image grants that env-var-created role `SUPERUSER`. The `api` service then connected to Postgres **as that same `app_user`**. Postgres superusers (and any role with `BYPASSRLS`) unconditionally bypass row-level security — no `CREATE POLICY`, no `FORCE ROW LEVEL SECURITY` changes that. Confirmed directly against the running stack:

```sql
SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = 'app_user';  -- t, t

-- with app.tenant_id set to a tenant that owns no rows at all:
SELECT count(*) FROM sales_orders;  -- returned every tenant's rows, not 0
```

This means the RLS "fail-closed" property §2.1 above documents as verified — and that `CLAUDE.md` calls a non-negotiable, verified guarantee — had never actually been enforced by the database for any request served through this docker-compose stack, for any table, since Phase 0. Nothing about `db.WithTenant`, `set_config`, or any policy definition was wrong; the role the entire story assumed was subject to RLS never was. This was not caught earlier because the original §2.1/§5.3 verification narrative ran its negative tests through the same superuser role — a negative test that can't fail regardless of whether RLS works is not really testing RLS.

**Fixed** in `migrations/004_least_privilege_app_role.sql`: a dedicated `erp_app` role (`NOSUPERUSER NOBYPASSRLS`, and — just as important — not the owner of any table, since non-owners are unconditionally subject to RLS the moment it's enabled, table owners are not) with `SELECT/INSERT/UPDATE/DELETE` granted explicitly, plus `ALTER DEFAULT PRIVILEGES` so future migrations' tables/sequences stay usable by it automatically. `docker-compose.yml`'s `api` service and `internal/config/config.go`'s default `DATABASE_DSN` now both point at `erp_app`, not `app_user` — `app_user` remains only the schema-owning role migrations run as. Re-verified the exact same negative test as `erp_app` post-fix: `sales_orders`, `sales_order_lines`, `payments`, `sales_order_discounts`, and `users` all correctly returned 0 rows for a tenant that owns none, while the correct tenant's data still resolved normally end-to-end through the running API.

**If this repo is ever deployed against a managed Postgres (RDS, Cloud SQL, etc.), the same check needs to be re-run there**: confirm whatever role the service authenticates as is not the provider's default master/admin role, which often carries superuser-equivalent privileges for the same reason `app_user` did here.

### 6.2 A second, smaller latent bug found while building on top of this: `audit_logs` could never actually be written to

`audit_logs` (§ SECTION 5 of `001_schema.sql`) is `PARTITION BY RANGE (created_at)` but the original migration only left a *commented-out example* of creating a partition — no partition, including a default, was ever actually created. Every `INSERT INTO audit_logs` was therefore guaranteed to fail with `no partition of relation "audit_logs" found for row`, from the moment the table was created. This went unnoticed through Phase 0/1 because nothing ever wrote to `audit_logs` until this hardening pass's `POST /inventory/adjustments` handler tried to. Fixed in the same migration: a `DEFAULT` partition (so writes never fail even if a monthly-partition job falls behind) plus an explicit current-month partition. Future months still need the scheduled-job/`pg_partman` automation the original comment already called for — this fix makes writes correct now, it doesn't add that automation.

### 6.2b A third bug, found immediately after shipping 6.1/6.2: a migration-ordering mistake that broke fresh installs

Migration 003 originally added `users.employee_code` via `ALTER TABLE`. The seed data in `002_seed.sql` was updated in the same pass to insert rows using that column. On the volume this was developed and tested against, 003 had already been applied by hand before the seed data was touched, so the mistake was invisible there. On a genuinely fresh volume, `docker-entrypoint-initdb.d` runs every file in this directory strictly in filename order — `001`, then `002_seed.sql`, then `003_hardening.sql` — so `002`'s `INSERT INTO users` referencing `employee_code` ran *before* `003` had a chance to add that column, failed immediately, and silently aborted the rest of that seed script (everything after `users` in that file — products, variants, stock — never ran either). Surfaced as `docker compose down -v && up` leaving `users` (and everything seeded after it) empty, discovered while diagnosing an unrelated `erp_app` role-missing report from the same fresh-install path.

**Fixed:** `employee_code` now lives directly in `001_schema.sql`'s `CREATE TABLE users`, not a later migration — see `003_hardening.sql`'s header note for the standing rule this sets: a column a seed row needs belongs in `001_schema.sql`, never a later-numbered migration, however related it seems. Re-verified with the same `down -v && up -d --build` flow: `erp_app` created correctly, `employee_code` populated for all three seed users, and a full login → barcode-scan round trip succeeded against the freshly-initialized stack.

### 6.3 Closed this pass

- **RLS extended** to `sales_order_lines` and `payments` (previously enforced only by application-level joins through `sales_orders` — see the note that used to be at the bottom of `001_schema.sql`), plus the new `sales_order_discounts` table. `pos_terminals` lost its RLS policy on purpose, for the same bootstrap reason `merchants` never had one — see `migrations/003_hardening.sql`'s header comment.
- **Failed-login lockout** now actually triggers (`internal/authn/lockout.go`) — the gap flagged by the `NOTE:` comment that used to be in `handlers.go`.
- **Session timeout tiers** (§3.1) replace the flat 24h token.
- **`/auth/refresh`, `/auth/logout`, `/auth/pin-login`** (§3.1) — refresh-token issuance/rotation/revocation, and PIN quick-login with device binding.
- **`DELETE /sales/orders/{id}/lines/{line_id}`, `POST /sales/orders/{id}/discounts`** (§3.4).
- **`GET /sales/orders/{id}` full line detail** (§3.4) — the gap this doc used to describe under "not yet true to this doc" is closed; the Flutter client's `CartLineDisplay` local-tracking workaround was removed accordingly.
- **The 15-minute reservation-expiry sweeper** (`internal/sales/sweeper.go`) — verified live: forced a reservation's `expires_at` into the past, confirmed the sweeper (ticking every 1 minute from `main.go`) released it and marked it `expired` within one tick.
- **`GET /inventory`, `POST /inventory/adjustments`** (§3.3) — the latter gated on `Branch Manager`/`Merchant Admin` as an interim role-name check, not the schema's still-unenforced `permissions`/`role_permissions` model (a general permission-code middleware is a bigger piece of work than this one endpoint needed — see the handler's doc comment).
- **Basic reports** (§3.7).
- **`internal/authn/roles.go`'s `HasRole`** — the one shared, case-insensitive role check other packages (discount authorization, inventory-adjustment permission) use, instead of each re-implementing its own.

### 6.4 Still open

- **`PATCH .../lines/{line_id}`, `POST .../customer`, `GET .../receipt`, `POST .../void`, standalone `POST/DELETE /inventory/reservations`** — never built; today's workarounds (delete+re-add a line, no dedicated customer-attach/receipt/void path) are noted next to each in §3 above.
- **`/sync/push`/`/sync/pull` and any Flutter-side offline store** — this pass deliberately stopped short of it. It's the single biggest remaining gap versus the "offline-capable" Phase 1 Definition of Done, and needs a real decision (which local-storage approach on the Flutter side) before implementation starts, not just more server-side plumbing.
- **General permission-code enforcement** (`permissions`/`role_permissions`) — currently only two handlers (`AdjustStock`, `ApplyDiscount`'s authorizer check) do any role-based gating at all, and both do it by role name, not by the schema's permission-code model.
- **Discount-before-tax GST treatment** — `POST .../discounts` applies as a post-tax reduction today (see §3.4); a fully GST-accurate implementation needs the tax rate, not just the resolved `tax_amount`, available per line.
- **PIN-login and delete-line UI in the Flutter app** — the backend supports both; `erp-pos-flutter` only has UI for delete-line (added this pass), not PIN quick-login.
