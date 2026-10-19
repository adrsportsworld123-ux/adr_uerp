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

**Phase 12 (2026-10-10) — billing redesign and pairing.** `pos_screen.dart`
is now a shell plus `part` files in `lib/screens/pos/` (status bar, product
grid, cart panel, payment sheet, dialogs); `lib/ui/approval_pin_pad.dart`
and `lib/ui/format.dart` are shared. Keyboard: F2 scan, F4 customer,
F7 discount, F8 hold, F9 recall, F12 pay, `+` `−` `Del` on the selected
line. Two rules to keep: the scan field is never disabled (a disabled
field drops focus and the next scanner input is lost), and every dialog
the screen opens gets the session via `_scoped(...)`, so the screen works
wherever its provider sits. `test/pos_screen_offline_test.dart` drives a
complete keyboard-only sale offline against real SQLite plus goldens at
390/800/1280 px (`flutter test --update-goldens` after an intended visual
change). Live-backend widget tests must let real I/O finish with
`tester.runAsync` and end by pumping past dart:io's 15 s keep-alive timer.

**Phase 13 (2026-10-10) — sales history and returns.** The F6 menu entry
"Sales history & returns" opens `lib/screens/orders_screen.dart`:
- a day summary, scoped to this till or the whole branch
- filters
- this device's unsynced offline sales from SQLite, shown as "waiting to sync"
- server sales; tapping one opens `ReceiptScreen` in history mode (`orderId` set: reprint, no void)

`lib/screens/return_screen.dart` records a return:
- `GET .../returnable`, then `POST .../returns`
- one idempotency key per screen, so a retried submit can never refund twice
- the manager's code and PIN on `ApprovalPinPad` when the merchant's policy requires approval

Returns are online-only, and an offline sale can't be returned until it has synced.

`test/orders_returns_test.dart` uses its own SQLite directory, because test files run concurrently and share the default one. `api_client_live_test.dart` honours `ERP_API_URL` for running against a throwaway API.

**Open-price items (Phase 13).** Always add items through `addProductToCart` in `lib/ui/open_price_dialog.dart`, never through `session.addToCart` directly. It opens the price keypad (`OpenPriceDialog`) when `AppSession.openPricePrompt` is set, which happens in two cases:
- the lookup carried `open_price`
- the server answered `OPEN_PRICE_REQUIRED`, which is what happens for search hits, since they carry no range

It also reopens the keypad after a failed approval PIN. Offline, the device enforces the cached range (local DB v3 adds the range columns and `pending_order_lines.open_price`) and refuses prices that need approval.

**Attendance in the status bar (2026-10-18).** Check-in/check-out is a chip in the status bar, next to the shift chip; it is no longer in the overflow menu. The chip shows "Check in" (warn tone) or "In · 9:02 AM" (pay tone), and tapping it opens `AttendanceScreen`. `AppSession.attendance` holds the state: `refreshAttendance()` loads it when `PosScreen` starts and again after the attendance screen closes, and the screen pushes what it loads with `setAttendance`. The chip is disabled offline. On a phone-width bar the ADR badge is hidden so the terminal name keeps its room. Tested in `test/shift_test.dart`.

**Round-off at checkout (2026-10-19).** When Sales Policy allows it (`AppSession.roundOffMax` > 0, loaded with the shift rule in `refreshShift()`), the payment sheet shows a Round off chip that waives the bill's paise, and "Other amount" waives any amount up to the limit. `roundOffAuto` starts the chip on. The sheet returns a `_PaymentResult` (payments + signed `roundOff`); `session.checkout(payments, roundOff:)` sends it online, or stores it in `pending_orders.round_off` (local DB v5) for `/sync/push` offline. Receipts and the X/Z screens show the Round off line. erp-core-go `phase0_1_design.md` §3.67. Tested in `test/pos_screen_offline_test.dart`.

**GST split (2026-10-19).** The cart totals, receipt screen and Z report show CGST + SGST (or IGST, or the merchant's single tax such as VAT) from the server's `tax_breakup` (erp-core-go `phase0_1_design.md` §3.68). `AppSession.taxRegime` (from `GET /tax/regime`, loaded in `refreshShift()`, kept for offline) lets `taxRowsFor(order)` split an offline cart locally with the server's rule (`TaxBreakup.local`). Tested in `test/tax_breakup_test.dart`; the billing goldens show the CGST/SGST rows. Money is shown in the branch's currency (`PosCurrency` in `lib/ui/format.dart`, set from `/tax/regime` in `refreshShift()`; INR groups 1,23,456, others 123,456); use `formatRupees` / `PosCurrency.symbol`, never a literal ₹. The return confirmation shows the credit note's tax (§3.69). The open-price keypad takes an optional Description (§3.70): online it's sent with the line; offline it's stored in `pending_order_lines.description` (local DB v6) and sent with `/sync/push`. The receipt screen hides SKUs when the branch's `show_sku` is off.

**Grid order and tile colours (§3.71).**
- **Order:** when the branch's Sales Policy says so (`AppSession.openPriceFirst`, from the sales settings' branch list), open-price items come first on the grid. Online, the server sorts (`searchProducts(branchId:)`); offline, `OfflineStore.searchCatalog(openPriceFirst:)` does.
- **Colour:** tiles take the product's `pos_color`, or for an open-price item the standard open-price colour (`tileAccent()`), with an "Open price" label.
- **Local DB:** v7 adds `catalog.pos_color` and resets `last_pull_at` once, so every product gets its colour.

Tested in `test/product_grid_test.dart` (own SQLite folder, so the billing goldens are untouched).

**Shifts (Phase 13).** `lib/screens/shift_screen.dart` has three parts:
- `ShiftScreen`: open with a float, the X report, and cash in/out via `CashMovementDialog`, which keeps one idempotency key across retries
- `CloseShiftScreen`: a denomination count with live variance. When the count doesn't match, it asks for a reason and a manager's code and PIN. It syncs offline sales before counting.
- `ShiftReportView`: the X and Z reports

The status bar's shift chip opens it. `AppSession.refreshShift()` loads the open shift and `shifts_required`. When shifts are required and none is open, `PosScreen` starts on the shift screen. Shifts are online-only; offline selling is never blocked.

**X and Z reports (2026-10-18, `erp-core-go` `phase0_1_design.md` §3.60).** The X report is one shift (the shift screen; a closed shift's is "X report (final)"); the Z report is the branch's whole business day. Menu → X / Z reports (`lib/screens/till_reports_screen.dart`) lists shifts by dates (this till or the branch) and shows the day's Z report, printed through `PrinterService` from the server's ESC/POS bytes (`/pos-shifts/{id}/report/print`, `/reports/z-report/print`), and closes a day that has ended (`reports.day_close`, the server decides). `test/till_reports_test.dart` prints to a real `ServerSocket`.

**Sync queue (Phase 13).** `lib/screens/sync_queue_screen.dart` (menu → Sync queue) shows the device's offline sales:
- waiting to sync
- failed, with the server's reason
- synced
- needs review, for sales the server accepted but that oversold stock (`negative_stock`)

It also lists the server's open review items for this till. Local DB v4 records `synced_at` and `needs_review`.

Live tests need seed stock and seed credentials. After a full admin e2e run, restore the dev passwords, and if needed Arjun's PIN `1234`, and top up the seed bat (`99999999-…`) with an inventory adjustment.

**Product lookup (Phase 13).** `lib/screens/product_lookup_screen.dart`, reached from menu → Product lookup (`ProductSearchScreen(lookupMode: true)`, where a scanned barcode opens the product directly) or from the search screen's info button. It shows price, stock by branch and batches, and offers Edit price, Adjust stock and Print label (Standard, Compact, Detailed or QR, with copies, to the receipt printer). Permissions come from `GET /me/permissions` (`AppSession.can`). While they're unknown, actions stay enabled and the server's 403 decides.

**Approval policy (Phase 15).** The merchant's approval policy (`GET /settings/approval-policies`) drives these screens:
- The discount dialog reads `AppSession.discountTiers`, which `refreshPermissions` loads. It falls back to the server defaults of 5/15/25%.
- The return screen asks for a manager only above the returnable's `approval_above`.
- Product lookup's adjust dialog shows approver fields after `ADJUSTMENT_NOT_APPROVED`.

In all three the client only gives hints. When the server wants an approver the client didn't expect, the fields appear.

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
- `branchId`/`posTerminalId` in `AppSession` come from the device's
  pairing (`lib/state/terminal_config.dart`, Phase 12): the app boots into
  `PairingScreen` until an admin-issued code pairs it to a terminal.
  `--dart-define=USE_SEED_TERMINAL=true` uses the seed terminal for
  development. Tests construct `AppSession` directly or pass
  `PosApp(initialConfig: ...)`.
- The idempotency key generator in `session.dart` (`_newIdempotencyKey`)
  uses `Random().nextInt(1 << 31)` deliberately kept under Dart's
  documented safe boundary for the non-secure `Random` generator.
- `PrinterService` (`lib/printing/printer_service.dart`) is deliberately
  `dart:io Socket`-based, not a new package — this app has taken on
  exactly one new dependency since Phase 0 (`sqflite`, for offline sync).
  Don't reach for a Bluetooth printer SDK without a real reason; raw-TCP
  network printing covers the common case.
