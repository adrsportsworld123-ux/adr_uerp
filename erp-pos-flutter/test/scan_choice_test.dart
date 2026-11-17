// One printed code, several things (erp-core-go migrations/083): a code on
// several colours of a product always asks; an item in several MRP lots
// takes the one in stock, and asks "Which MRP is on the pack?" when two or
// more are. Online (the server decides) and offline (the cached catalog,
// keyed by barcode and variant since local DB v9).
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:path/path.dart' as p;
import 'package:provider/provider.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import 'package:erp_pos_app/api/api_client.dart';
import 'package:erp_pos_app/local/local_db.dart';
import 'package:erp_pos_app/local/offline_store.dart';
import 'package:erp_pos_app/screens/pos_screen.dart';
import 'package:erp_pos_app/state/session.dart';
import 'package:erp_pos_app/ui/theme.dart';

http.Response _json(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json'});

ScanOption _opt(String id, {String? lotOf, double? available, String mrp = '999.00', String label = ''}) =>
    ScanOption(variantId: id, sku: 'SKU-$id', label: label, mrp: mrp, sellingPrice: mrp, mrpLotOf: lotOf, available: available);

Map<String, dynamic> _entry(String variant, {String? lotOf, String label = '', String mrp = '999.00', bool sellable = true}) => {
      'barcode': '8901234567897',
      'product_id': 'prod-polo',
      'product_name': 'Polo',
      'hsn_code': '6105',
      'variant_id': variant,
      'sku': 'SKU-$variant',
      'selling_price': mrp,
      'mrp': mrp,
      'cgst_rate': 2.5,
      'sgst_rate': 2.5,
      'igst_rate': 0.0,
      'cess_rate': 0.0,
      'label': label,
      'mrp_lot_of': lotOf,
      'sellable': sellable,
    };

Future<void> _settle(WidgetTester tester, {int rounds = 10}) async {
  for (var i = 0; i < rounds; i++) {
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 25)));
    await tester.pump(const Duration(milliseconds: 16));
  }
}

void main() {
  setUpAll(() async {
    sqfliteFfiInit();
    databaseFactory = databaseFactoryFfi;
    await databaseFactory.setDatabasesPath(Directory.systemTemp.createTempSync('scan_choice_test').path);
  });

  group('resolveScanOptions (the server rule, for offline scans)', () {
    test('different colours on one code always ask', () {
      final r = resolveScanOptions([_opt('red', available: 5, label: 'Red'), _opt('blue', available: 0, label: 'Blue')]);
      expect(r.choiceRequired, isTrue);
      expect(r.options.map((o) => o.variantId), ['red', 'blue']);
    });
    test('MRP lots: the one in stock is taken', () {
      final r = resolveScanOptions([_opt('bat', available: 0), _opt('lot', lotOf: 'bat', available: 4, mrp: '1099.00')]);
      expect(r.choiceRequired, isFalse);
      expect(r.chosen.variantId, 'lot');
    });
    test('MRP lots: two in stock ask, offering only those', () {
      final r = resolveScanOptions([
        _opt('bat', available: 2),
        _opt('lot1', lotOf: 'bat', available: 0, mrp: '1099.00'),
        _opt('lot2', lotOf: 'bat', available: 3, mrp: '1199.00'),
      ]);
      expect(r.choiceRequired, isTrue);
      expect(r.options.map((o) => o.variantId), ['bat', 'lot2']);
    });
    test('MRP lots: none in stock takes the oldest', () {
      final r = resolveScanOptions([_opt('bat', available: 0), _opt('lot', lotOf: 'bat', available: 0)]);
      expect(r.choiceRequired, isFalse);
      expect(r.chosen.variantId, 'bat');
    });
  });

  test('ProductLookup reads options and withOption takes the pick', () {
    final l = ProductLookup.fromJson({
      'product_id': 'p',
      'product_name': 'Polo',
      'variant_id': 'red',
      'sku': 'SKU-red',
      'selling_price': '699.00',
      'mrp': '799.00',
      'choice_required': true,
      'options': [
        {'variant_id': 'red', 'sku': 'SKU-red', 'label': 'Red', 'mrp': '799.00', 'selling_price': '699.00', 'mrp_lot_of': null, 'available': '5.000'},
        {'variant_id': 'blue', 'sku': 'SKU-blue', 'label': 'Blue', 'mrp': '799.00', 'selling_price': '649.00', 'mrp_lot_of': null, 'available': null},
      ],
    });
    expect(l.choiceRequired, isTrue);
    expect(l.options, hasLength(2));
    expect(l.options.first.available, 5);
    final b = l.withOption(l.options[1]);
    expect([b.variantId, b.sku, b.sellingPrice, b.productName], ['blue', 'SKU-blue', '649.00', 'Polo (Blue)']);
  });

  test('offline catalog keeps every variant under one code (v9)', () async {
    await LocalDb.instance.db;
    final store = OfflineStore();
    await store.cacheCatalog([
      _entry('red', label: 'Red'),
      _entry('blue', label: 'Blue'),
      _entry('red-1099', lotOf: 'red', label: 'Red', mrp: '1099.00'),
      _entry('green', label: 'Green', sellable: false),
    ]);
    final all = await store.lookupBarcodeAll('8901234567897');
    expect(all.map((i) => i.product.lookup.variantId).toList(), ['blue', 'red', 'red-1099'],
        reason: 'sellable only, each variant followed by its lots');
    expect(all.last.product.mrpLotOf, 'red');
    expect((await store.lookupBarcode('8901234567897'))!.lookup.variantId, 'blue');
  });

  test('a v8 cache upgrades to v9 with its rows kept and a full re-pull', () async {
    final dir = Directory.systemTemp.createTempSync('scan_choice_v8');
    final path = p.join(dir.path, 'erp_pos_offline.db');
    final old = await databaseFactory.openDatabase(path,
        options: OpenDatabaseOptions(
          version: 8,
          onCreate: (db, _) async {
            await db.execute('CREATE TABLE catalog (barcode TEXT PRIMARY KEY, product_id TEXT NOT NULL, product_name TEXT NOT NULL, hsn_code TEXT, '
                'variant_id TEXT NOT NULL, sku TEXT NOT NULL, selling_price TEXT NOT NULL, mrp TEXT NOT NULL, cgst_rate REAL NOT NULL, '
                'sgst_rate REAL NOT NULL, igst_rate REAL NOT NULL, cess_rate REAL NOT NULL, price_includes_tax INTEGER NOT NULL DEFAULT 0, '
                'open_price_min TEXT, open_price_max TEXT, open_price_approval_above TEXT, pos_color TEXT, pack_qty REAL NOT NULL DEFAULT 1, '
                'unit_symbol TEXT, unit_decimals INTEGER NOT NULL DEFAULT 3)');
            await db.execute('CREATE TABLE sync_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)');
            await db.insert('catalog', {
              'barcode': '1', 'product_id': 'p', 'product_name': 'Old', 'variant_id': 'v', 'sku': 'S', 'selling_price': '1.00', 'mrp': '1.00',
              'cgst_rate': 0.0, 'sgst_rate': 0.0, 'igst_rate': 0.0, 'cess_rate': 0.0,
            });
            await db.insert('sync_meta', {'key': 'last_pull_at', 'value': '2026-01-01T00:00:00Z'});
          },
        ));
    await old.close();
    final upgraded = await LocalDb.openAt(path);
    final rows = await upgraded.query('catalog');
    expect(rows.single['variant_id'], 'v');
    expect(rows.single['sellable'], 1);
    expect(await upgraded.query('sync_meta', where: "key = 'last_pull_at'"), isEmpty, reason: 'the next pull is a full one');
    await upgraded.insert('catalog', {...rows.single, 'variant_id': 'v2'});
    expect(await upgraded.query('catalog', where: "barcode = '1'"), hasLength(2), reason: 'two variants under one code');
    await upgraded.close();
  });

  testWidgets('scanning a code on two colours asks which, and adds the one picked', (tester) async {
    tester.view.physicalSize = const Size(1280, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    Map<String, dynamic>? added;
    String? scannedBranch;
    final api = ApiClient(
      baseUrl: 'http://pos-test.invalid',
      httpClient: MockClient((req) async {
        final path = req.url.path;
        if (path.contains('/products/barcode/')) {
          scannedBranch = req.url.queryParameters['branch_id'];
          return _json({
            'product_id': 'p',
            'product_name': 'Polo',
            'variant_id': 'red',
            'sku': 'SKU-red',
            'selling_price': '699.00',
            'mrp': '799.00',
            'pack_qty': '1.000',
            'choice_required': true,
            'options': [
              {'variant_id': 'red', 'sku': 'SKU-red', 'label': 'Red', 'mrp': '799.00', 'selling_price': '699.00', 'available': '5.000'},
              {'variant_id': 'blue', 'sku': 'SKU-blue', 'label': 'Blue', 'mrp': '799.00', 'selling_price': '649.00', 'available': '2.000'},
            ],
          });
        }
        if (path.endsWith('/sales/orders/o1/lines') && req.method == 'POST') {
          added = jsonDecode(req.body) as Map<String, dynamic>;
          return _json({
            'order_id': 'o1',
            'order_number': 'SO-1',
            'status': 'cart',
            'subtotal': '649.00',
            'discount_total': '0.00',
            'tax_total': '0.00',
            'grand_total': '649.00',
            'lines': [
              {
                'line_id': 'l1', 'variant_id': 'blue', 'sku': 'SKU-blue', 'product_name': 'Polo', 'quantity': '1', 'unit_price': '649.00',
                'discount_amount': '0.00', 'tax_amount': '0.00', 'line_total': '649.00',
              }
            ],
          });
        }
        if (path.endsWith('/categories')) return _json({'categories': []});
        if (path.endsWith('/products/search')) return _json({'results': []});
        return _json({'error': {'code': 'NOT_FOUND', 'message': 'not in this test'}}, 404);
      }),
    );
    final s = AppSession(api: api, branchId: 'b', posTerminalId: 't', terminalName: 'Counter', branchName: 'Branch', merchantCode: 'm');
    s.userId = 'u';
    s.currentOrder = OrderSummary(
        orderId: 'o1', orderNumber: 'SO-1', status: 'cart', subtotal: '0.00', discountTotal: '0.00', taxTotal: '0.00', grandTotal: '0.00', lines: []);
    await tester.pumpWidget(MaterialApp(
      theme: posTheme(Brightness.light),
      home: ChangeNotifierProvider<AppSession>.value(value: s, child: const PosScreen()),
    ));
    await _settle(tester);
    await tester.enterText(find.byKey(const Key('scan-field')), '8901234567897');
    await tester.testTextInput.receiveAction(TextInputAction.done);
    await _settle(tester);
    expect(scannedBranch, 'b', reason: 'the till names its branch');
    expect(find.byKey(const Key('scan-choice')), findsOneWidget);
    expect(find.text('Which one is it?'), findsOneWidget);
    expect(added, isNull, reason: 'nothing is added before the cashier picks');
    await tester.tap(find.byKey(const Key('scan-option-blue')));
    await _settle(tester, rounds: 30);
    expect(added?['variant_id'], 'blue');
    expect(find.byKey(const Key('scan-choice')), findsNothing);
    // Cancelling adds nothing.
    added = null;
    await tester.enterText(find.byKey(const Key('scan-field')), '8901234567897');
    await tester.testTextInput.receiveAction(TextInputAction.done);
    await _settle(tester);
    await tester.tap(find.text('Cancel'));
    await _settle(tester, rounds: 30);
    expect(added, isNull);
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
  });
}
