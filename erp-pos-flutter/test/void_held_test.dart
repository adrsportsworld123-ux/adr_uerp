// Voiding a held order from the Held Orders screen (erp-core-go
// internal/sales/void_held.go): a reason is required, the request names
// it, and the list refreshes without the voided order. Scripted with
// MockClient.
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:provider/provider.dart';

import 'package:erp_pos_app/api/api_client.dart';
import 'package:erp_pos_app/screens/held_orders_screen.dart';
import 'package:erp_pos_app/state/session.dart';
import 'package:erp_pos_app/ui/theme.dart';

void main() {
  testWidgets('voids a held order with a reason; it leaves the list', (tester) async {
    var voided = false;
    final requests = <http.Request>[];
    final client = MockClient((r) async {
      requests.add(r);
      final json = {'content-type': 'application/json'};
      if (r.url.path == '/api/v1/sales/orders/held') {
        return http.Response(
            jsonEncode({
              'held_orders': voided
                  ? []
                  : [
                      {'order_id': 'o-1', 'order_number': 'MGR-0042', 'held_at': '2026-10-17 10:00:00+05:30', 'hold_note': 'Blue shirt', 'grand_total': '540.00', 'line_count': 2},
                    ],
            }),
            200,
            headers: json);
      }
      if (r.url.path == '/api/v1/sales/orders/o-1/void-held') {
        voided = true;
        return http.Response(jsonEncode({'order_id': 'o-1', 'status': 'held_voided'}), 200, headers: json);
      }
      return http.Response('{}', 404);
    });
    final session = AppSession(
      api: ApiClient(baseUrl: 'http://pos-test.invalid', httpClient: client),
      branchId: 'branch-test',
      posTerminalId: 'terminal-test',
    );
    await tester.pumpWidget(ChangeNotifierProvider.value(
      value: session,
      child: MaterialApp(theme: posTheme(Brightness.light), home: const HeldOrdersScreen()),
    ));
    await tester.pumpAndSettle();
    expect(find.text('MGR-0042'), findsOneWidget);

    await tester.tap(find.byKey(const Key('void-held-o-1')));
    await tester.pumpAndSettle();
    final confirm = find.byKey(const Key('void-held-confirm'));
    expect(tester.widget<FilledButton>(confirm).onPressed, isNull, reason: 'a reason is required');
    await tester.enterText(find.byKey(const Key('void-held-reason')), 'Customer left');
    await tester.pump();
    await tester.tap(confirm);
    await tester.pumpAndSettle();

    final call = requests.singleWhere((r) => r.url.path.endsWith('/void-held'));
    expect(jsonDecode(call.body), {'reason': 'Customer left'});
    expect(find.textContaining('voided'), findsOneWidget);
    expect(find.text('No carts are currently on hold'), findsOneWidget);
  });
}
