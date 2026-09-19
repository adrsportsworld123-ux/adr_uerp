# erp-core-go — context for Claude Code

This file is read automatically by Claude Code at the start of every session in
this repo. It exists so you don't have to re-explain this project from
scratch — read it, then read `docs/phase0_1_design.md` for the technical
detail before making changes.

## What this is

The Go transactional core of a configurable, AI-powered, cloud-native
Universal Retail & Wholesale ERP (multi-industry: sports, grocery, pharmacy,
chemicals, steel, jewelry, apparel, electronics, ...) built through
configurable masters and business rules, not industry-specific code. This
repo owns the performance-critical transactional path: auth, sales/billing,
inventory/stock reservation, and (later) offline sync and notification
dispatch. See `docs/tech_stack_decision.md` §3.1 for why Go owns this slice
specifically, and `docs/tech_stack_decision.md` §10 for how this repo fits
alongside the other three planned repos (`erp-business-py`, `erp-web-admin`,
`erp-pos-flutter` — the last one is a sibling repo, see `../erp-pos-flutter`).

**Read `docs/phase0_1_design.md` before writing code that touches the
schema, auth, or sales flow — it is the single most relevant document in
this repo and stays the source of truth as new work lands.** `docs/` also
has `tech_stack_decision.md` (full stack rationale) and `phased_roadmap.md`
(phase-by-phase plan, including current status per phase).

## Non-negotiable constraints — do not silently relitigate these

- **Auth stays custom-built.** The user explicitly decided against
  Keycloak/a managed IdP despite the higher solo-builder effort and risk:
  *"I agree your flagged point effort and risk but i need all this in my
  control in my service so keep this as same."* The OIDC-standard interface
  boundary (every other service talks to auth only via JWT
  validation/standard endpoints, never internal function calls) is what
  keeps a future swap *possible*, not a signal to push for one now. Don't
  re-raise this unless the user brings it up.
- **Multi-tenancy is Postgres RLS, shared DB, `merchant_id` on every
  tenant-scoped table.** Never touch a tenant-scoped table outside
  `db.WithTenant(ctx, tenantID, fn)` — see `internal/db/db.go`'s comment for
  why, and `docs/phase0_1_design.md` §2.1 for the verified fail-closed
  property (no tenant context set → query errors, never leaks cross-tenant
  data).
- **Money is always `NUMERIC(14,2)` in Postgres**, never `FLOAT`/`float64`
  in a schema column. Go-side arithmetic uses `float64` in a couple of
  documented spots where the result lands back in a `NUMERIC` column and
  rounds cleanly — that's an accepted, already-reviewed tradeoff, not an
  invitation to introduce float64 storage anywhere new.
- **Stock reservation is optimistic-locked** via `stock_levels.version` —
  see `internal/sales/reservation.go`. Don't replace this with row locks
  without a real reason; it was chosen for concurrent-cart scale.
- **Every write the client might retry needs an idempotency key** —
  `sales_orders.idempotency_key` is the existing pattern (`ON CONFLICT ...
  DO UPDATE ... RETURNING`, and a status-based short-circuit at checkout).
  Follow it for new mutating endpoints in this offline-first system.

## Verification status — read this before trusting anything blindly

This repo was originally built in a sandboxed environment with **no
network access** to `proxy.golang.org`, so real dependencies (`pgx`, `chi`,
`golang-jwt/jwt`, `golang.org/x/crypto/bcrypt`) could never be fetched
there. Everything was verified as rigorously as that constraint allowed:

- **All SQL was verified against a live, seeded PostgreSQL 16 instance** —
  including RLS cross-tenant isolation, the optimistic-locking retry logic
  (success / insufficient-stock / stale-version cases), the idempotent
  checkout replay path, and (most recently) the two `/dev/*` password
  endpoints' exact statement sequence, including a deliberate wrong-tenant
  negative test.
- **Go code was type-checked** (`go build`/`go vet`) against faithful local
  stub packages standing in for the four real dependencies — this caught
  several real bugs (a missing import, missing `::text` casts on `NUMERIC`
  scans, a potential slice-bounds panic) but is not the same as a real
  build.
- **What was never verified: a real `go build`/`go test` against the actual
  dependencies, and Flutter/Dart entirely** (different repo, same
  constraint).

**Now that this repo is on a machine with normal network access, the very
first thing to do is the real version of that check:**

```bash
go mod tidy
go build ./...
go vet ./...
go test ./...
```

Treat any failure here as a real bug to fix, not a stub-fidelity artifact —
but also don't be surprised if everything passes cleanly; the stub
type-checking was faithful to each package's real exported API surface.

## Current state (see `docs/phased_roadmap.md` for the full phase list)

**Built and verified (Phase 0 done, Phase 1 in progress):**
- `POST /auth/login` — merchant_code → tenant resolution → RLS-scoped user
  lookup → bcrypt verify → JWT issue
- `GET /products/barcode/{code}` — the POS scan endpoint
- Full cart lifecycle: `POST /sales/orders` (open cart, idempotent) →
  `POST /sales/orders/{id}/lines` (add line + auto-reserve stock) →
  `POST /sales/orders/{id}/checkout` (idempotent, validates payment total,
  finalizes) → `GET /sales/orders/{id}` (aggregates only — see gap below)
- CORS middleware (`internal/httpserver/cors.go`) — needed because a
  browser's preflight `OPTIONS` was hitting chi's default 405 before any
  real request landed; native mobile/desktop clients were never affected
- Two dev-only, unauthenticated password endpoints
  (`internal/authn/dev_handlers.go`), gated behind
  `DEV_AUTH_TOOLS_ENABLED` (default `false`; `true` in the shipped
  `docker-compose.yml` only) — **must stay `false` outside local dev**,
  see `docs/phase0_1_design.md` §5.3 for the full reasoning

**Known gap, not yet closed:** `GET /sales/orders/{id}`'s `loadOrder()`
returns order-level aggregates only, not the joined `sales_order_lines`.
The Flutter client currently works around this by tracking added lines
locally per-device (see `../erp-pos-flutter/lib/state/session.dart`'s
`CartLineDisplay` doc comment) — fine for one terminal, not for a second
device reloading someone else's cart. Fix: join `sales_order_lines` (+
`product_variants` for name/sku) into `loadOrder`, or add a dedicated
`GET /sales/orders/{id}/lines`.

**Flagged, deliberately not yet done — see the `NOTE:` comment in
`internal/authn/handlers.go`:** failed-login attempts aren't incremented on
a wrong password, so the 5-attempt/15-minute lockout the schema and login
handler are already built to check never actually triggers. Don't ship
Phase 1 without this — it's a straightforward `UPDATE` inside the existing
`WithTenant` pattern.

**Next items, in dependency order** (full detail in
`docs/phased_roadmap.md`'s Phase 1 status section):
1. `DELETE /sales/orders/{id}/lines/{line_id}`
2. Failed-login lockout enforcement (see above)
3. `GET /sales/orders/{id}` full line detail
4. Manual/authorized discounts with tiered authorization
5. 15-minute reservation-expiry sweeper
6. Barcode & label generation, printer integration
7. PIN quick-login + device binding
8. Basic daily/stock/EOD reports

## Repo layout

```
cmd/api/main.go              entry point: config → db → auth → router
internal/config               env-var configuration
internal/db                   pgxpool wrapper + WithTenant (the RLS pattern)
internal/authn                JWT issue/verify, login handler, auth middleware, dev-only password tooling
internal/catalog               barcode-scan handler
internal/sales                 cart → add line → checkout, optimistic-locking stock reservation
internal/httpserver            chi router wiring, CORS middleware
internal/httpx                 shared JSON response/error helpers
migrations/001_schema.sql      full Phase 0/1 DDL (verified against live Postgres)
migrations/002_seed.sql        one merchant/branch/user/product to develop against
docs/                          copies of the project's design docs (tech stack, roadmap, schema/API design)
docker-compose.yml             postgres + redis + this service
```

## Working conventions to keep

- `NUMERIC` columns need an explicit `::text` cast in SQL before scanning
  into a Go `string` — pgx doesn't do this conversion implicitly.
- Every handler that returns an error uses `httpx.Error`/`httpx.JSON`
  (`internal/httpx/respond.go`) so the error shape stays consistent:
  `{ "error": { "code", "message", "details" } }`.
- Run `gofmt -l .` (or `-w`) before considering anything done — this
  codebase has been kept `gofmt`-clean throughout.
- When you add a new tenant-scoped table or query, it goes through
  `DB.WithTenant`, full stop — that's the whole RLS safety property.
- Data access is hand-written SQL via `pgx` directly — no ORM/codegen layer
  yet (`docs/tech_stack_decision.md` §3.1 originally named `GORM`/`sqlc`;
  the as-built note there explains why that was deferred, and flags
  introducing `sqlc` at/before Phase 2 once the query surface grows). Don't
  add an ORM as a drive-by refactor without reading that note first.
- Every API error code actually in use is catalogued in
  `docs/phase0_1_design.md` §3 (just above §3.1) — add new mutating
  endpoints' codes there as you build them, so that table stays the real
  contract instead of drifting from the code.
