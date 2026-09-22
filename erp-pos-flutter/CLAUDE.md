# erp-pos-flutter — context for Claude Code

This file is read automatically by Claude Code at the start of every session
in this repo. Read it, then read `../erp-core-go/docs/phase0_1_design.md`
§3 for the API contracts this app calls against (assumes this repo and
`erp-core-go` are sibling directories, e.g. `D:\projects\erp-core-go` and
`D:\projects\erp-pos-flutter` — if they're laid out differently, the API
contract is still the source of truth, just at a different relative path).

**This file was badly out of date until an explicit "review previous
phases for anything missing" audit (2026-09-22) caught it** — it still
described a Phase 0/1 walking-skeleton ("not yet built: remove-line,
discounts, offline persistence, receipt printing, PIN quick-login") long
after `erp-core-go`'s own roadmap had marked all of those backend
features "closed." The backend work was real; this app's own UI simply
never caught up to most of it, across Phase 1's discounts, Phase 3's
promotions/coupons/loyalty, and Phase 4's credit facility — nobody had
gone back to check the POS terminal (the only place any of this is
actually *used*) against what the backend could already do. That gap is
now closed; see "Current state" below for what's real as of this pass,
and treat this file as trustworthy again unless a future session finds
otherwise.

## What this is

The Flutter POS terminal client for a configurable, AI-powered, cloud-native
Universal Retail & Wholesale ERP. Targets Android/iOS/Windows tablets per
`../erp-core-go/docs/tech_stack_decision.md` §4.

## Current state

**Built and verified live** (against a real running `erp-core-go` +
Postgres, not just `flutter analyze`/`flutter test`):

- Login: password (`POST /auth/login`) and PIN quick-login
  (`POST /auth/pin-login`, device-bound) — `lib/screens/login_screen.dart`.
- Core cart flow: barcode scan → add line → update line quantity → remove
  line → checkout, backed by `AppSession` (`lib/state/session.dart`, a
  `ChangeNotifier`) and `ApiClient` (`lib/api/api_client.dart`) —
  `lib/screens/pos_screen.dart`.
- Offline-first: `lib/local/offline_store.dart` (sqflite) — a cart is
  either entirely server-backed or entirely local, never mixed mid-sale;
  `POST /sync/push`/`GET /sync/pull` reconcile once connectivity returns.
- **Closed in the 2026-09-22 audit pass** — every one of these calls a
  real `erp-core-go` endpoint that had existed since an earlier phase but
  was never wired into this app:
  - Manual discounts (`POST /sales/orders/{id}/discounts`) — a dialog
    mirroring the backend's 0-5/5-15/15-25% approval tiers.
  - Customer attach, existing-or-walk-in (`POST /sales/orders/{id}/customer`)
    — required before a credit sale or loyalty redemption.
  - Promotions auto-apply and coupon codes
    (`POST /sales/orders/{id}/promotions/apply`,
    `POST /sales/orders/{id}/coupons`).
  - Loyalty point redemption (`POST /sales/orders/{id}/loyalty/redeem`),
    with the customer's live balance fetched via
    `GET /customers/{id}/loyalty`.
  - `credit` as a real checkout payment method, gated client-side on a
    customer being attached (the backend's own `CREDIT_HOLD`/
    `CREDIT_LIMIT_EXCEEDED` codes are mapped to readable messages).
  - Void (`POST /sales/orders/{id}/void`).
  - **Receipt display and printing** — Phase 1's own "receipt-printing"
    Definition of Done item, previously entirely unbuilt on this side
    despite the backend driver (`internal/printing`) existing since
    Phase 1. `lib/screens/receipt_screen.dart` renders the real itemized
    receipt (`GET /sales/orders/{id}/receipt`); `lib/printing/printer_service.dart`
    sends the backend's raw ESC/POS bytes (`GET .../receipt/print`) to a
    configured network printer over raw TCP (port 9100 — the same
    protocol most thermal receipt printers speak on a LAN, chosen over a
    Bluetooth SDK to keep this app's dependency-light posture; see that
    file's own doc comment). `lib/screens/printer_settings_screen.dart`
    configures the printer's host/port.

**Three real backend bugs found and fixed during this pass, live, not by
inspection alone** — this app's own new e2e-style test
(`test/api_client_live_test.dart`, runs against a real docker-compose
backend, skips itself if none is reachable) is what surfaced them:

1. `POST /sales/orders/{id}/promotions/apply`, `POST /sales/orders/{id}/coupons`,
   and `POST /sales/orders/{id}/loyalty/redeem` don't return a bare
   order object like every other cart-mutating endpoint — they wrap it
   as `{applied/discount_amount, order}`, and `order` can be `null` for
   promotions when nothing matched. `ApiClient` unwraps this correctly
   now; a naive client-side JSON cast would throw (and briefly did).
2. `POST /products` (built server-side in this same audit pass — see
   `erp-core-go`'s own `CLAUDE.md`/roadmap) never seeded a `stock_levels`
   row for a brand-new variant, so the first `AddLine` against it threw a
   confusing `NOT_FOUND` instead of a correct `STOCK_UNAVAILABLE` — fixed
   server-side, verified from this app's own live test.
3. Checkout allowed finalizing a cart with **zero lines** and recording
   whatever payment amount the client sent, producing a valid-looking
   zero-value "sale" — fixed server-side (a real `EMPTY_CART`-shaped
   `INVALID_REQUEST`), verified from this app's own live test.

**Known gap, still real:** `GET /sales/orders/{id}` doesn't yet return
line items for the FULL order in every case exactly as this app expects
— check `phase0_1_design.md` §3.4 before assuming otherwise if a second
device's cart view ever looks wrong.

**Not yet built:** on-device verification of offline sync, printing, and
PIN-login against real Android/iOS hardware or a real network thermal
printer — this environment has never had either. Everything above is
verified against a real backend over HTTP/TCP, which is meaningfully
better than a stub, but is not the same as an actual cashier's hands on
an actual device. A camera-based barcode scanner (for a phone/tablet
without a dedicated scanner gun) is also still open — `pos_screen.dart`'s
own doc comment flags it.

## Verification status

`flutter pub get && flutter analyze && flutter test` all run clean as of
this pass — zero issues, all tests passing (`test/offline_store_test.dart`,
`test/printer_service_test.dart` — a real `ServerSocket` on localhost, not
mocked — `test/widget_test.dart`, and `test/api_client_live_test.dart`,
which needs a running `erp-core-go` + Postgres on `localhost:8080` to do
anything beyond skip itself). Run all four for real before trusting a
change here — this project's own history (see the bugs above, and the
original `MyApp`-vs-`PosApp` boilerplate mismatch from Phase 0) is that
sandbox-only review misses real things a real toolchain catches in
seconds.

## Working conventions to keep

- All monetary values from the API arrive as JSON strings (e.g.
  `"2299.00"`), matching the backend's `NUMERIC(14,2)` columns — don't
  parse them into `double` for anything that gets displayed or sent back
  as a price; only for intermediate math where the result is immediately
  re-formatted as a string for display.
- `AppSession` (`lib/state/session.dart`) is the single source of truth for
  session/cart state — a `ChangeNotifier` screens `context.watch`. New
  mutating operations follow the existing
  `try { ... } on ApiException catch (e) { ... }` pattern that maps API
  error codes to user-facing messages, and check `_canUseLiveOnlyFeatures`
  first if the operation has no offline equivalent (discounts, promotions,
  coupons, loyalty, void, customer attach, quantity update all fall in
  this category — `OfflineStore`'s local schema only ever models the
  walking-skeleton cart shape of lines + payments).
- `branchId`/`posTerminalId` in `AppSession` are hardcoded to the seed data
  for now (`TODO(phase 2)` comment in `session.dart`) — a real
  terminal-registration/device-binding flow replaces this later, per FRD
  §18.
- The idempotency key generator in `session.dart` (`_newIdempotencyKey`)
  uses `Random().nextInt(1 << 31)` deliberately kept under Dart's
  documented safe boundary for the non-secure `Random` generator.
- `PrinterService` (`lib/printing/printer_service.dart`) is deliberately
  `dart:io Socket`-based, not a new package — this app has taken on
  exactly one new dependency since Phase 0 (`sqflite`, for offline sync).
  Don't reach for a Bluetooth printer SDK without a real reason; raw-TCP
  network printing covers the common case.
