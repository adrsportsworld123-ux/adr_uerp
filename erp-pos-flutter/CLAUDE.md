# erp-pos-flutter — context for Claude Code

This file is read automatically by Claude Code at the start of every session
in this repo. Read it, then read `../erp-core-go/docs/phase0_1_design.md`
§3 for the API contracts this app calls against (assumes this repo and
`erp-core-go` are sibling directories, e.g. `D:\projects\erp-core-go` and
`D:\projects\erp-pos-flutter` — if they're laid out differently, the API
contract is still the source of truth, just at a different relative path).

## What this is

The Flutter POS terminal client for a configurable, AI-powered, cloud-native
Universal Retail & Wholesale ERP. Targets Android/iOS/Windows tablets per
`../erp-core-go/docs/tech_stack_decision.md` §4 — offline-first is the
long-term target (local SQLite via Drift, per the tech stack doc), though
Phase 0/1 as built so far talks directly to the live API with no local
persistence yet.

## Current state

**Built:** login screen (`lib/screens/login_screen.dart`) → barcode-scan +
cart screen (`lib/screens/pos_screen.dart`), backed by `AppSession`
(`lib/state/session.dart`, a `ChangeNotifier` — Provider-based state
management) and `ApiClient` (`lib/api/api_client.dart`) hitting
`erp-core-go`'s real endpoints: `POST /auth/login`, `GET
/products/barcode/{code}`, `POST /sales/orders`, `POST
/sales/orders/{id}/lines`, `POST /sales/orders/{id}/checkout`.

**Known gap, worked around deliberately (not silently):** the backend's
`GET /sales/orders/{id}` doesn't yet return line items, only order-level
aggregates (see `../erp-core-go/docs/phase0_1_design.md` §3.4 and §4). This
app compensates with `CartLineDisplay` in `session.dart` — it tracks what
*this device* just added locally. That's fine for a single terminal, but a
second device (or a page refresh against a cart another device opened)
currently has no way to see line detail. Read `CartLineDisplay`'s doc
comment in `session.dart` before changing cart-display logic — the real
fix is on the backend (`erp-core-go`), not a client-side patch.

**Not yet built:** remove-line, discounts, offline local persistence
(Drift/SQLite), receipt printing, PIN quick-login, device binding — see
`../erp-core-go/docs/phased_roadmap.md`'s Phase 1 status for the full list
and dependency order shared across both clients.

## Verification status — read this before trusting anything blindly

This repo was built in a sandboxed environment with **no network access to
the Flutter SDK's package/artifact hosts**, so `flutter analyze`, `flutter
test`, `flutter build`, and `flutter run` could never actually be executed
there. What verification *was* possible: brace-balance and structural
checks across every `.dart` file, and careful manual review against the
real Flutter/Dart API surface from documentation. That is meaningfully
weaker than actually running the tools — **treat this codebase as
unverified by a real toolchain until you run the commands below.**

One real, user-reported bug already surfaced this gap: `flutter analyze`
found a `creation_with_non_type 'MyApp'` error in the auto-generated
`test/widget_test.dart` (Flutter's own `flutter create` boilerplate
referencing a class name — `MyApp` — that doesn't exist in this app, whose
root widget is `PosApp` in `lib/main.dart`). That's been fixed (the test
file now actually exercises `PosApp`), but it's a concrete reminder that
sandbox review missed something a real toolchain caught in seconds — run
the toolchain for real before assuming anything else is clean.

**First thing to do in Claude Code:**

```bash
flutter pub get
flutter analyze
flutter test
```

Fix whatever `flutter analyze`/`flutter test` surface for real — don't
assume the sandbox-era review already covers it.

## Working conventions to keep

- All monetary values from the API arrive as JSON strings (e.g.
  `"2299.00"`), matching the backend's `NUMERIC(14,2)` columns — don't
  parse them into `double` for anything that gets displayed or sent back
  as a price; only for intermediate math where the result is immediately
  re-formatted as a string for display, matching the pattern already in
  `session.dart`/`api_client.dart`.
- `AppSession` (`lib/state/session.dart`) is the single source of truth for
  session/cart state — a `ChangeNotifier` screens `context.watch`. Keep new
  mutating operations there, following the existing
  `try { ... } on ApiException catch (e) { ... }` pattern that maps API
  error codes (e.g. `STOCK_UNAVAILABLE`, `PAYMENT_MISMATCH`) to
  user-facing messages.
- `branchId`/`posTerminalId` in `AppSession` are hardcoded to the seed data
  for now (`TODO(phase 2)` comment in `session.dart`) — a real
  terminal-registration/device-binding flow replaces this later, per FRD
  §18.
- The idempotency key generator in `session.dart` (`_newIdempotencyKey`)
  uses `Random().nextInt(1 << 31)` deliberately kept under Dart's
  documented safe boundary for the non-secure `Random` generator, since
  this couldn't be tested against a real Dart runtime — don't casually
  "simplify" this back to `1 << 32` without checking that boundary for
  real first.
