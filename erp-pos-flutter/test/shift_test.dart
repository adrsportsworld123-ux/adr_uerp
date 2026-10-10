// Phase 13 shifts on the POS, against a scripted backend that answers with
// erp-core-go's shapes (internal/sales/shifts.go): open with a float, a
// paid-out that needs a manager, a matching close straight through, a
// short count that needs a reason and approval, and the till opening on
// the shift screen when the merchant requires shifts.
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
import 'package:erp_pos_app/screens/shift_screen.dart';
import 'package:erp_pos_app/state/session.dart';
import 'package:erp_pos_app/ui/theme.dart';

class _Backend {
  final requests = <http.Request>[];
  bool open = false;
  bool required = false;
  double float = 0;
  double paidOut = 0;
  final movements = <Map<String, dynamic>>[];
  Map<String, dynamic>? lastClose;

  double get expected => float + 500 - paidOut; // 500 of cash sales in the window

  Map<String, dynamic> report(String kind, {Map<String, dynamic>? close}) => {
        'kind': kind,
        'generated_at': '2026-10-10T12:00:00Z',
        'order_count': 2,
        'voided_count': 0,
        'net_sales': '900.00',
        'by_payment_method': {'cash': '500.00', 'upi': '400.00'},
        'return_count': 0,
        'refund_total': '0.00',
        'refunds_by_method': {},
        'cash': {
          'opening_float': float.toStringAsFixed(2),
          'cash_sales': '500.00',
          'cash_refunds': '0.00',
          'float_in': '0.00',
          'drops': '0.00',
          'paid_in': '0.00',
          'paid_out': paidOut.toStringAsFixed(2),
          'expected': expected.toStringAsFixed(2),
        },
        'movements': movements,
        if (close != null) 'close': close,
      };

  Map<String, dynamic> shift(String status, {Map<String, dynamic>? close}) => {
        'shift_id': 'shift-1',
        'shift_number': 4,
        'status': status,
        'branch_id': 'branch-test',
        'pos_terminal_id': 'terminal-test',
        'terminal_name': 'Counter 2',
        'opening_float': float.toStringAsFixed(2),
        'opened_by': 'Ravi',
        'opened_at': '2026-10-10T03:30:00Z',
        'closed_by': status == 'closed' ? 'Ravi' : null,
        'closed_at': status == 'closed' ? '2026-10-10T12:00:00Z' : null,
        'report': report(status == 'closed' ? 'Z' : 'X', close: close),
      };

  http.Response json(int code, Object body) => http.Response(jsonEncode(body), code, headers: {'content-type': 'application/json; charset=utf-8'});

  Future<http.Response> handle(http.Request r) async {
    requests.add(r);
    final path = r.url.path;
    if (path == '/api/v1/sales/settings') {
      return json(200, {'returns_require_approval': true, 'return_window_days': null, 'shifts_required': required});
    }
    if (path == '/api/v1/pos-shifts/current') return json(200, {'shift': open ? shift('open') : null});
    if (r.method == 'POST' && path == '/api/v1/pos-shifts') {
      float = ((jsonDecode(r.body) as Map)['opening_float'] as num).toDouble();
      open = true;
      return json(201, shift('open'));
    }
    if (r.method == 'POST' && path == '/api/v1/pos-shifts/shift-1/cash-movements') {
      final b = jsonDecode(r.body) as Map<String, dynamic>;
      if (b['kind'] == 'paid_out' && b['approver_pin'] != '1234') {
        return json(403, {
          'error': {'code': 'CASH_MOVEMENT_NOT_APPROVED', 'message': 'needs a manager'}
        });
      }
      paidOut += (b['amount'] as num).toDouble();
      movements.add({
        'movement_id': 'm${movements.length}',
        'kind': b['kind'],
        'amount': (b['amount'] as num).toStringAsFixed(2),
        'reason': b['reason'],
        'performed_by': 'Ravi',
        'approved_by': 'Arjun',
        'created_at': '2026-10-10T08:00:00Z',
      });
      return json(201, shift('open'));
    }
    if (r.method == 'POST' && path == '/api/v1/pos-shifts/shift-1/close') {
      final b = jsonDecode(r.body) as Map<String, dynamic>;
      final counted = (b['denominations'] as List).fold<double>(0, (s, d) => s + (d['denomination'] as num) * (d['count'] as num));
      final variance = counted - expected;
      if (variance != 0 && b['reason'] == null) {
        return json(400, {
          'error': {'code': 'INVALID_REQUEST', 'message': 'give a reason'}
        });
      }
      lastClose = b;
      open = false;
      return json(200, shift('closed', close: {
        'counted_total': counted.toStringAsFixed(2),
        'variance': variance.toStringAsFixed(2),
        'reason': b['reason'] ?? '',
        'approved_by': variance == 0 ? null : 'Arjun',
        'cash_reconciliation_id': 'recon-1',
      }));
    }
    if (path == '/api/v1/categories') return json(200, {'categories': []});
    if (path == '/api/v1/products/search') return json(200, {'results': [], 'total': 0});
    return json(404, {
      'error': {'code': 'NOT_FOUND', 'message': 'unscripted ${r.method} $path'}
    });
  }
}

AppSession _session(_Backend b) {
  final s = AppSession(
    api: ApiClient(baseUrl: 'http://pos-test.invalid', httpClient: MockClient(b.handle)),
    branchId: 'branch-test',
    posTerminalId: 'terminal-test',
    terminalName: 'Counter 2',
  );
  s.userId = 'cashier-test';
  s.roles = ['POS User'];
  return s;
}

Future<void> _settle(WidgetTester tester, {int rounds = 10}) async {
  for (var i = 0; i < rounds; i++) {
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 20)));
    await tester.pump(const Duration(milliseconds: 50));
  }
}

Future<void> _pump(WidgetTester tester, AppSession session, Widget home) async {
  tester.view.physicalSize = const Size(1280, 1400);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(MaterialApp(
    theme: posTheme(Brightness.light),
    home: ChangeNotifierProvider<AppSession>.value(value: session, child: home),
  ));
  await _settle(tester);
}

void main() {
  setUpAll(() async {
    sqfliteFfiInit();
    databaseFactory = databaseFactoryFfi;
    await databaseFactory.setDatabasesPath(Directory.systemTemp.createTempSync('pos_shift_test').path);
  });

  testWidgets('open with a float, a paid-out needs a manager, a matching count closes to a Z report', (tester) async {
    final backend = _Backend();
    final session = _session(backend);
    await _pump(tester, session, const ShiftScreen());

    expect(find.text('No shift is open on this till'), findsOneWidget);
    await tester.enterText(find.byKey(const Key('opening-float')), '2000');
    await tester.tap(find.byKey(const Key('open-shift')));
    await _settle(tester);
    expect(find.text('X report (so far)'), findsOneWidget);
    expect(tester.widget<Text>(find.byKey(const Key('expected-cash'))).data, '₹2,500.00');
    final openBody = jsonDecode(backend.requests.firstWhere((r) => r.method == 'POST').body) as Map<String, dynamic>;
    expect(openBody['opening_float'], 2000);
    expect(openBody['idempotency_key'], isNotEmpty);

    // Paid out: wrong PIN keeps the dialog open with the message, then succeeds.
    await tester.tap(find.byKey(const Key('cash-movement')));
    await _settle(tester);
    await tester.tap(find.text('Paid out'));
    await tester.pump();
    await tester.enterText(find.byKey(const Key('movement-amount')), '150');
    await tester.enterText(find.byKey(const Key('movement-reason')), 'Courier charges');
    await tester.enterText(find.byKey(const Key('movement-approver')), 'EMP003');
    for (final d in ['9', '9', '9', '9']) {
      await tester.tap(find.text(d).last);
    }
    await tester.pump();
    await tester.tap(find.byKey(const Key('movement-save')));
    await _settle(tester);
    expect(find.byKey(const Key('movement-error')), findsOneWidget);
    await tester.tap(find.text('Clear'));
    for (final d in ['1', '2', '3', '4']) {
      await tester.tap(find.text(d).last);
    }
    await tester.pump();
    await tester.tap(find.byKey(const Key('movement-save')));
    await _settle(tester);
    expect(find.byType(CashMovementDialog), findsNothing);
    final moves = backend.requests.where((r) => r.url.path.endsWith('/cash-movements')).map((r) => jsonDecode(r.body) as Map).toList();
    expect(moves, hasLength(2));
    expect(moves[0]['idempotency_key'], moves[1]['idempotency_key'], reason: 'a retry of the same movement keeps its key');
    expect(tester.widget<Text>(find.byKey(const Key('expected-cash'))).data, '₹2,350.00');
    expect(find.textContaining('Courier charges'), findsOneWidget);

    // Close: 1 × 2000 + 3 × 100 + 1 × 50 = 2350, matching: no reason or approval.
    await tester.tap(find.byKey(const Key('close-shift')));
    await _settle(tester);
    await tester.enterText(find.byKey(const Key('count-2000')), '1');
    await tester.enterText(find.byKey(const Key('count-100')), '3');
    await tester.enterText(find.byKey(const Key('count-50')), '1');
    await tester.pump();
    expect(tester.widget<Text>(find.byKey(const Key('counted-total'))).data, '₹2,350.00');
    expect(find.byKey(const Key('close-reason')), findsNothing);
    await tester.tap(find.byKey(const Key('confirm-close')));
    await _settle(tester);
    expect(find.text('Z report (final)'), findsOneWidget);
    expect(tester.widget<Text>(find.byKey(const Key('variance'))).data, '₹0.00');
    expect(session.currentShift, isNull);
    final den = (backend.lastClose!['denominations'] as List).firstWhere((d) => d['denomination'] == 2000);
    expect(den['count'], 1);
    session.dispose();
  });

  testWidgets('a short count needs a reason and a manager before it can close', (tester) async {
    final backend = _Backend()
      ..open = true
      ..float = 1000;
    final session = _session(backend);
    await session.refreshShift();
    await _pump(tester, session, const CloseShiftScreen());

    await tester.enterText(find.byKey(const Key('count-500')), '2');
    await tester.enterText(find.byKey(const Key('count-200')), '2');
    await tester.pump();
    // Expected 1500, counted 1400: ₹100 short.
    expect(tester.widget<Text>(find.byKey(const Key('close-variance'))).data, '₹100.00');
    expect(find.text('Short'), findsOneWidget);
    final confirm = find.byKey(const Key('confirm-close'));
    expect(tester.widget<FilledButton>(confirm).onPressed, isNull);

    await tester.enterText(find.byKey(const Key('close-reason')), 'Change given wrongly');
    await tester.enterText(find.byKey(const Key('close-approver')), 'EMP003');
    for (final d in ['1', '2', '3', '4']) {
      await tester.tap(find.text(d).last);
    }
    await tester.pump();
    expect(tester.widget<FilledButton>(confirm).onPressed, isNotNull);
    await tester.ensureVisible(confirm);
    await tester.tap(confirm);
    await _settle(tester);
    expect(backend.lastClose!['reason'], 'Change given wrongly');
    expect(backend.lastClose!['approver_code'], 'EMP003');
    expect(backend.lastClose!['approver_pin'], '1234');
    session.dispose();
  });

  testWidgets('when shifts are required and none is open, the till starts on the shift screen', (tester) async {
    final backend = _Backend()..required = true;
    final session = _session(backend);
    await _pump(tester, session, const PosScreen());
    await _settle(tester);
    expect(find.byType(ShiftScreen), findsOneWidget);
    expect(find.text('Open a shift before taking payment.'), findsOneWidget);
    session.dispose();
  });
}
