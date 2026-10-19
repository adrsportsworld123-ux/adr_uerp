// The POS grid (erp-core-go migrations/063): a branch can list open-price
// items first, and a tile carries the product's own colour, or the
// standard open-price colour, with an "Open price" label. Offline (the
// session's grid source with no network), so the order comes from the
// on-device catalog — the online order is the server's (tested there).
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/testing.dart';
import 'package:provider/provider.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import 'package:erp_pos_app/api/api_client.dart';
import 'package:erp_pos_app/local/offline_store.dart';
import 'package:erp_pos_app/screens/pos_screen.dart';
import 'package:erp_pos_app/state/session.dart';
import 'package:erp_pos_app/ui/theme.dart';

Map<String, dynamic> _product(String id, String name, {bool open = false, String? color}) => {
      'barcode': '89000000000$id',
      'product_id': 'prod-$id',
      'product_name': name,
      'hsn_code': '9506',
      'variant_id': 'var-$id',
      'sku': 'SKU-$id',
      'selling_price': '100.00',
      'mrp': '100.00',
      'cgst_rate': 9.0,
      'sgst_rate': 9.0,
      'igst_rate': 0.0,
      'cess_rate': 0.0,
      if (open) 'open_price': {'min': '1.00', 'max': '5000.00'},
      if (color != null) 'pos_color': color,
    };

Future<void> _settle(WidgetTester tester, {int rounds = 8}) async {
  for (var i = 0; i < rounds; i++) {
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 25)));
    await tester.pump(const Duration(milliseconds: 16));
  }
}

Future<AppSession> _pump(WidgetTester tester, {required bool openPriceFirst}) async {
  tester.view.physicalSize = const Size(1280, 800);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final api = ApiClient(baseUrl: 'http://pos-test.invalid', httpClient: MockClient((_) async => throw const SocketException('offline (test)')));
  final s = AppSession(api: api, branchId: 'b', posTerminalId: 't', terminalName: 'Counter', branchName: 'Branch', merchantCode: 'm');
  s.userId = 'u';
  s.offlineMode = true;
  s.openPriceFirst = openPriceFirst;
  await tester.pumpWidget(MaterialApp(
    theme: posTheme(Brightness.light),
    home: ChangeNotifierProvider<AppSession>.value(value: s, child: const PosScreen()),
  ));
  await _settle(tester);
  return s;
}

List<String> _tileNames(WidgetTester tester) => tester
    .widgetList<Semantics>(find.byWidgetPredicate((w) => w is Semantics && (w.properties.label ?? '').endsWith('Add to cart.')))
    .map((s) => s.properties.label!.split(',').first)
    .toList();

void main() {
  setUpAll(() async {
    sqfliteFfiInit();
    databaseFactory = databaseFactoryFfi;
    await databaseFactory.setDatabasesPath(Directory.systemTemp.createTempSync('pos_grid_test').path);
    await OfflineStore().cacheCatalog([
      _product('1', 'Arrow Bat'),
      _product('2', 'Bat Engraving', open: true),
      _product('3', 'Cap', color: '#0EA5E9'),
      _product('4', 'Custom Jersey Print', open: true, color: '#F59E0B'),
    ]);
  });

  testWidgets('as today: the grid is in name order', (tester) async {
    await _pump(tester, openPriceFirst: false);
    expect(_tileNames(tester), ['Arrow Bat', 'Bat Engraving', 'Cap', 'Custom Jersey Print']);
  });

  testWidgets('a branch that lists open-price items first, coloured and labelled', (tester) async {
    await _pump(tester, openPriceFirst: true);
    expect(_tileNames(tester), ['Bat Engraving', 'Custom Jersey Print', 'Arrow Bat', 'Cap']);
    expect(find.byKey(const Key('open-price-badge')), findsNWidgets(2));
    // Every coloured tile: the two open-price items and the product with its own colour.
    expect(find.byKey(const Key('tile-accent-var-2')), findsOneWidget);
    expect(find.byKey(const Key('tile-accent-var-3')), findsOneWidget);
    expect(find.byKey(const Key('tile-accent-var-4')), findsOneWidget);
    expect(find.byKey(const Key('tile-accent-var-1')), findsNothing, reason: 'a plain product keeps the plain tile');
  });

  test('tile colour: the product colour wins, else the open-price default', () {
    GridProduct g({String? color, bool open = false}) => GridProduct(
        lookup: ProductLookup(productId: 'p', productName: 'n', variantId: 'v', sku: 's', sellingPrice: '1', mrp: '1', posColor: color),
        categoryName: null,
        available: null,
        openPrice: open);
    expect(tileAccent(g(), PosColors.light), isNull);
    expect(tileAccent(g(open: true), PosColors.light), PosColors.light.plum);
    expect(tileAccent(g(open: true, color: '#F59E0B'), PosColors.light), const Color(0xFFF59E0B));
    expect(tileAccent(g(color: '#0EA5E9'), PosColors.light), const Color(0xFF0EA5E9));
  });
}
