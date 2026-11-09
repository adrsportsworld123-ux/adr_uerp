// Sales history shows each sale's payment status and what is still due,
// collects a credit payment at the till (erp-core-go migrations/082), and
// the full product search shows this branch's stock. Online against a
// mocked API.
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:provider/provider.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import 'package:erp_pos_app/api/api_client.dart';
import 'package:erp_pos_app/screens/orders_screen.dart';
import 'package:erp_pos_app/screens/pos_screen.dart';
import 'package:erp_pos_app/state/session.dart';
import 'package:erp_pos_app/ui/theme.dart';

http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json'});

Map<String, Object?> _order(String number, {required String status, required String pending, String credit = '0.00'}) => {
      'order_id': 'id-$number',
      'order_number': number,
      'status': 'finalized',
      'finalized_at': DateTime.now().toUtc().toIso8601String(),
      'terminal_name': 'Counter',
      'cashier_name': 'Ravi',
      'customer_name': 'Kumar',
      'item_count': '1',
      'grand_total': '1000.00',
      'payment_methods': [credit == '0.00' ? 'cash' : 'credit'],
      'payment_status': status,
      'pending_amount': pending,
      'credit_amount': credit,
    };

Future<void> _settle(WidgetTester tester, {int rounds = 10}) async {
  for (var i = 0; i < rounds; i++) {
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 25)));
    await tester.pump(const Duration(milliseconds: 16));
  }
}

AppSession _session(MockClient client) {
  final s = AppSession(
      api: ApiClient(baseUrl: 'http://pos-test.invalid', httpClient: client),
      branchId: 'b',
      posTerminalId: 't',
      terminalName: 'Counter',
      branchName: 'Branch',
      merchantCode: 'm');
  s.userId = 'u';
  return s;
}

void main() {
  setUpAll(() async {
    sqfliteFfiInit();
    databaseFactory = databaseFactoryFfi;
    await databaseFactory.setDatabasesPath(Directory.systemTemp.createTempSync('credit_collection_test').path);
  });

  testWidgets('history shows payment status and due, and collects a payment', (tester) async {
    tester.view.physicalSize = const Size(1280, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final listQueries = <Map<String, String>>[];
    Map<String, dynamic>? posted;
    var pending = '1000.00';
    final client = MockClient((req) async {
      final p = req.url.path;
      if (p.endsWith('/sales/orders') && req.method == 'GET') {
        listQueries.add(req.url.queryParameters);
        return _json({
          'orders': [
            _order('SO-CR', status: pending == '0.00' ? 'paid' : 'partially_paid', pending: pending, credit: '1000.00'),
            _order('SO-CASH', status: 'paid', pending: '0.00'),
          ],
          'total': 2,
        });
      }
      if (p.endsWith('/sales/summary')) {
        return _json({'order_count': 2, 'net_sales': '2000.00', 'pending_total': pending, 'pending_count': pending == '0.00' ? 0 : 1});
      }
      if (p.endsWith('/sales/orders/id-SO-CR/credit-payments') && req.method == 'POST') {
        posted = jsonDecode(req.body) as Map<String, dynamic>;
        if ((posted!['amount'] as num) > 1000) {
          return _json({'error': {'code': 'PAYMENT_EXCEEDS_BALANCE', 'message': 'too much'}}, 409);
        }
        pending = (1000 - (posted!['amount'] as num)).toStringAsFixed(2);
        return _json({'order_number': 'SO-CR', 'credit_paid': '400.00', 'pending_amount': pending, 'payment_status': 'partially_paid'});
      }
      return _json({'error': {'code': 'NOT_FOUND', 'message': 'not in this test'}}, 404);
    });
    final s = _session(client);
    await tester.pumpWidget(MaterialApp(
      theme: posTheme(Brightness.light),
      home: ChangeNotifierProvider<AppSession>.value(value: s, child: const OrdersScreen()),
    ));
    await _settle(tester);

    expect(find.descendant(of: find.byKey(const Key('payment-status-SO-CR')), matching: find.text('Partly paid')), findsOneWidget);
    expect(find.descendant(of: find.byKey(const Key('payment-status-SO-CASH')), matching: find.text('Paid')), findsOneWidget);
    expect(find.byKey(const Key('due-SO-CR')), findsOneWidget);
    expect(find.text('Due ₹1,000.00'), findsOneWidget);
    expect(find.byKey(const Key('due-SO-CASH')), findsNothing, reason: 'nothing is owed on a cash sale');
    expect(find.byKey(const Key('collect-SO-CASH')), findsNothing);
    expect(find.byKey(const Key('summary-pending')), findsOneWidget);

    await tester.tap(find.byKey(const Key('collect-SO-CR')));
    await _settle(tester);
    expect(find.byKey(const Key('collect-payment-dialog')), findsOneWidget);
    // More than is due: the button stays off and says why.
    await tester.enterText(find.byKey(const Key('collect-amount')), '1500');
    await tester.pump();
    expect(find.text('Enter an amount up to ₹1,000.00'), findsOneWidget);
    expect(tester.widget<FilledButton>(find.byKey(const Key('collect-submit'))).onPressed, isNull);

    await tester.enterText(find.byKey(const Key('collect-amount')), '400');
    await tester.pump();
    await tester.tap(find.byKey(const Key('collect-submit')));
    await _settle(tester, rounds: 30);
    expect(posted, isNotNull);
    expect(posted!['amount'], 400);
    expect(posted!['method'], 'cash');
    expect(posted!['pos_terminal_id'], 't', reason: 'the till names itself, so the cash counts in its drawer');
    expect((posted!['idempotency_key'] as String).isNotEmpty, isTrue);
    expect(find.byKey(const Key('collect-payment-dialog')), findsNothing);
    expect(find.text('Payment recorded · ₹600.00 still due'), findsOneWidget);
    expect(find.text('Due ₹600.00'), findsOneWidget, reason: 'the list reloads');

    // "Payment pending" over "All dates" asks for every owed sale.
    await tester.tap(find.text('All dates'));
    await _settle(tester);
    expect(listQueries.last.containsKey('from'), isFalse);
    await tester.tap(find.byKey(const Key('history-payment-filter')));
    await _settle(tester);
    await tester.tap(find.text('Payment pending').last);
    await _settle(tester);
    expect(listQueries.last['payment_status'], 'pending');
  });

  testWidgets('the full product search shows this branch stock', (tester) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    String? searchedBranch;
    Map<String, Object?> hit(String id, String name, String available) =>
        {'variant_id': id, 'product_id': 'p$id', 'name': name, 'sku': 'SKU-$id', 'selling_price': 100, 'mrp': 120, 'available': available};
    final client = MockClient((req) async {
      final p = req.url.path;
      if (p.endsWith('/products/search')) {
        searchedBranch = req.url.queryParameters['branch_id'];
        return _json({
          'results': [hit('v1', 'Plenty bat', '25.000'), hit('v2', 'Few ball', '3.000'), hit('v3', 'Gone cap', '0.000'), hit('v4', 'Below pad', '-2.000')]
        });
      }
      if (p.endsWith('/categories')) return _json({'categories': []});
      return _json({'error': {'code': 'NOT_FOUND', 'message': 'not in this test'}}, 404);
    });
    final s = _session(client);
    await tester.pumpWidget(MaterialApp(
      theme: posTheme(Brightness.light),
      home: ChangeNotifierProvider<AppSession>.value(value: s, child: const PosScreen()),
    ));
    await _settle(tester);
    await tester.tap(find.byKey(const Key('open-product-search')));
    await _settle(tester);
    expect(searchedBranch, 'b', reason: 'the search asks for this branch, so the stock is this branch');
    final results = find.byKey(const Key('embedded-product-search'));
    expect(find.descendant(of: results, matching: find.textContaining('25 in stock', findRichText: true)), findsOneWidget);
    expect(find.descendant(of: results, matching: find.textContaining('3 left', findRichText: true)), findsOneWidget);
    expect(find.descendant(of: results, matching: find.textContaining('Out of stock', findRichText: true)), findsNWidgets(2),
        reason: 'zero and below zero (a negative sale) both read as out of stock');
  });
}
