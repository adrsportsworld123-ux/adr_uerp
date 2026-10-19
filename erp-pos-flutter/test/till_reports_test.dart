// X and Z reports on the till (menu → X / Z reports), against a scripted
// backend answering with erp-core-go's shapes (internal/sales shifts.go,
// day_report.go): the shift list opens a shift's X report; the Z tab shows
// the branch's day, prints it to the receipt printer, and closes a day
// that has ended.
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:erp_pos_app/api/api_client.dart';
import 'package:erp_pos_app/screens/till_reports_screen.dart';
import 'package:erp_pos_app/state/session.dart';
import 'package:erp_pos_app/ui/theme.dart';

class _Backend {
  final requests = <http.Request>[];
  bool closed = false;

  Map<String, dynamic> day(String date) => {
        'kind': 'Z',
        'branch_id': 'branch-test',
        'branch_name': 'MG Road',
        'date': date,
        'status': closed ? 'closed' : 'open',
        'z_number': closed ? 7 : null,
        'closed_by': closed ? 'Arjun' : null,
        'closed_at': closed ? '2026-10-18T04:00:00Z' : null,
        'generated_at': '2026-10-18T04:00:00Z',
        'first_order_number': 'SO-1',
        'last_order_number': 'SO-9',
        'order_count': 9,
        'subtotal': '9000.00',
        'discount_total': '500.00',
        'tax_total': '1530.00',
        'net_sales': '10030.00',
        'credit_sales': '0.00',
        'average_bill': '1114.44',
        'voided_count': 1,
        'voided_total': '200.00',
        'released_count': 2,
        'by_payment_method': {'cash': '4030.00', 'upi': '6000.00'},
        'by_channel': {'pos': '10030.00'},
        'return_count': 0,
        'refund_total': '0.00',
        'refunds_by_method': {},
        'tax_rates': [
          {'rate': '18', 'taxable_value': '8500.00', 'tax': '1530.00'}
        ],
        'cashiers': [
          {'cashier': 'Ravi', 'orders': 9, 'net_sales': '10030.00'}
        ],
        'cash': {
          'cash_sales': '4030.00', 'cash_refunds': '0.00', 'float_in': '0.00', 'drops': '0.00', 'paid_in': '0.00', 'paid_out': '0.00',
          'net_cash': '4030.00', 'counted': '6030.00', 'variance': '0.00',
        },
        'shifts': [
          {'shift_id': 'shift-1', 'shift_number': 4, 'terminal_name': 'Counter 2', 'status': 'closed', 'opened_by': 'Ravi', 'opened_at': '2026-10-17T03:30:00Z',
           'closed_at': '2026-10-17T12:00:00Z', 'order_count': 9, 'net_sales': '10030.00', 'expected_cash': '6030.00', 'counted_cash': '6030.00', 'variance': '0.00'}
        ],
        'open_shifts': 0,
      };

  http.Response json(int code, Object body) => http.Response(jsonEncode(body), code, headers: {'content-type': 'application/json; charset=utf-8'});

  Future<http.Response> handle(http.Request r) async {
    requests.add(r);
    final p = r.url.path;
    if (p == '/api/v1/pos-shifts') {
      return json(200, {
        'shifts': [
          {'shift_id': 'shift-1', 'shift_number': 4, 'status': 'closed', 'branch_id': 'branch-test', 'branch_name': 'MG Road', 'pos_terminal_id': 'terminal-test',
           'terminal_name': 'Counter 2', 'opening_float': '2000.00', 'opened_by': 'Ravi', 'opened_at': '2026-10-17T03:30:00Z', 'closed_by': 'Ravi',
           'closed_at': '2026-10-17T12:00:00Z', 'variance': '0.00', 'order_count': 9, 'net_sales': '10030.00'}
        ],
        'total': 1, 'page': 1, 'limit': 50,
      });
    }
    if (p == '/api/v1/pos-shifts/shift-1') {
      return json(200, {
        'shift_id': 'shift-1', 'shift_number': 4, 'status': 'closed', 'branch_id': 'branch-test', 'branch_name': 'MG Road', 'pos_terminal_id': 'terminal-test',
        'terminal_name': 'Counter 2', 'opening_float': '2000.00', 'opened_by': 'Ravi', 'opened_at': '2026-10-17T03:30:00Z', 'closed_by': 'Ravi',
        'closed_at': '2026-10-17T12:00:00Z',
        'report': {
          'kind': 'Z', 'generated_at': '2026-10-17T12:00:00Z', 'order_count': 9, 'voided_count': 0, 'net_sales': '10030.00',
          'by_payment_method': {'cash': '4030.00', 'upi': '6000.00'}, 'return_count': 0, 'refund_total': '0.00', 'refunds_by_method': {},
          'cash': {'opening_float': '2000.00', 'cash_sales': '4030.00', 'cash_refunds': '0.00', 'float_in': '0.00', 'drops': '0.00', 'paid_in': '0.00',
                   'paid_out': '0.00', 'expected': '6030.00'},
          'movements': [],
          'close': {'counted_total': '6030.00', 'variance': '0.00', 'reason': '', 'approved_by': null, 'cash_reconciliation_id': 'r1'},
        },
      });
    }
    if (p == '/api/v1/reports/z-report') return json(200, day(r.url.queryParameters['date']!));
    if (p == '/api/v1/reports/z-report/print') return http.Response.bytes(utf8.encode('\x1b@Z REPORT'), 200);
    if (r.method == 'POST' && p == '/api/v1/reports/z-report/close') {
      closed = true;
      return json(201, {'report': day((jsonDecode(r.body) as Map)['date'] as String), 'replayed': false});
    }
    return json(404, {
      'error': {'code': 'NOT_FOUND', 'message': 'unscripted ${r.method} $p'}
    });
  }
}

Future<void> _settle(WidgetTester tester, {int rounds = 10}) async {
  for (var i = 0; i < rounds; i++) {
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 20)));
    await tester.pump(const Duration(milliseconds: 50));
  }
}

void main() {
  testWidgets('X: a shift opens to its X report; Z: the day prints and closes', (tester) async {
    // A fake receipt printer on localhost.
    final printer = await tester.runAsync(() => ServerSocket.bind(InternetAddress.loopbackIPv4, 0));
    final printed = <int>[];
    printer!.listen((c) => c.listen(printed.addAll));
    SharedPreferences.setMockInitialValues({'printer_host': '127.0.0.1', 'printer_port': printer.port});

    final backend = _Backend();
    final session = AppSession(
      api: ApiClient(baseUrl: 'http://pos-test.invalid', httpClient: MockClient(backend.handle)),
      branchId: 'branch-test',
      posTerminalId: 'terminal-test',
      terminalName: 'Counter 2',
    )..roles = ['Branch Manager'];
    tester.view.physicalSize = const Size(1000, 1800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(MaterialApp(
      theme: posTheme(Brightness.light),
      home: ChangeNotifierProvider<AppSession>.value(value: session, child: const TillReportsScreen()),
    ));
    await _settle(tester);

    // X: this till's shifts.
    final list = backend.requests.firstWhere((r) => r.url.path == '/api/v1/pos-shifts');
    expect(list.url.queryParameters['terminal_id'], 'terminal-test');
    expect(find.text('Counter 2 · shift 4'), findsOneWidget);
    await tester.tap(find.byKey(const Key('shift-shift-1')));
    await _settle(tester);
    expect(find.text('X report (final)'), findsOneWidget);
    expect(find.byKey(const Key('print-x-report')), findsOneWidget);
    await tester.pageBack();
    await tester.pump(const Duration(seconds: 1));
    await _settle(tester);

    // Z: the day, then print and close it.
    await tester.tap(find.byKey(const Key('tab-z')));
    await _settle(tester);
    expect(tester.widget<Text>(find.byKey(const Key('z-net-sales'))).data, '₹10,030.00');
    expect(tester.widget<Text>(find.byKey(const Key('z-status'))).data, 'Day not closed: figures so far');
    await tester.tap(find.byKey(const Key('print-z-report')));
    await _settle(tester, rounds: 20);
    expect(utf8.decode(printed, allowMalformed: true), contains('Z REPORT'));

    // Today hasn't ended: pick yesterday, then close it.
    final close = find.byKey(const Key('close-day'));
    expect(tester.widget<FilledButton>(close).onPressed, isNull);
    await tester.tap(find.byKey(const Key('z-date')));
    await tester.pumpAndSettle();
    final yesterday = DateTime.now().subtract(const Duration(days: 1));
    if (yesterday.month != DateTime.now().month) {
      await tester.tap(find.byTooltip('Previous month'));
      await tester.pumpAndSettle();
    }
    await tester.tap(find.text('${yesterday.day}').last);
    await tester.tap(find.text('OK'));
    await _settle(tester);
    await tester.tap(find.byKey(const Key('close-day')));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('confirm-close-day')));
    await _settle(tester);
    final closeReq = backend.requests.firstWhere((r) => r.method == 'POST');
    expect(jsonDecode(closeReq.body), {'branch_id': 'branch-test', 'date': '${yesterday.year}-${yesterday.month.toString().padLeft(2, '0')}-${yesterday.day.toString().padLeft(2, '0')}'});
    expect(tester.widget<Text>(find.byKey(const Key('z-status'))).data, 'Z no. 7 · closed by Arjun');
    expect(find.byKey(const Key('close-day')), findsNothing);

    await tester.runAsync(() => printer.close());
    session.dispose();
  });
}
