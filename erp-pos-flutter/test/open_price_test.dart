// Phase 13 open-price items on the POS. Offline: the device enforces the
// range from its cached catalog and refuses prices that need approval.
// Online: the server's OPEN_PRICE_REQUIRED (with the range in `details`)
// opens the keypad for an item whose lookup didn't carry a range (a search
// hit), and a failed approval reopens it with the message.
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
import 'package:erp_pos_app/screens/pos_screen.dart';
import 'package:erp_pos_app/state/session.dart';
import 'package:erp_pos_app/ui/open_price_dialog.dart';
import 'package:erp_pos_app/ui/theme.dart';

AppSession _session(http.Client client, {bool offline = false}) {
  final s = AppSession(
    api: ApiClient(baseUrl: 'http://pos-test.invalid', httpClient: client),
    branchId: 'branch-test',
    posTerminalId: 'terminal-test',
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

http.Response _json(int code, Object body) =>
    http.Response(jsonEncode(body), code, headers: {'content-type': 'application/json; charset=utf-8'});

Map<String, dynamic> _order(List<Map<String, dynamic>> lines, String total) => {
      'order_id': 'order-1',
      'order_number': 'MGR-1',
      'status': 'cart',
      'channel': 'pos',
      'subtotal': total,
      'discount_total': '0.00',
      'tax_total': '0.00',
      'grand_total': total,
      'lines': lines,
    };

void main() {
  setUpAll(() async {
    sqfliteFfiInit();
    databaseFactory = databaseFactoryFfi;
    await databaseFactory.setDatabasesPath(Directory.systemTemp.createTempSync('pos_open_price_test').path);
    await OfflineStore().cacheCatalog([
      {
        'barcode': '2900000000017',
        'product_id': 'prod-alt',
        'product_name': 'Bat knocking service',
        'hsn_code': '9987',
        'variant_id': 'var-alt',
        'sku': 'SVC-KNOCK',
        'selling_price': '120.00',
        'mrp': '500.00',
        'cgst_rate': 9.0,
        'sgst_rate': 9.0,
        'igst_rate': 0.0,
        'cess_rate': 0.0,
        'price_includes_tax': true,
        'open_price': {'min': '10.00', 'max': '500.00', 'approval_above': '200.00'},
      },
    ]);
  });

  testWidgets('offline: scanning an open-price item asks for the price and enforces the range', (tester) async {
    tester.view.physicalSize = const Size(1280, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final session = _session(MockClient((_) async => throw const SocketException('offline (test)')), offline: true);
    await tester.pumpWidget(MaterialApp(
      theme: posTheme(Brightness.light),
      home: ChangeNotifierProvider<AppSession>.value(value: session, child: const PosScreen()),
    ));
    await _settle(tester);

    await tester.enterText(find.byKey(const Key('scan-field')), '2900000000017');
    await tester.testTextInput.receiveAction(TextInputAction.done);
    await _settle(tester);
    expect(find.byType(OpenPriceDialog), findsOneWidget);
    expect(find.textContaining('Open price · ₹10.00 to ₹500.00'), findsOneWidget);

    final add = find.byKey(const Key('open-price-add'));
    await tester.enterText(find.byKey(const Key('open-price-field')), '600');
    await tester.pump();
    expect(find.textContaining('Must be between'), findsOneWidget);
    expect(tester.widget<FilledButton>(add).onPressed, isNull);

    await tester.enterText(find.byKey(const Key('open-price-field')), '250');
    await tester.pump();
    expect(find.textContaining('which needs a connection'), findsOneWidget, reason: 'no approval offline');
    expect(tester.widget<FilledButton>(add).onPressed, isNull);

    await tester.tap(find.text('Use suggested ₹120.00'));
    await tester.pump();
    expect(find.text('Add at ₹120.00'), findsOneWidget);
    await tester.tap(add);
    await _settle(tester);
    await tester.pump(const Duration(milliseconds: 400)); // dialog exit transition

    expect(find.byType(OpenPriceDialog), findsNothing);
    final line = session.currentOrder!.lines.single;
    expect(line.unitPrice, '120.00');
    expect(line.openPrice, isTrue);
    expect(find.textContaining('open price ₹120.00'), findsOneWidget);
    session.dispose();
  });

  testWidgets('online: OPEN_PRICE_REQUIRED opens the keypad; a failed approval reopens it with the message', (tester) async {
    final lineBodies = <Map<String, dynamic>>[];
    var approvals = 0;
    final client = MockClient((r) async {
      if (r.method == 'POST' && r.url.path == '/api/v1/sales/orders') return _json(201, _order([], '0.00'));
      if (r.method == 'POST' && r.url.path == '/api/v1/sales/orders/order-1/lines') {
        final body = jsonDecode(r.body) as Map<String, dynamic>;
        lineBodies.add(body);
        if (body['unit_price'] == null) {
          return _json(422, {
            'error': {
              'code': 'OPEN_PRICE_REQUIRED',
              'message': 'enter the price',
              'details': {
                'open_price': {'min': '10.00', 'max': '500.00', 'approval_above': '200.00'},
                'suggested_price': '120.00',
              }
            }
          });
        }
        if (body['approver_pin'] != '1234') {
          approvals++;
          return _json(403, {
            'error': {'code': 'OPEN_PRICE_NOT_APPROVED', 'message': 'needs approval'}
          });
        }
        return _json(200, _order([
          {
            'line_id': 'l1',
            'variant_id': 'var-alt',
            'sku': 'SVC-KNOCK',
            'product_name': 'Bat knocking service',
            'quantity': '1.000',
            'unit_price': '250.00',
            'discount_amount': '0.00',
            'tax_amount': '38.14',
            'line_total': '250.00',
            'taxable_value': '211.86',
            'price_includes_tax': true,
            'open_price': true,
            'price_approved_by': 'Arjun',
          }
        ], '250.00'));
      }
      return _json(404, {
        'error': {'code': 'NOT_FOUND', 'message': 'unscripted ${r.method} ${r.url.path}'}
      });
    });
    tester.view.physicalSize = const Size(1280, 1100);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final session = _session(client);
    // A search hit: no range known on the device.
    final product = ProductLookup(
        productId: 'prod-alt', productName: 'Bat knocking service', variantId: 'var-alt', sku: 'SVC-KNOCK', sellingPrice: '120.00', mrp: '500.00');
    bool? result;
    await tester.pumpWidget(MaterialApp(
      theme: posTheme(Brightness.light),
      home: Scaffold(
        body: Builder(
          builder: (context) => FilledButton(
            onPressed: () async => result = await addProductToCart(context, session, product, 1),
            child: const Text('Add'),
          ),
        ),
      ),
    ));
    await tester.tap(find.text('Add'));
    await _settle(tester);
    expect(find.byType(OpenPriceDialog), findsOneWidget, reason: 'the server said the item needs a price');

    await tester.enterText(find.byKey(const Key('open-price-field')), '250');
    await tester.pump();
    await tester.enterText(find.byKey(const Key('open-price-approver')), 'EMP003');
    for (final d in ['9', '9', '9', '9']) {
      await tester.tap(find.text(d).last);
    }
    await tester.pump();
    await tester.tap(find.byKey(const Key('open-price-add')));
    await _settle(tester);
    await tester.pump(const Duration(milliseconds: 400));

    expect(approvals, 1);
    expect(find.byType(OpenPriceDialog), findsOneWidget, reason: 'reopened for another try');
    expect(find.byKey(const Key('open-price-error')), findsOneWidget);
    expect(tester.widget<TextField>(find.byKey(const Key('open-price-field'))).controller!.text, '250', reason: 'keeps the price');

    for (final d in ['1', '2', '3', '4']) {
      await tester.tap(find.text(d).last);
    }
    await tester.pump();
    await tester.tap(find.byKey(const Key('open-price-add')));
    await _settle(tester);
    await tester.pump(const Duration(milliseconds: 400));

    expect(result, isTrue);
    expect(find.byType(OpenPriceDialog), findsNothing);
    expect(session.currentOrder!.lines.single.openPrice, isTrue);
    final last = lineBodies.last;
    expect(last['unit_price'], 250.0);
    expect(last['approver_code'], 'EMP003');
    expect(last['approver_pin'], '1234');
    session.dispose();
  });
}
