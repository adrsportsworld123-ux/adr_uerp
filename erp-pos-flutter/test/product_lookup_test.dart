// Phase 13 product lookup at the till, against a scripted server with
// erp-core-go's shapes (GET /products/{id}, GET /inventory/variants/{id}/
// branches, GET /me/permissions, PATCH /pricing/variants/{id}, POST
// /inventory/adjustments, GET /products/variants/{id}/label).
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:erp_pos_app/api/api_client.dart';
import 'package:erp_pos_app/screens/product_lookup_screen.dart';
import 'package:erp_pos_app/state/session.dart';
import 'package:erp_pos_app/ui/theme.dart';

class _Server {
  final Set<String> perms;
  final requests = <http.Request>[];
  String price = '80.00';
  _Server(this.perms);

  http.Response json(int code, Object body) => http.Response(jsonEncode(body), code, headers: {'content-type': 'application/json; charset=utf-8'});

  Future<http.Response> handle(http.Request r) async {
    requests.add(r);
    final path = r.url.path;
    if (path == '/api/v1/me/permissions') return json(200, {'permissions': perms.toList()});
    if (path == '/api/v1/products/p1') {
      return json(200, {
        'product_id': 'p1',
        'name': 'Cough Syrup 100ml',
        'price_includes_tax': true,
        'variants': [
          {
            'variant_id': 'v1',
            'sku': 'RX-CS-100',
            'mrp': '95.00',
            'selling_price': price,
            'cost_price': '50.00',
            'track_batch': true,
            'status': 'active',
            'generated_barcode': '2000000000999',
            'open_price': null,
          }
        ],
      });
    }
    if (path == '/api/v1/inventory/variants/v1/branches') {
      expect(r.url.queryParameters['branch_id'], 'branch-test');
      return json(200, {
        'branches': [
          {'branch_id': 'branch-test', 'branch_name': 'Koramangala', 'on_hand': '12.000', 'reserved': '1.000', 'available': '11.000'},
          {'branch_id': 'b2', 'branch_name': 'MG Road', 'on_hand': '4.000', 'reserved': '0.000', 'available': '4.000'},
        ],
        'batches': [
          {'batch_id': 'bt1', 'batch_no': 'CS-2026A', 'expiry_date': '2026-12-31', 'quantity_remaining': '5.000', 'expired': false},
          {'batch_id': 'bt2', 'batch_no': 'CS-2027B', 'expiry_date': '2027-08-31', 'quantity_remaining': '7.000', 'expired': false},
        ],
      });
    }
    if (r.method == 'PATCH' && path == '/api/v1/pricing/variants/v1') {
      final b = jsonDecode(r.body) as Map<String, dynamic>;
      if ((b['selling_price'] as num) < 50 && b['override'] != true) {
        return json(409, {
          'error': {'code': 'NEGATIVE_MARGIN', 'message': 'below cost'}
        });
      }
      price = (b['selling_price'] as num).toStringAsFixed(2);
      return json(200, {'variant_id': 'v1'});
    }
    if (r.method == 'POST' && path == '/api/v1/inventory/adjustments') return json(200, {'on_hand': '10.000'});
    if (path == '/api/v1/products/variants/v1/label') return http.Response.bytes([0x1B, 0x40, 0x41], 200);
    return json(404, {
      'error': {'code': 'NOT_FOUND', 'message': 'unscripted ${r.method} $path'}
    });
  }
}

Future<void> _settle(WidgetTester tester, {int rounds = 10}) async {
  for (var i = 0; i < rounds; i++) {
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 20)));
    await tester.pump(const Duration(milliseconds: 60));
  }
}

Future<AppSession> _pump(WidgetTester tester, _Server server) async {
  tester.view.physicalSize = const Size(1280, 1200);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final session = AppSession(
    api: ApiClient(baseUrl: 'http://pos-test.invalid', httpClient: MockClient(server.handle)),
    branchId: 'branch-test',
    posTerminalId: 'terminal-test',
  );
  session.userId = 'u1';
  await tester.pumpWidget(MaterialApp(
    theme: posTheme(Brightness.light),
    home: ChangeNotifierProvider<AppSession>.value(value: session, child: const ProductLookupScreen(productId: 'p1', variantId: 'v1')),
  ));
  await _settle(tester);
  return session;
}

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('shows price and stock; without pricing.manage the price edit is disabled; adjust and label send the right request',
      (tester) async {
    final server = _Server({'inventory.adjust'});
    final session = await _pump(tester, server);

    expect(tester.widget<Text>(find.byKey(const Key('lookup-price'))).data, '₹80.00');
    expect(tester.widget<Text>(find.byKey(const Key('lookup-stock'))).data, '11');
    expect(find.text('MG Road'), findsOneWidget);
    expect(find.text('CS-2026A'), findsOneWidget);
    expect(tester.widget<OutlinedButton>(find.byKey(const Key('lookup-edit-price'))).onPressed, isNull,
        reason: 'no pricing.manage: disabled up front');
    expect(find.byTooltip('Needs the pricing.manage permission'), findsOneWidget);

    // Remove 2 damaged units from a chosen batch.
    await tester.tap(find.byKey(const Key('lookup-adjust')));
    await _settle(tester);
    await tester.enterText(find.byKey(const Key('adjust-qty')), '2');
    await tester.enterText(find.byKey(const Key('adjust-reason')), 'Damaged in transit');
    await tester.tap(find.byKey(const Key('adjust-save')));
    await _settle(tester);
    final adj = jsonDecode(server.requests.lastWhere((r) => r.url.path == '/api/v1/inventory/adjustments').body) as Map<String, dynamic>;
    expect(adj['quantity_delta'], -2);
    expect(adj['branch_id'], 'branch-test');
    expect(adj['reason'], 'Damaged in transit');
    expect(find.text('Stock adjusted'), findsOneWidget);

    // Detailed label, a chosen batch, 2 copies. No printer is configured
    // here, so the send fails after the label was fetched.
    await tester.tap(find.byKey(const Key('lookup-label')));
    await _settle(tester);
    await tester.tap(find.byKey(const Key('template-detailed')));
    await tester.pump();
    await tester.tap(find.byTooltip('More'));
    await tester.pump();
    expect(tester.widget<Text>(find.byKey(const Key('label-copies'))).data, '2');
    await tester.tap(find.byKey(const Key('label-print')));
    await _settle(tester);
    final label = server.requests.lastWhere((r) => r.url.path.endsWith('/label'));
    expect(label.url.queryParameters, {'template': 'detailed', 'copies': '2', 'branch_id': 'branch-test'});
    expect(find.byKey(const Key('label-error')), findsOneWidget, reason: 'no printer configured is reported, not swallowed');
    session.dispose();
  });

  testWidgets('with pricing.manage a below-cost price needs a second, confirmed save', (tester) async {
    final server = _Server({'pricing.manage', 'inventory.adjust'});
    final session = await _pump(tester, server);

    await tester.tap(find.byKey(const Key('lookup-edit-price')));
    await _settle(tester);
    await tester.enterText(find.byKey(const Key('price-field')), '45');
    await tester.enterText(find.byKey(const Key('price-reason')), 'Clearance');
    await tester.tap(find.byKey(const Key('price-save')));
    await _settle(tester);
    expect(find.textContaining('below the cost price'), findsOneWidget);
    expect(find.text('Save below cost'), findsOneWidget);
    await tester.tap(find.byKey(const Key('price-save')));
    await _settle(tester);

    final patches = server.requests.where((r) => r.method == 'PATCH').map((r) => jsonDecode(r.body) as Map).toList();
    expect(patches.map((p) => p['override']), [false, true]);
    expect(find.textContaining('Sales already made keep their price'), findsOneWidget);
    expect(tester.widget<Text>(find.byKey(const Key('lookup-price'))).data, '₹45.00', reason: 'reloaded after the save');
    session.dispose();
  });
}
