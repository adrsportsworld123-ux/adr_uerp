# erp-pos-flutter — Phase 0/1 walking skeleton (client)

Login → scan → cart → checkout, talking to the real `erp-core-go` API.

## Read this before trusting the code

Unlike `erp-core-go`, **this code has not been compiled, analyzed, or run
at all.** The sandbox that wrote it had no Flutter or Dart SDK installed,
and the network path to install one (Flutter's distribution lives on
`storage.googleapis.com`) was blocked by the same organizational egress
policy that blocked the Go module proxy — confirmed with a direct
connection attempt, not assumed. For the Go backend, that same restriction
was worked around by type-checking against faithful local stubs of its
dependencies; there's no equivalent shortcut for a full Flutter app (no
Android/iOS SDK, no emulator, and Flutter's own SDK itself was the
unreachable piece, not just a package).

What *was* done to catch bugs anyway, in decreasing order of rigor:

1. Every field name and JSON key in `lib/api/api_client.dart` was
   cross-checked line-by-line against the actual Go structs in
   `erp-core-go/internal/{authn,catalog,sales}/handlers.go` and
   `api_client.dart` — not against the design doc's prose, against the
   real shipped, verified Go code.
2. A scripted brace/paren/bracket balance check ran across every `.dart`
   file (comments stripped, string interpolation left intact so its
   embedded `${...}` code still counts) — everything balances, but this
   only catches gross structural errors, not type errors, typos in a
   valid identifier, or wrong API usage.
3. Everything else is a careful manual read against Flutter/Dart APIs I
   have high but not certain confidence in — `void`-returning callback
   covariance with async handlers (`onPressed: someAsyncMethod`), Provider
   and Material 3 widget APIs, `Random.nextInt` bounds, and so on.

**Before you rely on this beyond "carefully written," run:**

```bash
flutter create --org com.example --project-name erp_pos_app .   # generates android/ios/etc. scaffolding this repo doesn't ship
flutter pub get
flutter analyze
flutter run   # against a running erp-core-go (see its README)
```

If `flutter analyze` finds something, it's very likely one of the risk
points named above — start there.

## What's here

```
lib/main.dart                  app entry, Provider wiring, API base URL config
lib/api/api_client.dart        HTTP client for /api/v1 — mirrors erp-core-go exactly
lib/state/session.dart         AppSession (ChangeNotifier): auth + current cart
lib/screens/login_screen.dart  merchant code / email / password → POST /auth/login
lib/screens/pos_screen.dart    scan → cart → checkout, one continuous screen
```

## A real gap this surfaced in the backend

Building the cart screen surfaced something the backend doesn't do yet:
`GET /sales/orders/{id}` (phase0_1_design.md §3.4) is documented as
returning "Full order detail," but the actual `loadOrder()` in
`erp-core-go/internal/sales/handlers.go` only selects order-level
aggregates — no line items. This app works around it by tracking what it
itself just added to the cart locally (`CartLineDisplay` in
`state/session.dart`), which is fine for a single device but means a
second device, or a reload against a cart another device opened, can't see
line detail. See the comment on `CartLineDisplay` for the concrete fix
(extend `loadOrder` to join `sales_order_lines`, or add a dedicated lines
endpoint) — flagged rather than silently worked around only on this side.

## Known simplifications in this slice

- **Barcode input is a plain TextField, not a camera scanner.** This is
  actually correct for real hardware barcode-scanner guns (they're
  keyboard-wedge devices — they type the code + Enter into whatever has
  focus) but a phone/tablet without a scanner gun has no way to scan yet;
  a camera-based scanner (e.g. `mobile_scanner`) is a real follow-up, not
  a placeholder this already covers.
- **No local/offline storage.** Everything round-trips to the server
  immediately — no SQLite/Drift cache, no offline queue. The tech stack
  doc calls for Flutter's offline-first storage as a core requirement;
  this walking-skeleton slice deliberately doesn't build it yet so the
  first slice stays small enough to review, matching how `erp-core-go`
  started with one endpoint before the full sales flow.
- **PIN quick-login isn't implemented** — only the username/password
  path, noted on the login screen itself.
- **`branch_id`/`pos_terminal_id` are hardcoded** to the seed data in
  `erp-core-go/migrations/002_seed.sql`. A real device-registration/
  binding flow (FRD §18) replaces this in a later slice — see the `TODO`
  in `state/session.dart`.

## Running against erp-core-go

1. Start the backend: `docker compose up --build` in `erp-core-go/`
   (see that repo's README — includes the seed data this app assumes).
2. Point this app at it: an Android emulator reaches the host machine at
   `10.0.2.2` (the default in `main.dart`), not `localhost` — a real
   device on the same network needs your machine's LAN IP instead, and
   iOS simulator/desktop can use `localhost` directly. Override at build
   time rather than editing the constant:
   ```bash
   flutter run --dart-define=API_BASE_URL=http://localhost:8080
   ```
