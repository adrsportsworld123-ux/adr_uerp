// Phase 13 sync queue: real SQLite (FFI, own directory) holding a queued,
// a failed and an already-synced-needs-review sale, and a scripted server.
// Checks the counts, the server's review items for this till, and that
// "Retry now" pushes what's waiting and records the server's
// negative_stock flag on the sale it accepted.
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:provider/provider.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import 'package:erp_pos_app/api/api_client.dart';
import 'package:erp_pos_app/local/offline_store.dart';
import 'package:erp_pos_app/screens/sync_queue_screen.dart';
import 'package:erp_pos_app/state/session.dart';
import 'package:erp_pos_app/ui/theme.dart';

Future<void> _settle(WidgetTester tester, {int rounds = 10}) async {
  for (var i = 0; i < rounds; i++) {
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 25)));
    await tester.pump(const Duration(milliseconds: 30));
  }
}

String _count(WidgetTester tester, String key) =>
    tester.widget<Text>(find.descendant(of: find.byKey(Key(key)), matching: find.byType(Text))).data!;

void main() {
  late String queuedId, failedId;

  setUpAll(() async {
    sqfliteFfiInit();
    databaseFactory = databaseFactoryFfi;
    await databaseFactory.setDatabasesPath(Directory.systemTemp.createTempSync('pos_sync_queue_test').path);
    final store = OfflineStore();
    await store.cacheCatalog([
      {
        'barcode': '8900000000101',
        'product_id': 'p1',
        'product_name': 'Grip tape',
        'hsn_code': '9506',
        'variant_id': 'v1',
        'sku': 'GRIP-1',
        'selling_price': '150.00',
        'mrp': '150.00',
        'cgst_rate': 6.0,
        'sgst_rate': 6.0,
        'igst_rate': 0.0,
        'cess_rate': 0.0,
        'price_includes_tax': true,
      }
    ]);
    final product = (await store.lookupBarcode('8900000000101'))!;
    Future<String> sale(int qty) async {
      final id = await store.createPendingOrder('branch-test', 'terminal-test');
      await store.addLine(id, product, qty.toDouble());
      await store.checkout(id, [PaymentInput(method: 'cash', amount: 150.0 * qty)]);
      return id;
    }

    queuedId = await sale(1);
    failedId = await sale(2);
    await store.markSyncFailed(failedId, 'unknown variant');
    final reviewed = await sale(3);
    await store.markSynced(reviewed, 'server-order-0', needsReview: true);
  });

  testWidgets('counts, review items, and a retry that records the server flag', (tester) async {
    tester.view.physicalSize = const Size(1280, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);

    final pushed = <Map<String, dynamic>>[];
    final client = MockClient((r) async {
      http.Response json(Object b) => http.Response(jsonEncode(b), 200, headers: {'content-type': 'application/json; charset=utf-8'});
      switch (r.url.path) {
        case '/api/v1/inventory/review':
          expect(r.url.queryParameters['terminal_id'], 'terminal-test');
          return json({
            'items': [
              {
                'item_id': 'rv1',
                'product_name': 'Grip tape',
                'sku': 'GRIP-1',
                'order_number': 'MGR-77',
                'quantity': '3.000',
                'on_hand_now': '-2.000',
                'created_at': '2026-10-10T06:00:00Z',
              }
            ],
            'total': 1,
            'open_count': 1,
          });
        case '/api/v1/sync/push':
          final orders = ((jsonDecode(r.body) as Map)['orders'] as List).cast<Map<String, dynamic>>();
          pushed.addAll(orders);
          return json({
            'results': [
              for (var i = 0; i < orders.length; i++)
                {'idempotency_key': orders[i]['idempotency_key'], 'status': 'ok', 'order_id': 'server-$i', 'negative_stock': i == 0},
            ]
          });
        case '/api/v1/sync/pull':
          return json({'server_time': '2026-10-10T12:00:00Z', 'catalog': [], 'stock': []});
      }
      return http.Response('{}', 404);
    });
    final session = AppSession(
      api: ApiClient(baseUrl: 'http://pos-test.invalid', httpClient: client),
      branchId: 'branch-test',
      posTerminalId: 'terminal-test',
    );
    session.userId = 'cashier-test';

    await tester.pumpWidget(MaterialApp(
      theme: posTheme(Brightness.light),
      home: ChangeNotifierProvider<AppSession>.value(value: session, child: const SyncQueueScreen()),
    ));
    await _settle(tester);

    expect(_count(tester, 'count-waiting'), 'Waiting · 1');
    expect(_count(tester, 'count-failed'), 'Failed · 1');
    expect(_count(tester, 'count-review'), 'Needs review · 1');
    expect(find.textContaining('Failed: unknown variant'), findsOneWidget);
    expect(find.byKey(const Key('review-rv1')), findsOneWidget);
    expect(find.textContaining('stock now -2'), findsOneWidget);
    expect(find.text('Retry now'), findsOneWidget);
    expect(find.textContaining('Synced · needs review'), findsOneWidget);

    await tester.tap(find.byKey(const Key('sync-now')));
    await _settle(tester, rounds: 16);

    expect(pushed, hasLength(2), reason: 'both the queued and the failed sale are retried');
    expect(_count(tester, 'count-waiting'), 'Waiting · 0');
    expect(_count(tester, 'count-failed'), 'Failed · 0');
    final rows = await tester.runAsync(() => OfflineStore().listSyncQueue());
    final byId = {for (final r in rows!) r.id: r};
    final firstPushed = pushed.first['idempotency_key'];
    final flagged = rows.where((r) => r.state == 'needs_review').length;
    expect(flagged, 2, reason: 'the earlier flagged sale plus the one the server just flagged ($firstPushed)');
    expect(byId[queuedId]!.synced && byId[failedId]!.synced, isTrue);
    expect(byId[failedId]!.syncError, isNull, reason: 'a successful retry clears the old error');
    session.dispose();
  });
}
