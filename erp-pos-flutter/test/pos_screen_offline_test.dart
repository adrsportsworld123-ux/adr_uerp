// Phase 12 billing screen tests that need no server: the session runs in
// offline mode against a seeded on-device catalog (real SQLite via FFI),
// and every HTTP call fails like a dead network would. That makes the cart,
// the payment sheet and the layouts fully deterministic, and exercises the
// offline path a real cashier depends on.
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/testing.dart';
import 'package:path/path.dart' as p;
import 'package:provider/provider.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import 'package:erp_pos_app/api/api_client.dart';
import 'package:erp_pos_app/local/offline_store.dart';
import 'package:erp_pos_app/screens/pos_screen.dart';
import 'package:erp_pos_app/state/session.dart';
import 'package:erp_pos_app/ui/theme.dart';

Map<String, dynamic> _product(String barcode, String id, String name, String sku, String price) => {
      'barcode': barcode,
      'product_id': 'prod-$id',
      'product_name': name,
      'hsn_code': '9506',
      'variant_id': 'var-$id',
      'sku': sku,
      'selling_price': price,
      'mrp': price,
      'cgst_rate': 6.0,
      'sgst_rate': 6.0,
      'igst_rate': 0.0,
      'cess_rate': 0.0,
      'price_includes_tax': true,
    };

Future<void> _loadFonts() async {
  Future<void> family(String name, List<String> files) async {
    final loader = FontLoader(name);
    for (final f in files) {
      final bytes = File('assets/fonts/$f').readAsBytesSync();
      loader.addFont(Future.value(ByteData.view(bytes.buffer)));
    }
    await loader.load();
  }

  await family('Geist', ['Geist-Regular.ttf', 'Geist-Medium.ttf', 'Geist-SemiBold.ttf', 'Geist-Bold.ttf']);
  await family('GeistMono', ['GeistMono-Regular.ttf', 'GeistMono-Medium.ttf', 'GeistMono-SemiBold.ttf']);
  // Material icons carry meaning on the till (scan, sync, customer), so
  // load the real icon font from the Flutter SDK when it's available.
  final sdk = Platform.environment['FLUTTER_ROOT'];
  final iconFont = File(sdk == null ? '' : '$sdk/bin/cache/artifacts/material_fonts/materialicons-regular.otf');
  if (sdk != null && iconFont.existsSync()) {
    final loader = FontLoader('MaterialIcons')..addFont(Future.value(ByteData.view(iconFont.readAsBytesSync().buffer)));
    await loader.load();
  }
}

AppSession _offlineSession() {
  // Every request fails at the socket, exactly like a dead network.
  final client = MockClient((_) async => throw const SocketException('offline (test)'));
  final api = ApiClient(baseUrl: 'http://pos-test.invalid', httpClient: client);
  final s = AppSession(
    api: api,
    branchId: 'branch-test',
    posTerminalId: 'terminal-test',
    terminalName: 'Counter 2',
    branchName: 'Koramangala',
    merchantCode: 'acme-sports',
  );
  s.userId = 'cashier-test';
  s.roles = ['POS User'];
  s.offlineMode = true;
  return s;
}

/// SQLite via FFI is real async; pump frames while letting it finish.
Future<void> _settle(WidgetTester tester, {int rounds = 8}) async {
  for (var i = 0; i < rounds; i++) {
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 25)));
    await tester.pump(const Duration(milliseconds: 16));
  }
}

Future<AppSession> _pumpPos(WidgetTester tester, Size size) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final session = _offlineSession();
  await tester.pumpWidget(MaterialApp(
    theme: posTheme(Brightness.light),
    debugShowCheckedModeBanner: false,
    home: ChangeNotifierProvider<AppSession>.value(value: session, child: const PosScreen()),
  ));
  await _settle(tester);
  return session;
}

void main() {
  setUpAll(() async {
    sqfliteFfiInit();
    databaseFactory = databaseFactoryFfi;
    final dbPath = p.join(await databaseFactory.getDatabasesPath(), 'erp_pos_offline.db');
    await databaseFactory.deleteDatabase(dbPath);
    final store = OfflineStore();
    await store.cacheCatalog([
      _product('8901234567890', '1', 'SG Kashmir Willow Bat · SH', 'CRK-BAT-SH', '2299.00'),
      _product('8901234560105', '2', 'Yonex Mavis 350 Shuttle · 6', 'BDM-SHT-M350', '1140.00'),
      _product('8901234560204', '3', 'Nivia Football · Size 5', 'FTB-NIV-S5', '899.00'),
    ]);
    await store.cacheStock([
      {'variant_id': 'var-1', 'on_hand': '18.000', 'reserved': '0.000', 'available': '18.000'},
      {'variant_id': 'var-2', 'on_hand': '4.000', 'reserved': '0.000', 'available': '4.000'},
      {'variant_id': 'var-3', 'on_hand': '0.000', 'reserved': '0.000', 'available': '0.000'},
    ]);
    await _loadFonts();
  });

  testWidgets('a complete offline cash sale with the keyboard only', (tester) async {
    final session = await _pumpPos(tester, const Size(1280, 800));

    // The scan field has focus on arrival; a wedge scanner types + Enter.
    expect(tester.widget<TextField>(find.byKey(const Key('scan-field'))).focusNode!.hasFocus, isTrue);
    await tester.enterText(find.byKey(const Key('scan-field')), '8901234567890');
    await tester.testTextInput.receiveAction(TextInputAction.done);
    await _settle(tester);
    expect(find.text('SG Kashmir Willow Bat · SH'), findsWidgets);
    expect(session.currentOrder?.lines, hasLength(1));
    expect(session.currentOrder?.grandTotal, '2299.00');

    // F12 opens payment; the cash field has focus; Enter completes.
    await tester.sendKeyEvent(LogicalKeyboardKey.f12);
    await _settle(tester);
    expect(find.text('Payment'), findsOneWidget);
    expect(find.text('₹2,299.00'), findsWidgets);
    await tester.enterText(find.byKey(const Key('cash-tendered')), '2500');
    await tester.pump();
    expect(find.text('₹201.00'), findsOneWidget, reason: 'change to return is shown as cash is typed');
    await tester.testTextInput.receiveAction(TextInputAction.done);
    await _settle(tester, rounds: 12);

    expect(find.text('Sale complete'), findsOneWidget);
    expect(find.textContaining('Saved offline'), findsOneWidget);
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await _settle(tester);
    expect(session.currentOrder, isNull, reason: 'a new sale starts after completion');
    expect(session.pendingSyncCount, greaterThanOrEqualTo(1), reason: 'the offline sale is queued for sync');
  });

  testWidgets('offline: banner shown, live-only actions disabled, credit explained', (tester) async {
    await _pumpPos(tester, const Size(1280, 800));
    expect(find.byKey(const Key('offline-banner')), findsOneWidget);

    await tester.enterText(find.byKey(const Key('scan-field')), '8901234560105');
    await tester.testTextInput.receiveAction(TextInputAction.done);
    await _settle(tester);

    final discount = find.ancestor(of: find.text('Discount'), matching: find.byType(OutlinedButton));
    expect(tester.widget<OutlinedButton>(discount).onPressed, isNull, reason: 'discounts need a connection');

    await tester.tap(find.byKey(const Key('pay-button')));
    await _settle(tester);
    expect(find.bySemanticsLabel(RegExp('Credit, unavailable: Credit needs a connection')), findsOneWidget);
    expect(find.textContaining('Offline sale'), findsOneWidget);
  });

  testWidgets('offline product search filters the cached catalog by name', (tester) async {
    await _pumpPos(tester, const Size(1280, 800));
    expect(find.text('Nivia Football · Size 5'), findsOneWidget);
    expect(find.text('Out of stock'), findsOneWidget, reason: 'cached stock shows on the tile');
    expect(find.text('4 left'), findsOneWidget, reason: 'low stock is called out');

    await tester.enterText(find.byKey(const Key('scan-field')), 'shuttle');
    await tester.pump(const Duration(milliseconds: 350)); // search debounce
    await _settle(tester);
    expect(find.text('Yonex Mavis 350 Shuttle · 6'), findsOneWidget);
    expect(find.text('Nivia Football · Size 5'), findsNothing);
  });

  testWidgets('round-off: the cashier waives a small balance, and the offline sale queues it for sync', (tester) async {
    final session = await _pumpPos(tester, const Size(1280, 800));
    session.roundOffMax = 5; // Sales Policy, as loaded with the shift rule

    await tester.enterText(find.byKey(const Key('scan-field')), '8901234567890');
    await tester.testTextInput.receiveAction(TextInputAction.done);
    await _settle(tester);
    await tester.tap(find.byKey(const Key('pay-button')));
    await _settle(tester);
    expect(find.byKey(const Key('round-off')), findsOneWidget);
    expect(tester.widget<Text>(find.byKey(const Key('amount-due'))).data, '₹2,299.00');

    // ₹2,299.00 has no paise: waive another amount, within the ₹5 limit.
    await tester.tap(find.text('Other amount'));
    await _settle(tester);
    await tester.enterText(find.descendant(of: find.byType(AlertDialog), matching: find.byType(TextField)), '6');
    await tester.tap(find.text('Apply'));
    await _settle(tester);
    expect(find.textContaining('Enter an amount up to'), findsOneWidget, reason: 'above the limit is refused');
    expect(tester.widget<Text>(find.byKey(const Key('amount-due'))).data, '₹2,299.00');

    await tester.tap(find.text('Other amount'));
    await _settle(tester);
    await tester.enterText(find.descendant(of: find.byType(AlertDialog), matching: find.byType(TextField)), '4');
    await tester.tap(find.text('Apply'));
    await _settle(tester);
    expect(tester.widget<Text>(find.byKey(const Key('amount-due'))).data, '₹2,295.00');
    expect(find.text('Round off −₹4.00'), findsOneWidget);
    expect(find.text('Bill ₹2,299.00'), findsOneWidget);

    await tester.tap(find.byKey(const Key('complete-sale')));
    await _settle(tester, rounds: 12);
    expect(find.text('Sale complete'), findsOneWidget);
    expect(find.textContaining('Total ₹2,295.00'), findsOneWidget, reason: 'what the customer paid');

    final queued = await tester.runAsync(() => OfflineStore().listUnsynced());
    final sale = queued!.last;
    expect(sale.roundOff, -4);
    expect(sale.payments.single['amount'], 2295.0);
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await _settle(tester);
    // Leave the queue as the goldens below expect it (one earlier sale pending).
    await tester.runAsync(() => OfflineStore().markSynced(sale.id, 'synced-in-test'));
  });

  // Layout goldens at the three design breakpoints. Run with
  // `flutter test --update-goldens` after an intended visual change.
  for (final (name, size) in [
    ('phone', const Size(390, 844)),
    ('tablet_portrait', const Size(800, 1280)),
    ('desktop', const Size(1280, 800)),
  ]) {
    testWidgets('golden: billing screen, $name', (tester) async {
      await _pumpPos(tester, size);
      await tester.enterText(find.byKey(const Key('scan-field')), '8901234567890');
      await tester.testTextInput.receiveAction(TextInputAction.done);
      await _settle(tester);
      // Let button colour transitions (≈200 ms) finish: the Pay button just
      // switched from disabled (empty cart) to enabled.
      await tester.pump(const Duration(milliseconds: 400));
      await expectLater(find.byType(PosScreen), matchesGoldenFile('goldens/pos_billing_$name.png'));
    });
  }
}
