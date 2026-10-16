import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import 'package:erp_pos_app/api/api_client.dart';
import 'package:erp_pos_app/screens/pos_screen.dart';
import 'package:erp_pos_app/state/session.dart';

/// Reproduces, against the real running PosScreen widget tree (not the
/// private dialog classes directly — Dart privacy is per-file, so a
/// separate test file can't import `_DiscountDialog`/
/// `_CustomerPickerDialogState` anyway), the two bugs reported live:
/// "the discount Apply button doesn't work" and "the Add Walk-in button
/// doesn't work." Both turned out to be the same root cause — a
/// TextField wired to a TextEditingController but with no `onChanged`
/// callback to trigger a rebuild, so a button whose enabled state reads
/// that controller's `.text` never re-evaluates after the user types.
/// This test types into the fields in the exact order that exposed each
/// bug and asserts the button becomes enabled — it would fail against
/// the pre-fix code and passes now.
///
/// Needs a real running erp-core-go backend (docker-compose,
/// localhost:8080) — skipped, not failed, if one isn't reachable, same
/// convention as api_client_live_test.dart.
const _baseUrl = 'http://localhost:8080';

void main() {
  setUpAll(() {
    // PosScreen.initState calls AppSession.refreshPendingSyncCount, which
    // touches OfflineStore's real sqflite database — needs the same FFI
    // factory offline_store_test.dart already uses, since there's no
    // Android/iOS platform channel in a widget test.
    sqfliteFfiInit();
    databaseFactory = databaseFactoryFfi;
  });

  testWidgets('Add walk-in and Discount Apply buttons enable correctly after typing', (WidgetTester tester) async {
    // Widget tests run inside Flutter's fake-async test zone, which real
    // socket I/O (this app's actual HTTP calls) doesn't resolve inside —
    // tester.runAsync() is the documented escape hatch for exactly this:
    // live network calls from within a testWidgets body. Found live: the
    // first version of this test used a bare try/await outside runAsync
    // and silently reported the server unreachable even though a direct
    // curl against the same URL, at the same moment, succeeded.
    late ApiClient api;
    late AppSession session;
    bool serverUp = true;

    await tester.runAsync(() async {
      // Flutter's test binding installs a global HttpOverrides that some
      // package:http versions route real requests through in ways that
      // mangle the outgoing body — clearing it for the duration of this
      // real-network block avoids that, restored to whatever it already
      // was after runAsync exits scope naturally when the test ends.
      // Found live: constructing ApiClient's persistent http.Client()
      // OUTSIDE runAsync (before HttpOverrides.global was cleared) baked
      // in whatever override was active at that moment, permanently
      // breaking every request that client ever sent (400s, no error
      // body) even though ad-hoc http.post() calls made afterward inside
      // runAsync worked fine — constructing it inside, after clearing
      // the override, avoids capturing that stale state.
      HttpOverrides.global = null;
      api = ApiClient(baseUrl: _baseUrl);
      try {
        await api.login(merchantCode: 'acme-sports', email: 'arjun@acme-sports.test', password: 'Passw0rd!');
      } catch (_) {
        serverUp = false;
        return;
      }

      session = AppSession(
        api: api,
        branchId: '22222222-2222-2222-2222-222222222222',
        posTerminalId: '33333333-3333-3333-3333-333333333333',
      );
      session.userId = 'test';
      session.roles = ['Merchant Admin'];

      // A real cart line, via the same barcode the seed data assigns this
      // variant (migrations/002_seed.sql) — Discount only renders once
      // the cart is non-empty (`cartActive` in PosScreen.build).
      final product = await session.scanBarcode('8901234567890');
      if (product == null) {
        // ignore: avoid_print
        print('DEBUG scanBarcode returned null: ${session.lastError}');
        serverUp = false;
        return;
      }
      final added = await session.addToCart(product, 1);
      if (!added) {
        // ignore: avoid_print
        print('DEBUG addToCart failed: ${session.lastError}');
        serverUp = false;
      }
    });

    if (!serverUp) {
      // ignore: avoid_print
      print('SKIPPED: no live erp-core-go backend (or seed data) at $_baseUrl');
      return;
    }

    await tester.pumpWidget(
      MaterialApp(
        home: ChangeNotifierProvider<AppSession>.value(value: session, child: const PosScreen()),
      ),
    );
    // Phase 12: the screen loads its product grid from the live server on
    // open. That's real network I/O, which fake-async pumping alone never
    // completes (and its spinner would keep pumpAndSettle from settling),
    // so let real time pass between frames until the grid has loaded.
    for (var i = 0; i < 40 && find.byType(CircularProgressIndicator).evaluate().isNotEmpty; i++) {
      await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 50)));
      await tester.pump(const Duration(milliseconds: 50));
    }
    await tester.pump(const Duration(milliseconds: 400));

    // --- Add walk-in: type NAME ONLY (the exact scenario that exposed
    // the bug — the button's disabled check is an AND of both fields
    // being empty, so a fresh dialog starts disabled and must rebuild on
    // the very first keystroke to ever become enabled at all). ---
    await tester.tap(find.text('Customer'));
    await tester.pumpAndSettle();
    expect(find.text('Attach a customer'), findsOneWidget);

    Finder addWalkInButton() => find.widgetWithText(FilledButton, 'Add walk-in');
    expect(tester.widget<FilledButton>(addWalkInButton()).onPressed, isNull, reason: 'starts disabled with empty fields');

    await tester.enterText(find.widgetWithText(TextField, 'Name'), 'Walk-in Test ${DateTime.now().microsecondsSinceEpoch}');
    await tester.pump();
    expect(tester.widget<FilledButton>(addWalkInButton()).onPressed, isNotNull,
        reason: 'BUG: Add walk-in stayed disabled after typing a name — no onChanged was rebuilding the dialog');

    await tester.tap(find.text('Cancel').last);
    await tester.pumpAndSettle();

    // --- Discount Apply: type PERCENT FIRST (rebuilds via its own
    // pre-existing onChanged), THEN type REASON SECOND — the exact order
    // that exposed the bug, since percent's own onChanged masked the
    // missing one on the reason field until reason was the last thing
    // typed. ---
    await tester.tap(find.text('Discount'));
    await tester.pumpAndSettle();
    expect(find.text('Manual discount'), findsOneWidget);

    Finder applyButton() => find.widgetWithText(FilledButton, 'Apply');
    expect(tester.widget<FilledButton>(applyButton()).onPressed, isNull, reason: 'starts disabled with empty fields');

    await tester.enterText(find.widgetWithText(TextField, 'Discount %'), '3');
    await tester.pump();
    expect(tester.widget<FilledButton>(applyButton()).onPressed, isNull, reason: 'reason is still empty at this point');

    await tester.enterText(find.widgetWithText(TextField, 'Reason'), 'test reason');
    await tester.pump();
    expect(tester.widget<FilledButton>(applyButton()).onPressed, isNotNull,
        reason: 'BUG: Apply stayed disabled after typing a reason — no onChanged was rebuilding the dialog');

    // --- Fixed amount: the ₹ mode judges the amount by the percent of the
    // cart it comes to (one seed bat), like the server does. ---
    await tester.tap(find.text('Amount ₹'));
    await tester.pump();
    final amountField = find.widgetWithText(TextField, 'Discount amount (₹)');
    expect(amountField, findsOneWidget);
    await tester.enterText(amountField, '50');
    await tester.pump();
    expect(find.textContaining('% of the sale'), findsOneWidget);
    expect(tester.widget<FilledButton>(applyButton()).onPressed, isNotNull, reason: 'a small amount needs no approval');

    await tester.enterText(amountField, '400'); // ~17% of a ₹2,299 bat
    await tester.pump();
    expect(find.widgetWithText(TextField, 'Approver employee code'), findsOneWidget);
    expect(tester.widget<FilledButton>(applyButton()).onPressed, isNull, reason: 'needs an approver now');

    await tester.enterText(amountField, '100000');
    await tester.pump();
    expect(find.textContaining('The maximum on this sale is'), findsOneWidget);
    await tester.enterText(amountField, '10.555');
    await tester.pump();
    expect(find.text('Rupees and paise only, e.g. 99.50'), findsOneWidget);

    await tester.tap(find.text('Cancel').last);
    await tester.pumpAndSettle();
    // Unmount, then let dart:io's 15 s HTTP keep-alive timer (opened by the
    // grid's live search inside the fake clock) expire, so the test ends
    // with nothing pending.
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump(const Duration(seconds: 20));
  });
}
