// App-level boot tests. Phase 12: a device with no stored pairing opens on
// the pairing screen; a paired one opens on login for ITS terminal (no
// more hardcoded seed terminal).
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:erp_pos_app/api/api_client.dart';
import 'package:erp_pos_app/main.dart';
import 'package:erp_pos_app/state/terminal_config.dart';

void main() {
  testWidgets('a paired device shows the login screen for its terminal', (WidgetTester tester) async {
    await tester.pumpWidget(const PosApp(initialConfig: TerminalConfig.seed));

    expect(find.text('ERP POS'), findsOneWidget);
    expect(find.text('Log in'), findsOneWidget);
    expect(find.byType(TextField), findsNWidgets(3)); // merchant code, email, password
    expect(find.text('Till 1 · MG Road'), findsOneWidget);
    // Merchant code comes from the pairing, not a hardcoded default.
    expect(find.widgetWithText(TextField, 'acme-sports'), findsOneWidget);
  });

  testWidgets('an unpaired device opens on the pairing screen', (WidgetTester tester) async {
    // getInstance() caches its future across tests; a future created in a
    // previous test's fake-async zone never completes in this one.
    SharedPreferences.resetStatic();
    SharedPreferences.setMockInitialValues({});
    await tester.pumpWidget(const PosApp());
    await tester.pumpAndSettle();

    expect(find.text('Pair this terminal'), findsOneWidget);
    Finder pairButton() => find.widgetWithText(FilledButton, 'Pair terminal');
    expect(tester.widget<FilledButton>(pairButton()).onPressed, isNull, reason: 'nothing entered yet');

    await tester.enterText(find.widgetWithText(TextField, 'Merchant code'), 'acme-sports');
    await tester.enterText(find.byKey(const Key('pairing-code')), 'ab3k9');
    await tester.pump();
    expect(tester.widget<FilledButton>(pairButton()).onPressed, isNull, reason: 'a code is 8 characters');
    expect(find.text('AB3K9'), findsOneWidget, reason: 'codes are upper-cased as typed');

    await tester.enterText(find.byKey(const Key('pairing-code')), 'ab3k9mxz');
    await tester.pump();
    expect(tester.widget<FilledButton>(pairButton()).onPressed, isNotNull);
  });

  // Persistence round trip for the pairing. (Booting PosApp on a stored
  // config is covered by the first test via initialConfig; driving the
  // async storage load through PosApp under fake async proved unreliable
  // with the plugin's cached singleton, so the storage is tested directly.)
  testWidgets('a saved pairing loads back unchanged, and clearing it unpairs', (WidgetTester tester) async {
    SharedPreferences.resetStatic();
    SharedPreferences.setMockInitialValues({});
    final loaded = await tester.runAsync(() async {
      await TerminalConfig.seed.save();
      return TerminalConfig.load();
    });
    expect(loaded?.terminalId, TerminalConfig.seed.terminalId);
    expect(loaded?.branchName, 'MG Road');
    expect(loaded?.merchantCode, 'acme-sports');

    final afterClear = await tester.runAsync(() async {
      await TerminalConfig.clear();
      return TerminalConfig.load();
    });
    expect(afterClear, isNull);
  });

  // The pairing screen and the paired app are different widget trees.
  // Completing pairing must land on the login screen, and unpairing must
  // land back on pairing, without restarting the app.
  testWidgets('pairing leads straight to login, and unpairing back to pairing', (WidgetTester tester) async {
    SharedPreferences.resetStatic();
    SharedPreferences.setMockInitialValues({});
    final server = MockClient((req) async {
      if (req.url.path.endsWith('/pos-terminals/pair')) {
        return http.Response(
          '{"terminal_id":"t-1","terminal_name":"Till 7","branch_id":"b-1","branch_name":"Koramangala",'
          '"merchant_code":"acme-sports","merchant_name":"Acme Sports"}',
          200,
          headers: {'content-type': 'application/json'},
        );
      }
      return http.Response('{"error":{"code":"NOT_FOUND","message":"not found"}}', 404);
    });
    await tester.pumpWidget(PosApp(api: ApiClient(baseUrl: 'http://test', httpClient: server)));
    await tester.pumpAndSettle();
    expect(find.text('Pair this terminal'), findsOneWidget);

    await tester.enterText(find.widgetWithText(TextField, 'Merchant code'), 'acme-sports');
    await tester.enterText(find.byKey(const Key('pairing-code')), 'AB3K9XQ2');
    await tester.pump();
    await tester.tap(find.widgetWithText(FilledButton, 'Pair terminal'));
    await tester.pumpAndSettle();

    expect(find.text('Pair this terminal'), findsNothing, reason: 'the pairing screen must go away');
    expect(find.text('Log in'), findsOneWidget, reason: 'the login screen must show');
    expect(find.text('Till 7 · Koramangala'), findsOneWidget);
    expect(find.widgetWithText(TextField, 'acme-sports'), findsOneWidget);
    expect((await TerminalConfig.load())?.terminalId, 't-1', reason: 'the pairing is stored for the next start');

    await tester.tap(find.text('Unpair this device'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'Unpair'));
    await tester.pumpAndSettle();
    expect(find.text('Log in'), findsNothing);
    expect(find.text('Pair this terminal'), findsOneWidget);
    expect(await TerminalConfig.load(), isNull);
  });
}
