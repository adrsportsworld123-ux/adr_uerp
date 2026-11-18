// A sale goes back to a walk-in (no customer) from the customer picker (F4)
// or the payment step (erp-core-go DELETE /sales/orders/{id}/customer);
// when the customer's loyalty points come off, the cart's discounts do too
// and the till says so. Online against a mocked API.
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
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

Map<String, Object?> _orderJson({required String discount, bool cleared = false}) => {
      'order_id': 'o1',
      'order_number': 'SO-1',
      'status': 'cart',
      'subtotal': '1000.00',
      'discount_total': discount,
      'tax_total': '0.00',
      'grand_total': (1000 - double.parse(discount)).toStringAsFixed(2),
      'discounts_cleared': cleared,
      'lines': [
        {
          'line_id': 'l1', 'variant_id': 'v1', 'sku': 'BAT', 'product_name': 'Willow Bat', 'quantity': '1', 'unit_price': '1000.00',
          'discount_amount': discount, 'tax_amount': '0.00', 'line_total': (1000 - double.parse(discount)).toStringAsFixed(2),
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
  bool clearsDiscounts = true;

  late final client = MockClient((req) async {
    final p = req.url.path;
    calls.add('${req.method} $p');
    if (p.endsWith('/sales/orders/o1/customer') && req.method == 'DELETE') {
      return _json(_orderJson(discount: clearsDiscounts ? '0.00' : '10.00', cleared: clearsDiscounts));
    }
    if (p.endsWith('/sales/orders/o1/customer') && req.method == 'POST') {
      return _json({'order_id': 'o1', 'customer_id': 'c2', 'discounts_cleared': true});
    }
    if (p.endsWith('/sales/orders/o1') && req.method == 'GET') return _json(_orderJson(discount: '0.00'));
    if (p.endsWith('/customers')) {
      return _json({
        'customers': [
          {'customer_id': 'c2', 'name': 'Other Buyer', 'phone': '9000000002'},
        ],
      });
    }
    if (p.contains('/loyalty')) return _json({'customer_id': 'c2', 'available_points': 0, 'balance': 0});
    if (p.endsWith('/categories')) return _json({'categories': []});
    if (p.endsWith('/products/search')) return _json({'results': []});
    return _json({'error': {'code': 'NOT_FOUND', 'message': 'not in this test'}}, 404);
  });
}

Future<AppSession> _pump(WidgetTester tester, _Backend backend, {bool withCustomer = true}) async {
  tester.view.physicalSize = const Size(1280, 800);
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
  s.currentOrder = OrderSummary.fromJson(_orderJson(discount: '10.00'));
  if (withCustomer) {
    s.attachedCustomerId = 'c1';
    s.attachedCustomerName = 'Kumar';
    s.loyaltyAvailablePoints = 400;
  }
  await tester.pumpWidget(MaterialApp(
    theme: posTheme(Brightness.light),
    home: ChangeNotifierProvider<AppSession>.value(value: s, child: const PosScreen()),
  ));
  await _settle(tester, rounds: 10);
  return s;
}

void main() {
  setUpAll(() async {
    sqfliteFfiInit();
    databaseFactory = databaseFactoryFfi;
    await databaseFactory.setDatabasesPath(Directory.systemTemp.createTempSync('walk_in_customer_test').path);
  });

  testWidgets('the payment step clears the customer; the points and discounts come off', (tester) async {
    final backend = _Backend();
    final s = await _pump(tester, backend);
    await tester.tap(find.byKey(const Key('pay-button')));
    await _settle(tester);
    expect(find.text('Kumar'), findsWidgets);
    await tester.tap(find.byKey(const Key('payment-clear-customer')));
    await _settle(tester);
    expect(backend.calls, contains('DELETE /api/v1/sales/orders/o1/customer'));
    expect(s.attachedCustomerId, isNull);
    expect(s.loyaltyAvailablePoints, isNull);
    expect(s.currentOrder!.discountTotal, '0.00');
    expect(find.text('No customer'), findsOneWidget);
    expect(find.text('The customer’s points and discounts came off. Apply discounts again before payment.'), findsOneWidget);
    expect(find.byKey(const Key('payment-clear-customer')), findsNothing, reason: 'nothing left to clear');
  });

  testWidgets('F4 offers “Clear customer (walk-in)” only when there is one', (tester) async {
    final backend = _Backend()..clearsDiscounts = false;
    final s = await _pump(tester, backend);
    await tester.sendKeyEvent(LogicalKeyboardKey.f4);
    await _settle(tester);
    expect(find.byKey(const Key('customer-clear')), findsOneWidget);
    await tester.tap(find.byKey(const Key('customer-clear')));
    await _settle(tester);
    expect(s.attachedCustomerId, isNull);
    expect(s.currentOrder!.discountTotal, '10.00', reason: 'a manual discount isn’t the customer’s: it stays');
    expect(find.text('Walk-in sale — customer cleared'), findsOneWidget);

    await tester.sendKeyEvent(LogicalKeyboardKey.f4);
    await _settle(tester);
    expect(find.text('Attach a customer'), findsOneWidget);
    expect(find.byKey(const Key('customer-clear')), findsNothing);
  });

  testWidgets('switching to another customer refreshes the cart when their points came off', (tester) async {
    final backend = _Backend();
    final s = await _pump(tester, backend);
    final ok = await tester.runAsync(() => s.attachCustomer(customerId: 'c2', name: 'Other Buyer'));
    expect(ok, isTrue);
    expect(s.attachedCustomerId, 'c2');
    expect(s.customerChangeClearedDiscounts, isTrue);
    expect(backend.calls, contains('GET /api/v1/sales/orders/o1'));
    expect(s.currentOrder!.discountTotal, '0.00');
  });

  testWidgets('a sale without a customer clears nothing and calls nothing', (tester) async {
    final backend = _Backend();
    final s = await _pump(tester, backend, withCustomer: false);
    final ok = await tester.runAsync(() => s.clearCustomer());
    expect(ok, isTrue);
    expect(backend.calls.where((c) => c.contains('/customer')), isEmpty);
  });
}
