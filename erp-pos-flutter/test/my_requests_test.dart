// Leave, permission, attendance corrections and payslips at the till
// (erp-core-go §3.78): the backend is scripted with MockClient, so these run
// without a server.
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:provider/provider.dart';

import 'package:erp_pos_app/api/api_client.dart';
import 'package:erp_pos_app/screens/attendance_screen.dart';
import 'package:erp_pos_app/state/session.dart';
import 'package:erp_pos_app/ui/theme.dart';

Map<String, dynamic> _leave(String status) => {
      'leave_request_id': 'lv-1',
      'request_number': 'LV-00001',
      'leave_type': 'Casual leave',
      'half_day': false,
      'from_date': '2026-11-03',
      'to_date': '2026-11-04',
      'days': 2,
      'reason': 'wedding',
      'status': status,
      'unpaid_days': null,
      'decision_note': null,
      'cancel_reason': null,
      'requested_at': '2026-10-28 10:00:00+05:30',
    };

(AppSession, List<http.Request>) _setup() {
  final requests = <http.Request>[];
  final client = MockClient((r) async {
    requests.add(r);
    final json = {'content-type': 'application/json'};
    http.Response ok(Object body, [int status = 200]) => http.Response(jsonEncode(body), status, headers: json);
    switch (r.url.path) {
      case '/api/v1/hr/attendance/me':
        return ok({
          'open': null,
          'today': null,
          'settings': {'require_location': false, 'geofence_mode': 'flag', 'allow_web': true, 'allow_pos': true},
          'geofence': null,
        });
      case '/api/v1/hr/leave-balances':
        return ok({
          'balances': [
            {'name': 'Casual leave', 'paid': true, 'status': 'active', 'entitled': 12, 'taken': 3, 'pending': 2, 'available': 9},
            {'name': 'Loss of pay', 'paid': false, 'status': 'active', 'entitled': null, 'taken': 1, 'pending': 0, 'available': null},
          ],
          'permission': {'free_count': 2, 'used_count': 1, 'free_minutes': 240, 'used_free_minutes': 120, 'unpaid_minutes': 0, 'max_minutes': 240},
        });
      case '/api/v1/hr/leave-requests':
        if (r.method == 'POST') return ok({'request_number': 'LV-00009'}, 201);
        return ok({'leave_requests': [_leave('pending')]});
      case '/api/v1/hr/permission-requests':
        return ok({
          'permission_requests': [
            {
              'permission_request_id': 'pr-1',
              'request_number': 'PR-00004',
              'work_date': '2026-10-20',
              'from_time': '10:00',
              'to_time': '12:30',
              'reason': 'bank',
              'status': 'approved',
              'unpaid_minutes': 30,
              'decision_note': 'ok',
              'requested_at': '2026-10-19 10:00:00+05:30',
            }
          ]
        });
      case '/api/v1/hr/regularizations':
        return ok({'regularizations': []});
      case '/api/v1/hr/leave-types':
        return ok({
          'leave_types': [
            {'leave_type_id': 'cl', 'code': 'CL', 'name': 'Casual leave', 'paid': true, 'allow_half_day': true, 'status': 'active'},
            {'leave_type_id': 'old', 'code': 'OL', 'name': 'Old leave', 'paid': true, 'allow_half_day': true, 'status': 'inactive'},
          ]
        });
      case '/api/v1/hr/leave-requests/lv-1/cancel':
        return ok(_leave('cancelled'));
      case '/api/v1/payroll/employees/u-1/payslips':
        return ok({
          'payslips': [
            {
              'payslip_id': 'ps-1',
              'period_month': 9,
              'period_year': 2026,
              'days_present': '29.50',
              'days_in_period': '30.00',
              'gross_earnings': '29500.00',
              'total_deductions': '200.00',
              'net_pay': '29300.00',
            }
          ]
        });
    }
    return http.Response('{}', 404);
  });
  final s = AppSession(api: ApiClient(baseUrl: 'http://pos-test.invalid', httpClient: client), branchId: 'branch-test', posTerminalId: 'terminal-test');
  s.userId = 'u-1';
  return (s, requests);
}

Widget _app(AppSession s) => ChangeNotifierProvider.value(
      value: s,
      child: MaterialApp(
        theme: posTheme(Brightness.light),
        home: Builder(
          builder: (context) => Scaffold(
            body: Center(
              child: FilledButton(
                onPressed: () => Navigator.of(context).push(MaterialPageRoute(
                    builder: (_) => ChangeNotifierProvider<AppSession>.value(
                        value: s, child: AttendanceScreen(locate: () async => const DeviceFix.missing('No location service.'))))),
                child: const Text('open'),
              ),
            ),
          ),
        ),
      ),
    );

Future<void> _openRequests(WidgetTester tester) async {
  tester.view.physicalSize = const Size(1280, 1600);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  await tester.tap(find.text('open'));
  await tester.pumpAndSettle();
  await tester.tap(find.byKey(const Key('attendance-requests')));
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('shows the balance, permission use and my requests; asks for leave from the till', (tester) async {
    final (session, requests) = _setup();
    await tester.pumpWidget(_app(session));
    await _openRequests(tester);

    expect(find.text('9 left'), findsOneWidget);
    expect(find.text('3 taken · 2 waiting'), findsOneWidget);
    expect(find.text('Unpaid · 1 taken'), findsOneWidget);
    expect(find.byKey(const Key('permission-usage')), findsOneWidget);
    expect(find.textContaining('1 of 2 free used (120 of 240 min)'), findsOneWidget);
    expect(find.byKey(const Key('request-LV-00001')), findsOneWidget);
    expect(find.textContaining('30 min unpaid'), findsOneWidget);

    await tester.tap(find.byKey(const Key('ask-leave')));
    await tester.pumpAndSettle();
    expect(tester.widget<FilledButton>(find.byKey(const Key('ask-send'))).onPressed, isNull, reason: 'nothing chosen yet');
    await tester.tap(find.byKey(const Key('leave-type')));
    await tester.pumpAndSettle();
    expect(find.text('Old leave'), findsNothing, reason: 'an inactive type is not offered');
    await tester.tap(find.text('Casual leave').last);
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('ask-reason')), 'family function');
    await tester.pump();
    await tester.tap(find.byKey(const Key('ask-send')));
    await tester.pumpAndSettle();

    final post = requests.singleWhere((r) => r.method == 'POST' && r.url.path == '/api/v1/hr/leave-requests');
    final body = jsonDecode(post.body) as Map<String, dynamic>;
    expect(body['leave_type_id'], 'cl');
    expect(body['reason'], 'family function');
    expect(body['source'], 'pos');
    expect(body['half_day'], false);
    expect(body['from_date'], body['to_date']);
    expect(find.text('Sent for approval (LV-00009)'), findsOneWidget);
  });

  testWidgets('cancels a request still waiting', (tester) async {
    final (session, requests) = _setup();
    await tester.pumpWidget(_app(session));
    await _openRequests(tester);
    await tester.tap(find.descendant(of: find.byKey(const Key('request-LV-00001')), matching: find.text('Cancel')));
    await tester.pumpAndSettle();
    expect(requests.where((r) => r.url.path == '/api/v1/hr/leave-requests/lv-1/cancel'), hasLength(1));
    expect(find.text('LV-00001 cancelled'), findsOneWidget);
  });

  testWidgets('lists my finalized payslips', (tester) async {
    final (session, _) = _setup();
    await tester.pumpWidget(_app(session));
    await _openRequests(tester);
    await tester.tap(find.text('Payslips'));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('payslip-2026-9')), findsOneWidget);
    expect(find.text('Sep 2026'), findsOneWidget);
    expect(find.text('29300.00'), findsOneWidget);
    expect(find.textContaining('Days paid 29.5 of 30'), findsOneWidget);
  });
}
