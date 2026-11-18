// The customer's previous outstanding on the payment step (erp-core-go
// GET /sales/orders/{id}/previous-due, checkout's previous_due): kept on
// the bill by default, the amount due is the bill plus it, the cashier can
// leave it off or collect part of it, credit can't pay it, and the receipt
// shows the outstanding, what was collected and the balance. Online
// against a mocked API.
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:provider/provider.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import 'package:erp_pos_app/api/api_client.dart';
import 'package:erp_pos_app/screens/pos_screen.dart';
import 'package:erp_pos_app/state/session.dart';
import 'package:erp_pos_app/ui/theme.dart';

http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json'});

Map<String, Object?> _orderJson({String status = 'cart'}) => {
      'order_id': 'o1',
      'order_number': 'SO-3',
      'status': status,
      'subtotal': '1000.00',
      'discount_total': '0.00',
      'tax_total': '0.00',
      'grand_total': '1000.00',
      'lines': [
        {
          'line_id': 'l1', 'variant_id': 'v1', 'sku': 'BAT', 'product_name': 'Willow Bat', 'quantity': '1', 'unit_price': '1000.00',
          'discount_amount': '0.00', 'tax_amount': '0.00', 'line_total': '1000.00',
        },
      ],
    };

Future<void> _settle(WidgetTester tester, {int rounds = 30}) async {
  for (var i = 0; i < rounds; i++) {
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 25)));
    await tester.pump(const Duration(milliseconds: 16));
  }
}

class _Backend {
  final calls = <String>[];
  Map<String, dynamic>? checkoutBody;
  String outstanding = '500.00';

  late final client = MockClient((req) async {
    final p = req.url.path;
    calls.add('${req.method} $p');
    if (p.endsWith('/sales/orders/o1/previous-due')) {
      return _json({
        'customer_id': 'c1',
        'outstanding': outstanding,
        'invoices': [
          {'order_id': 'a', 'order_number': 'SO-1', 'date': '2026-11-01', 'due_date': null, 'outstanding': '300.00'},
          {'order_id': 'b', 'order_number': 'SO-2', 'date': '2026-11-05', 'due_date': null, 'outstanding': '200.00'},
        ],
      });
    }
    if (p.endsWith('/sales/orders/o1/checkout')) {
      checkoutBody = jsonDecode(req.body) as Map<String, dynamic>;
      return _json(_orderJson(status: 'finalized'));
    }
    if (p.endsWith('/sales/orders/o1/receipt')) {
      final collect = (checkoutBody?['previous_due'] as Map<String, dynamic>?)?['collect'] as num?;
      return _json({
        'order_number': 'SO-3', 'status': 'finalized', 'finalized_at': null, 'merchant_name': 'Shop', 'branch_name': 'Branch',
        'cashier_name': 'Ravi', 'customer_name': 'Kumar', 'lines': [], 'subtotal': '1000.00', 'discount_total': '0.00',
        'tax_total': '0.00', 'grand_total': '1000.00', 'payments': [
          {'method': 'cash', 'amount': '1000.00'},
        ],
        if (checkoutBody?['previous_due'] != null)
          'previous_due': {
            'outstanding': '500.00', 'collected': collect!.toStringAsFixed(2), 'balance_after': (500 - collect).toStringAsFixed(2),
            'total_received': (1000 + collect).toStringAsFixed(2),
            'payments': [
              if (collect > 0) {'order_number': 'SO-1', 'method': 'cash', 'amount': collect > 300 ? '300.00' : collect.toStringAsFixed(2)},
              if (collect > 300) {'order_number': 'SO-2', 'method': 'cash', 'amount': (collect - 300).toStringAsFixed(2)},
            ],
          },
      });
    }
    if (p.contains('/loyalty')) return _json({'customer_id': 'c1', 'available_points': 0, 'balance': 0});
    if (p.endsWith('/categories')) return _json({'categories': []});
    if (p.endsWith('/products/search')) return _json({'results': []});
    return _json({'error': {'code': 'NOT_FOUND', 'message': 'not in this test'}}, 404);
  });
}

Future<AppSession> _pump(WidgetTester tester, _Backend backend, {bool withCustomer = true}) async {
  tester.view.physicalSize = const Size(1280, 1000);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final s = AppSession(
      api: ApiClient(baseUrl: 'http://pos-test.invalid', httpClient: backend.client),
      branchId: 'b',
      posTerminalId: 't',
      terminalName: 'Counter',
      branchName: 'Branch',
      merchantCode: 'm');
  s.userId = 'u';
  s.currentOrder = OrderSummary.fromJson(_orderJson());
  if (withCustomer) {
    s.attachedCustomerId = 'c1';
    s.attachedCustomerName = 'Kumar';
  }
  await tester.pumpWidget(MaterialApp(
    theme: posTheme(Brightness.light),
    home: ChangeNotifierProvider<AppSession>.value(value: s, child: const PosScreen()),
  ));
  await _settle(tester, rounds: 10);
  return s;
}

String _due(WidgetTester tester) => tester.widget<Text>(find.byKey(const Key('amount-due'))).data!;

void main() {
  setUpAll(() async {
    sqfliteFfiInit();
    databaseFactory = databaseFactoryFfi;
    await databaseFactory.setDatabasesPath(Directory.systemTemp.createTempSync('previous_due_test').path);
  });

  testWidgets('kept on the bill: the due is the bill plus it, and the receipt shows the balance', (tester) async {
    final backend = _Backend();
    await _pump(tester, backend);
    await tester.tap(find.byKey(const Key('pay-button')));
    await _settle(tester);
    expect(find.byKey(const Key('previous-due')), findsOneWidget);
    expect(find.textContaining('2 earlier bills'), findsOneWidget);
    expect(_due(tester), contains('1,500.00'));
    expect(find.byKey(const Key('bill-plus-previous')), findsOneWidget);

    await tester.tap(find.byKey(const Key('complete-sale')));
    await _settle(tester);
    expect(backend.checkoutBody!['previous_due'], {'collect': 500});
    expect((backend.checkoutBody!['payments'] as List).single, containsPair('amount', 1500));

    expect(find.byKey(const Key('receipt-previous-due')), findsOneWidget);
    expect(find.byKey(const Key('receipt-previous-collected')), findsOneWidget);
    expect(find.textContaining('SO-1'), findsOneWidget);
    expect(tester.widget<Text>(find.byKey(const Key('receipt-total-received'))).data, contains('1,500.00'));
    expect(tester.widget<Text>(find.byKey(const Key('receipt-balance-due'))).data, contains('0.00'));
  });

  testWidgets('left off: the bill alone, and nothing is sent', (tester) async {
    final backend = _Backend();
    await _pump(tester, backend);
    await tester.tap(find.byKey(const Key('pay-button')));
    await _settle(tester);
    await tester.tap(find.byKey(const Key('previous-due-keep')));
    await _settle(tester, rounds: 5);
    expect(_due(tester), contains('1,000.00'));
    expect(find.byKey(const Key('bill-plus-previous')), findsNothing);
    await tester.tap(find.byKey(const Key('complete-sale')));
    await _settle(tester);
    expect(backend.checkoutBody!.containsKey('previous_due'), isFalse);
    expect((backend.checkoutBody!['payments'] as List).single, containsPair('amount', 1000));
    expect(find.byKey(const Key('receipt-previous-due')), findsNothing);
  });

  testWidgets('part of it collected: the amount typed, and the rest stays owed', (tester) async {
    final backend = _Backend();
    await _pump(tester, backend);
    await tester.tap(find.byKey(const Key('pay-button')));
    await _settle(tester);
    await tester.tap(find.byKey(const Key('previous-due-change')));
    await _settle(tester, rounds: 5);
    await tester.enterText(find.byType(TextField).last, '200');
    await tester.tap(find.text('Apply').last);
    await _settle(tester, rounds: 5);
    expect(_due(tester), contains('1,200.00'));
    expect(find.textContaining('Collecting'), findsOneWidget);
    await tester.tap(find.byKey(const Key('complete-sale')));
    await _settle(tester);
    expect(backend.checkoutBody!['previous_due'], {'collect': 200});
    expect(tester.widget<Text>(find.byKey(const Key('receipt-balance-due'))).data, contains('300.00'));
  });

  testWidgets('credit can’t pay the previous outstanding', (tester) async {
    final backend = _Backend();
    await _pump(tester, backend);
    await tester.tap(find.byKey(const Key('pay-button')));
    await _settle(tester);
    await tester.tap(find.text('Credit'));
    await _settle(tester, rounds: 5);
    expect(find.byKey(const Key('previous-due-credit')), findsOneWidget);
    expect(tester.widget<FilledButton>(find.byKey(const Key('complete-sale'))).onPressed, isNull);
    // Left off, credit pays the bill as before.
    await tester.tap(find.byKey(const Key('previous-due-keep')));
    await _settle(tester, rounds: 5);
    expect(find.byKey(const Key('previous-due-credit')), findsNothing);
    expect(tester.widget<FilledButton>(find.byKey(const Key('complete-sale'))).onPressed, isNotNull);
  });

  testWidgets('a walk-in sees no previous outstanding and the server isn’t asked', (tester) async {
    final backend = _Backend();
    await _pump(tester, backend, withCustomer: false);
    await tester.tap(find.byKey(const Key('pay-button')));
    await _settle(tester);
    expect(find.byKey(const Key('previous-due')), findsNothing);
    expect(backend.calls.where((c) => c.contains('previous-due')), isEmpty);
    expect(_due(tester), contains('1,000.00'));
  });
}
