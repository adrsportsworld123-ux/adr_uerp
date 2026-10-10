// Phase 13: sales history and returns on the POS. The online tests run
// against a scripted HTTP backend (MockClient) that answers with the same
// JSON shapes erp-core-go returns (internal/sales/history.go, returns.go).
// The offline test uses real SQLite via FFI, so a queued sale is a real row.
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
import 'package:erp_pos_app/screens/orders_screen.dart';
import 'package:erp_pos_app/state/session.dart';
import 'package:erp_pos_app/ui/theme.dart';

const _orderId = 'order-1';

Map<String, dynamic> _historyRow(String status) => {
      'order_id': _orderId,
      'order_number': 'MGR-1001',
      'status': status,
      'finalized_at': '2026-10-10 10:15:00+00',
      'branch_id': 'branch-test',
      'branch_name': 'Koramangala',
      'pos_terminal_id': 'terminal-test',
      'terminal_name': 'Counter 2',
      'cashier_id': 'u1',
      'cashier_name': 'Ravi',
      'customer_id': null,
      'customer_name': null,
      'item_count': '3.000',
      'subtotal': '3000.00',
      'discount_total': '0.00',
      'tax_total': '360.00',
      'grand_total': '3360.00',
      'payment_methods': ['cash'],
      'channel': 'pos',
    };

/// A scripted backend. [returnsAnswers] are the responses to successive
/// POST .../returns calls; every request is recorded in [requests].
class _Backend {
  final List<http.Request> requests = [];
  final List<(int, Map<String, dynamic>)> returnsAnswers;
  String status = 'finalized';

  _Backend(this.returnsAnswers);

  Future<http.Response> handle(http.Request r) async {
    requests.add(r);
    final path = r.url.path;
    http.Response json(int code, Object body) =>
        http.Response(jsonEncode(body), code, headers: {'content-type': 'application/json; charset=utf-8'});
    if (r.method == 'GET' && path == '/api/v1/sales/orders') {
      return json(200, {'orders': [_historyRow(status)], 'total': 1, 'page': 1, 'limit': 25});
    }
    if (r.method == 'GET' && path == '/api/v1/sales/summary') {
      return json(200, {
        'order_count': 1,
        'voided_count': 0,
        'refunded_count': 0,
        'gross_sales': '3000.00',
        'discount_total': '0.00',
        'tax_total': '360.00',
        'net_sales': '3360.00',
        'by_payment_method': [
          {'method': 'cash', 'amount': '3360.00', 'count': 1}
        ],
        'by_cashier': [],
      });
    }
    if (path == '/api/v1/sales/orders/$_orderId/receipt') {
      return json(200, {
        'order_number': 'MGR-1001',
        'status': status,
        'finalized_at': '2026-10-10T10:15:00Z',
        'merchant_name': 'Acme Sports',
        'branch_name': 'Koramangala',
        'cashier_name': 'Ravi',
        'customer_name': null,
        'lines': [
          {'product_name': 'Cricket Ball', 'sku': 'CRK-BALL', 'quantity': '3.000', 'unit_price': '1000.00', 'line_total': '3360.00'}
        ],
        'subtotal': '3000.00',
        'discount_total': '0.00',
        'tax_total': '360.00',
        'grand_total': '3360.00',
        'payments': [
          {'method': 'cash', 'amount': '3360.00'}
        ],
      });
    }
    if (path == '/api/v1/sales/orders/$_orderId/returnable') {
      return json(200, {
        'order_id': _orderId,
        'order_number': 'MGR-1001',
        'status': status,
        'returnable': true,
        'window_open': true,
        'requires_approval': true,
        'return_window_days': null,
        'credit_outstanding': '0.00',
        'lines': [
          {
            'line_id': 'line-1',
            'variant_id': 'v1',
            'product_name': 'Cricket Ball',
            'sku': 'CRK-BALL',
            'sold_quantity': '3.000',
            'returned_quantity': '0.000',
            'returnable_quantity': '3.000',
            'unit_refund': '1120.00',
          }
        ],
      });
    }
    if (r.method == 'POST' && path == '/api/v1/sales/orders/$_orderId/returns') {
      final (code, body) = returnsAnswers.removeAt(0);
      if (code == 201) status = 'partially_refunded';
      return json(code, body);
    }
    return json(404, {
      'error': {'code': 'NOT_FOUND', 'message': 'unscripted ${r.method} $path'}
    });
  }
}

AppSession _session(http.Client client, {bool offline = false}) {
  final s = AppSession(
    api: ApiClient(baseUrl: 'http://pos-test.invalid', httpClient: client),
    branchId: 'branch-test',
    posTerminalId: 'terminal-test',
    terminalName: 'Counter 2',
  );
  s.userId = 'cashier-test';
  s.roles = ['POS User'];
  s.offlineMode = offline;
  return s;
}

Future<void> _settle(WidgetTester tester, {int rounds = 8}) async {
  for (var i = 0; i < rounds; i++) {
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 25)));
    await tester.pump(const Duration(milliseconds: 16));
  }
}

Future<void> _pumpOrders(WidgetTester tester, AppSession session) async {
  tester.view.physicalSize = const Size(1280, 1000);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(MaterialApp(
    theme: posTheme(Brightness.light),
    home: ChangeNotifierProvider<AppSession>.value(value: session, child: const OrdersScreen()),
  ));
  await _settle(tester);
}

void main() {
  setUpAll(() async {
    sqfliteFfiInit();
    databaseFactory = databaseFactoryFfi;
    // Own directory: test files run concurrently, and the billing tests
    // queue offline sales of their own in the default location.
    await databaseFactory.setDatabasesPath(Directory.systemTemp.createTempSync('pos_orders_test').path);
  });

  testWidgets('history shows the day summary and a sale; a return needs approval and keeps one idempotency key',
      (tester) async {
    final backend = _Backend([
      (403, {
        'error': {'code': 'RETURN_NOT_APPROVED', 'message': 'needs approval'}
      }),
      (201, {
        'return_id': 'ret-1',
        'return_number': 'SRET-1',
        'order_id': _orderId,
        'order_status': 'partially_refunded',
        'refund_method': 'cash',
        'refund_amount': '1120.00',
        'tax_amount': '120.00',
        'restocked': true,
        'approved_by': 'mgr',
        'created_at': '2026-10-10 10:20:00+00',
        'replayed': false,
      }),
    ]);
    final session = _session(MockClient(backend.handle));
    await _pumpOrders(tester, session);

    expect(find.text('MGR-1001'), findsOneWidget);
    expect(find.text('Completed'), findsOneWidget);
    expect(find.byKey(const Key('summary-count')), findsOneWidget);
    expect(find.text('₹3,360.00'), findsWidgets, reason: 'net sales tile and the row total');
    final listCall = backend.requests.firstWhere((r) => r.url.path == '/api/v1/sales/orders');
    expect(listCall.url.queryParameters['terminal_id'], 'terminal-test', reason: 'defaults to this till');
    expect(listCall.url.queryParameters['from'], isNotNull);

    // Open the sale -> receipt in history mode -> Return items.
    await tester.tap(find.text('MGR-1001'));
    await _settle(tester);
    await tester.pump(const Duration(milliseconds: 600));
    expect(find.text('Void this sale'), findsNothing, reason: 'void is for the sale just made, not history');
    await tester.tap(find.byKey(const Key('receipt-return')));
    await _settle(tester);
    await tester.pump(const Duration(milliseconds: 600)); // route transition

    final submit = find.byKey(const Key('return-submit'));
    expect(tester.widget<FilledButton>(submit).onPressed, isNull,
        reason: 'nothing chosen yet');

    await tester.tap(find.byKey(const Key('return-plus-CRK-BALL')));
    await tester.pump();
    expect(find.text('Refund about ₹1,120.00'), findsOneWidget);
    await tester.tap(find.text('Wrong size'));
    await tester.enterText(find.byKey(const Key('return-approver-code')), 'EMP002');
    for (final d in ['1', '2', '3', '4']) {
      await tester.tap(find.text(d).last);
    }
    await tester.pump();

    await tester.ensureVisible(submit);
    await tester.tap(submit);
    await _settle(tester);
    expect(find.byKey(const Key('return-error')), findsOneWidget);
    expect(find.textContaining('Approval failed'), findsOneWidget);

    await tester.tap(submit);
    await _settle(tester);
    expect(find.text('Refund ₹1,120.00'), findsOneWidget, reason: 'confirmation shows the server amount');

    final posts = backend.requests.where((r) => r.method == 'POST').toList();
    expect(posts, hasLength(2));
    final first = jsonDecode(posts[0].body) as Map<String, dynamic>;
    final second = jsonDecode(posts[1].body) as Map<String, dynamic>;
    expect(first['idempotency_key'], second['idempotency_key'], reason: 'a retry never refunds twice');
    expect(second['lines'], [
      {'line_id': 'line-1', 'quantity': 1.0}
    ]);
    expect(second['reason'], 'Wrong size');
    expect(second['refund_method'], 'cash');
    expect(second['approver_code'], 'EMP002');
    expect(second['approver_pin'], '1234');
    expect(second['pos_terminal_id'], 'terminal-test');

    await tester.tap(find.text('Done'));
    await _settle(tester, rounds: 12);
    expect(find.byType(OrdersScreen), findsOneWidget);
    expect(find.text('Partly refunded'), findsOneWidget, reason: 'history reloads after a return');
    // dart:io's HTTP keep-alive timer isn't used by MockClient, but the
    // session timer is: leave cleanly.
    session.dispose();
  });

  testWidgets('offline: history shows this device\'s queued sales and says why the rest is missing', (tester) async {
    final store = OfflineStore();
    await tester.runAsync(() async {
      await store.cacheCatalog([
        {
          'barcode': '8900000000001',
          'product_id': 'prod-1',
          'product_name': 'Nivia Football',
          'hsn_code': '9506',
          'variant_id': 'var-1',
          'sku': 'FTB-1',
          'selling_price': '899.00',
          'mrp': '899.00',
          'cgst_rate': 6.0,
          'sgst_rate': 6.0,
          'igst_rate': 0.0,
          'cess_rate': 0.0,
          'price_includes_tax': true,
        }
      ]);
      final product = await store.lookupBarcode('8900000000001');
      final id = await store.createPendingOrder('branch-test', 'terminal-test');
      await store.addLine(id, product!, 2);
      await store.checkout(id, [PaymentInput(method: 'cash', amount: 1798)]);
    });

    final session = _session(MockClient((_) async => throw const SocketException('offline (test)')), offline: true);
    await _pumpOrders(tester, session);

    expect(find.textContaining('Offline. Only sales on this device are shown'), findsOneWidget);
    expect(find.text('Waiting to sync (1)'), findsOneWidget);
    expect(find.textContaining('OFFLINE-'), findsOneWidget);
    expect(find.text('₹1,798.00'), findsOneWidget);
    expect(find.textContaining('Queued · 2 item(s)'), findsOneWidget);
    session.dispose();
  });
}
