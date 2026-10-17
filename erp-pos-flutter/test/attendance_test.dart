// Attendance check-in/out at the till (erp-core-go internal/hr/attendance.go):
// the location comes from an injected locator, the backend is scripted with
// MockClient, so these run without GPS or a server.
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

Map<String, dynamic> _record({String? clockOut}) => {
      'id': 'att-1',
      'user_id': 'u-1',
      'work_date': '2026-10-17',
      'clock_in': '2026-10-17 09:02:00+05:30',
      if (clockOut != null) 'clock_out': clockOut,
      'status': 'present',
      'clock_in_evidence': {'source': 'pos', 'geofence': 'inside', 'distance_m': 12},
      'clock_out_evidence': <String, dynamic>{},
    };

(AppSession, List<http.Request>) _setup({bool checkedIn = false}) {
  final requests = <http.Request>[];
  final client = MockClient((r) async {
    requests.add(r);
    final json = {'content-type': 'application/json'};
    if (r.url.path == '/api/v1/hr/attendance/me') {
      return http.Response(
          jsonEncode({
            'open': checkedIn ? _record() : null,
            'today': checkedIn ? _record() : null,
            'settings': {'require_location': false, 'geofence_mode': 'flag', 'allow_web': true, 'allow_pos': true},
            'geofence': {'branch_name': 'MG Road', 'radius_m': 200},
          }),
          200,
          headers: json);
    }
    if (r.url.path == '/api/v1/hr/attendance/clock-in') return http.Response(jsonEncode(_record()), 200, headers: json);
    if (r.url.path == '/api/v1/hr/attendance/clock-out') {
      return http.Response(jsonEncode(_record(clockOut: '2026-10-17 18:00:00+05:30')), 200, headers: json);
    }
    return http.Response('{}', 404);
  });
  final s = AppSession(
    api: ApiClient(baseUrl: 'http://pos-test.invalid', httpClient: client),
    branchId: 'branch-test',
    posTerminalId: 'terminal-test',
  );
  return (s, requests);
}

Widget _app(AppSession s, Future<DeviceFix> Function() locate) => ChangeNotifierProvider.value(
      value: s,
      child: MaterialApp(
        theme: posTheme(Brightness.light),
        home: Builder(
          builder: (context) => Scaffold(
            body: Center(
              child: FilledButton(
                onPressed: () => Navigator.of(context).push(MaterialPageRoute(builder: (_) => AttendanceScreen(locate: locate))),
                child: const Text('open'),
              ),
            ),
          ),
        ),
      ),
    );

void main() {
  testWidgets('checks in with the device location, naming this till', (tester) async {
    final (session, requests) = _setup();
    await tester.pumpWidget(_app(session, () async => const DeviceFix.found(12.834, 80.17, 18)));
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();

    expect(find.text('Not checked in yet'), findsOneWidget);
    expect(find.textContaining('Location found (±18 m)'), findsOneWidget);
    await tester.tap(find.byKey(const Key('attendance-punch')));
    await tester.pumpAndSettle();

    final punch = requests.singleWhere((r) => r.url.path.endsWith('/clock-in'));
    expect(jsonDecode(punch.body), {'latitude': 12.834, 'longitude': 80.17, 'accuracy_m': 18});
    expect(punch.headers['X-Client'], 'pos');
    expect(punch.headers['X-Terminal-Id'], 'terminal-test');
    expect(find.textContaining('Checked in at'), findsOneWidget);
  });

  testWidgets('a till without GPS still checks out, without a location', (tester) async {
    final (session, requests) = _setup(checkedIn: true);
    await tester.pumpWidget(_app(session, () async => const DeviceFix.missing('This terminal has no location service.')));
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();

    expect(find.textContaining('Checked in since'), findsOneWidget);
    expect(find.text('This terminal has no location service.'), findsOneWidget);
    await tester.tap(find.byKey(const Key('attendance-punch')));
    await tester.pumpAndSettle();

    final punch = requests.singleWhere((r) => r.url.path.endsWith('/clock-out'));
    expect(jsonDecode(punch.body), isEmpty);
    expect(punch.headers['X-Terminal-Id'], 'terminal-test');
  });
}
