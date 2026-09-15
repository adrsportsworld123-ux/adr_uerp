# erp-core-go — Phase 0 / Phase 1 walking skeleton

Real auth (login → JWT), real multi-tenancy (Postgres RLS), and the first
real business endpoint (barcode scan → product/price/tax lookup), wired
together exactly as designed in `phase0_1_design.md`.

## What's here

```
cmd/api/main.go              entry point: wires config → db → auth → router
internal/config               env-var configuration
internal/db                   pgxpool wrapper + WithTenant (the RLS pattern)
internal/authn                JWT issue/verify, login handler, auth middleware
internal/catalog               the barcode-scan handler
internal/sales                 cart → add line → checkout, with optimistic-locking stock reservation
internal/httpserver            chi router wiring, CORS middleware
internal/httpx                 shared JSON response/error helpers
migrations/001_schema.sql      full Phase 0/1 DDL (tested against live Postgres)
migrations/002_seed.sql        one merchant/branch/user/product to develop against
docker-compose.yml             postgres + redis + this service, wired together
Dockerfile                     multi-stage build for the api binary
```

## A note on how this was built and verified

This code was written and reviewed in a sandboxed environment whose network
egress policy blocks `proxy.golang.org` (the Go module proxy) — a
deliberate organizational restriction, not something to route around. That
means **`go build` could not be run against the real dependencies** the way
the SQL schema was run against a real Postgres instance.

To still catch real bugs rather than hand you unverified code, every
external package this code imports (`pgx/v5`, `chi/v5`, `golang-jwt/jwt/v5`,
`golang.org/x/crypto/bcrypt`) was reimplemented as a minimal local stub
matching its real exported API surface, and the whole module was built and
`go vet`-ed against those stubs. This caught several real bugs before you
ever saw this code: a missing `context` import, `NUMERIC` columns being
scanned into Go `string` without the `::text` cast pgx needs to do that
safely, and a potential panic slicing a malformed `branch_id` in the order-
number generator. It does **not** verify runtime behavior against a real
Postgres wire connection — that part is on you to confirm with the steps
below.

What *was* verified against a real, live, RLS-enabled Postgres 16 instance
with seeded data: every SQL statement in `internal/authn/handlers.go`,
`internal/catalog/handlers.go`, and `internal/sales/handlers.go` +
`reservation.go` — including a cross-tenant isolation check, the full
cart → add-line → checkout lifecycle with correct running totals, the
optimistic-locking retry logic for stock reservation (a successful
reservation, an insufficient-stock rejection, and a stale-version
rejection, each producing the exact result the code expects), and the
idempotent-checkout replay path (a second checkout call against an
already-`finalized` order returns the existing result rather than
reprocessing). See `phase0_1_design.md` for the full verification notes.

**Do this before trusting the code beyond "it type-checks":**

```bash
go mod tidy      # fetches the real pgx/chi/jwt/bcrypt versions — needs network
go build ./...
go vet ./...
go run ./cmd/api  # or: docker compose up
```

## Two things you must do before login will work at all

These aren't optional cleanup — without both of these, login fails for
*every* password, silently or with a confusing browser error, no matter
what you type into the UI.

### 1. Replace the seed password hash — now easiest via a built-in endpoint

Two new endpoints exist specifically to remove the need for the external Go
program below: `POST /dev/hash-password` and `POST /dev/set-password`. They
only exist when the server is started with `DEV_AUTH_TOOLS_ENABLED=true`
(the shipped `docker-compose.yml` already sets this) — see
**"Dev-only password endpoints"** further down for the full contract, the
security tradeoffs, and why `set-password` must never be enabled outside
your own machine. If you'd rather not rely on that flag, the manual path
below still works unchanged.

`migrations/002_seed.sql`'s `password_hash` for `ravi@acme-sports.test` is
the literal string `$2a$10$placeholderplaceholderplaceholderplaceholderp`
— not a real bcrypt hash of any password. There is no "default password"
that works against it; bcrypt can't be reverse-engineered into one, and
this sandbox has no working path to *generate* one either (`pip install
bcrypt`, `npm install bcryptjs`, and `apt-get install apache2-utils` were
all attempted and all blocked by the same egress policy that blocks
`proxy.golang.org` — see above). Hand-rolling bcrypt's Blowfish key
schedule myself, unable to verify it against a real implementation, was
rejected on the same "don't ship unverifiable crypto" grounds as
everything else in this note.

Generate a real hash on your own machine (normal network access, so `go
mod tidy` there will work) with a disposable program:

```go
package main

import (
	"fmt"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	h, err := bcrypt.GenerateFromPassword([]byte("Passw0rd!"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(h))
}
```

`go run` it (in any scratch module — `go mod init tmp && go mod tidy && go
run .`), then either apply it straight to your running Postgres:

```sql
UPDATE users SET password_hash = '<paste the hash here>'
WHERE email = 'ravi@acme-sports.test';
```

or edit the value directly in `migrations/002_seed.sql` before your next
`docker compose up` (a fresh volume re-runs the seed file; an existing one
won't, so use the `UPDATE` against a database that already has data in it).

### 2. CORS — only affects browser-based clients (Flutter **web**, not app/desktop)

The router had no CORS middleware, so a browser's preflight `OPTIONS`
request to `/api/v1/auth/login` fell through to chi's default `405 Method
Not Allowed` — before the real `POST` (and your password) was ever sent.
This is exactly the `OPTIONS ... 405` you'd see in Chrome DevTools' Network
tab if you ran `flutter run -d chrome`: not a validation error, not a
wrong-password error — the request never reached the handler at all. A
native Android/iOS/desktop Flutter build never triggers this, because CORS
preflight is a browser same-origin-policy behavior, not a server-side
permission check.

Fixed in `internal/httpserver/cors.go` (wired into the router as the first
middleware in `internal/httpserver/router.go`). It's deliberately
permissive (`Access-Control-Allow-Origin: *`) for local dev — safe here
specifically because this API is Bearer-token authenticated, not
cookie-based, so a wildcard origin doesn't expose credentials the way it
would on a cookie-authenticated API. Tighten it to your real deployed
origin(s) before this leaves local dev.

Verified with a real, dependency-free test at
`internal/httpserver/cors_test.go` that (1) first reproduces the exact 405
against a bare `net/http` mux with only a `POST` route registered —
confirming this is genuinely the bug in the screenshot, not a guess — then
(2) confirms wrapping with `CORS(...)` turns the preflight into a `204`
with the right headers while a real `POST` still reaches the handler and
returns `200`. Run it yourself with `go test ./internal/httpserver/... -v`.

### Dev-only password endpoints (`DEV_AUTH_TOOLS_ENABLED=true`)

Two endpoints, both unauthenticated by design (that's the point — you're
using them precisely because you can't log in yet), implemented in
`internal/authn/dev_handlers.go` and only registered by the router when
`DEV_AUTH_TOOLS_ENABLED=true`. The shipped `docker-compose.yml` sets this,
since that compose file *is* your local dev environment. **Do not set this
env var to `true` anywhere else** — see the "why" below.

**`POST /dev/hash-password`** — pure computation, no database access. Turns
a plaintext password into a bcrypt hash you can paste into SQL yourself.

Request:
```json
{ "password": "Passw0rd!" }
```
Response (`200`):
```json
{ "hash": "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy" }
```
Errors: `400 INVALID_REQUEST` if `password` is missing or over 72 bytes
(bcrypt's own input limit — it silently truncates past that rather than
erroring, so this endpoint rejects it up front instead of quietly hashing
something shorter than you typed).

**`POST /dev/set-password`** — resolves the merchant by `merchant_code`,
hashes `new_password`, and updates that one user's `password_hash` directly
in Postgres, inside the same `WithTenant`/RLS-scoped transaction every
other tenant write in this codebase uses. This is the one-call replacement
for the "generate a hash → `UPDATE` it yourself" dance above.

Request:
```json
{
  "merchant_code": "acme-sports",
  "email": "ravi@acme-sports.test",
  "new_password": "Passw0rd!"
}
```
Response (`200`):
```json
{ "status": "ok", "user_id": "55555555-5555-5555-5555-555555555555", "email": "ravi@acme-sports.test" }
```
Errors: `400 INVALID_REQUEST` (missing field, or `new_password` over 72
bytes); `404 NOT_FOUND` for an unknown `merchant_code`/`email` pair —
deliberately generic, so this endpoint can't itself be used to enumerate
which accounts exist even in a dev environment.

Example, end to end:
```bash
curl -s localhost:8080/dev/set-password \
  -H 'Content-Type: application/json' \
  -d '{"merchant_code":"acme-sports","email":"ravi@acme-sports.test","new_password":"Passw0rd!"}'

curl -s localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"merchant_code":"acme-sports","email":"ravi@acme-sports.test","password":"Passw0rd!"}'
```

**Why `set-password` is gated behind a flag that defaults to `false`, and
must stay `false` anywhere but your own machine:** an endpoint that changes
any known account's password with no proof of who's asking is a textbook
account-takeover primitive — the instant this multi-tenant system has real
merchants and real users, leaving it reachable is a way to hand an attacker
every account on the platform, one `merchant_code`+`email` guess at a time.
It exists at all only because, right now, you have exactly one seeded user
and no external way to set their password. The real, production-safe
version of "I forgot my password" is a Phase 1+ item: an emailed,
single-use, time-limited reset token — not a raw "set this password" call.
`hash-password` is lower-risk (see the code comment — it never touches an
account, at worst it's a CPU-cost/rate-limiting concern) but is gated by
the same flag anyway, since a public bcrypt oracle has no purpose once real
users exist either. Verified end to end against a live, seeded Postgres
instance: the `merchant_code → tenant → UPDATE ... RETURNING id` sequence
this handler runs was replayed by hand in `psql`, confirming it updates the
right row, returns zero rows (not an error) for an unknown email, and —
run with a *wrong* tenant context on purpose — that RLS silently scopes the
`UPDATE` to zero rows even for a real user's email, so this endpoint cannot
cross a tenant boundary even in a bug scenario.

## Running it

```bash
docker compose up --build
```

This starts Postgres (auto-running `migrations/001_schema.sql` then
`002_seed.sql` on first boot), Redis (present for later phases, unused by
Phase 0/1 code yet), and the API on `:8080`.

Then:

```bash
# Login
curl -s localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"merchant_code":"acme-sports","email":"ravi@acme-sports.test","password":"Passw0rd!"}'
# → { "access_token": "...", "user_id": "...", "roles": ["POS User"], ... }

# Scan the seeded barcode (use the access_token from above)
curl -s localhost:8080/api/v1/products/barcode/8901234567890 \
  -H "Authorization: Bearer <access_token>"
# → { "product_name": "SG Cricket Bat", "sku": "SG-BAT-SH", "selling_price": "2299.00", ... }

# Open a cart
curl -s localhost:8080/api/v1/sales/orders -X POST \
  -H "Authorization: Bearer <access_token>" -H 'Content-Type: application/json' \
  -d '{"branch_id":"22222222-2222-2222-2222-222222222222","pos_terminal_id":"33333333-3333-3333-3333-333333333333","idempotency_key":"my-first-cart-001"}'
# → { "order_id": "...", "status": "cart", "grand_total": "0.00", ... }

# Add the scanned item (use the variant_id from the barcode scan, and order_id from above)
curl -s localhost:8080/api/v1/sales/orders/<order_id>/lines -X POST \
  -H "Authorization: Bearer <access_token>" -H 'Content-Type: application/json' \
  -d '{"variant_id":"99999999-9999-9999-9999-999999999999","quantity":1}'
# → { "status": "cart", "subtotal": "2299.00", "tax_total": "413.82", "grand_total": "2712.82" }
# (migrations/002_seed.sql seeds 10 units of this variant in stock so this works out of the box)

# Checkout
curl -s localhost:8080/api/v1/sales/orders/<order_id>/checkout -X POST \
  -H "Authorization: Bearer <access_token>" -H 'Content-Type: application/json' \
  -d '{"payments":[{"method":"cash","amount":2712.82}]}'
# → { "status": "finalized", "grand_total": "2712.82" }
```

## What's deliberately NOT here yet

Per the phased roadmap: `DELETE` on a line (remove + release reservation),
manual/authorized discounts, inventory adjustments, the `/sync/push` and
`/sync/pull` endpoints, the 15-minute reservation-expiry sweeper, and the
failed-login lockout counter (flagged with a `NOTE:` comment in
`authn/handlers.go` — don't ship Phase 1 without it). These are the next
slices to build on this same foundation, following the API contracts
already defined in `phase0_1_design.md` §3.
