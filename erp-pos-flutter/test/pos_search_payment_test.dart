// The full product search opens beside the cart (never over it), shows
// each hit's MRP and closes with Esc; the payment step can add a customer.
// Online against a mocked API: only search, categories and the order
// endpoints answer, everything else is a plain 404.
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

final _hit = {
  'variant_id': 'v1',
  'product_id': 'p1',
  'name': 'SG Kashmir Willow Bat',
  'sku': 'CRK-BAT',
  'category_name': 'Cricket',
  'selling_price': 899,
  'mrp': 999,
};

Future<void> _settle(WidgetTester tester, {int rounds = 10}) async {
  for (var i = 0; i < rounds; i++) {
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 25)));
    await tester.pump(const Duration(milliseconds: 16));
  }
}

Future<AppSession> _pump(WidgetTester tester, {Size size = const Size(1280, 800)}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final api = ApiClient(
    baseUrl: 'http://pos-test.invalid',
    httpClient: MockClient((req) async {
      final p = req.url.path;
      if (p.endsWith('/products/search')) return _json({'results': [_hit]});
      if (p.endsWith('/categories')) return _json({'categories': []});
      return _json({'error': {'code': 'NOT_FOUND', 'message': 'not in this test'}}, 404);
    }),
  );
  final s = AppSession(api: api, branchId: 'b', posTerminalId: 't', terminalName: 'Counter', branchName: 'Branch', merchantCode: 'm');
  s.userId = 'u';
  s.currentOrder = OrderSummary(
    orderId: 'o1',
    orderNumber: 'SO-1',
    status: 'cart',
    subtotal: '899.00',
    discountTotal: '0.00',
    taxTotal: '0.00',
    grandTotal: '899.00',
    lines: [
      OrderLine(
          lineId: 'l1', variantId: 'v1', sku: 'CRK-BAT', productName: 'SG Kashmir Willow Bat', quantity: '1', unitPrice: '899.00',
          discountAmount: '0.00', taxAmount: '0.00', lineTotal: '899.00', mrp: '999.00', mrpDiscount: '100.00'),
    ],
  );
  await tester.pumpWidget(MaterialApp(
    theme: posTheme(Brightness.light),
    home: ChangeNotifierProvider<AppSession>.value(value: s, child: const PosScreen()),
  ));
  await _settle(tester);
  return s;
}

void main() {
  setUpAll(() async {
    sqfliteFfiInit();
    databaseFactory = databaseFactoryFfi;
    await databaseFactory.setDatabasesPath(Directory.systemTemp.createTempSync('pos_search_payment_test').path);
  });

  testWidgets('the full search opens beside the cart, shows the MRP and closes with Esc', (tester) async {
    await _pump(tester);
    expect(find.byKey(const Key('tile-mrp-v1')), findsOneWidget, reason: 'the grid tile shows the MRP');

    await tester.tap(find.byKey(const Key('open-product-search')));
    await _settle(tester);
    expect(find.byKey(const Key('embedded-product-search')), findsOneWidget);
    expect(find.byKey(const Key('search-mrp-v1')), findsOneWidget);
    expect(find.text('MRP ₹999.00'), findsOneWidget);
    // The cart is still there: its line and the Pay button.
    expect(find.byKey(const Key('pay-button')), findsOneWidget);
    expect(find.byKey(const Key('cart-line-l1-detail')), findsOneWidget);

    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await _settle(tester);
    expect(find.byKey(const Key('embedded-product-search')), findsNothing);
    expect(find.byKey(const Key('scan-field')), findsOneWidget);

    // F3 opens it again.
    await tester.sendKeyEvent(LogicalKeyboardKey.f3);
    await _settle(tester);
    expect(find.byKey(const Key('embedded-product-search')), findsOneWidget);
    await tester.tap(find.byKey(const Key('close-product-search')));
    await _settle(tester);
    expect(find.byKey(const Key('embedded-product-search')), findsNothing);
  });

  testWidgets('the payment step adds a customer', (tester) async {
    await _pump(tester);
    await tester.tap(find.byKey(const Key('pay-button')));
    await _settle(tester);
    expect(find.byKey(const Key('payment-customer')), findsOneWidget);
    expect(find.text('No customer'), findsOneWidget);
    await tester.tap(find.byKey(const Key('payment-add-customer')));
    await _settle(tester);
    expect(find.text('Attach a customer'), findsOneWidget, reason: 'the same picker as the cart’s customer button');
  });

  for (final size in const [Size(390, 844), Size(800, 1280)]) {
    testWidgets('the search fits a ${size.width.toInt()} px screen, with the cart still reachable', (tester) async {
      await _pump(tester, size: size);
      await tester.tap(find.byKey(const Key('open-product-search')));
      await _settle(tester);
      expect(find.byKey(const Key('search-mrp-v1')), findsOneWidget);
      expect(find.byKey(size.width < 640 ? const Key('phone-cart-bar') : const Key('pay-button')), findsOneWidget);
    });
  }
}
