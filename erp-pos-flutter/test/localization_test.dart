// Localization (erp-core-go migrations/064): every on-screen string has a
// translation in each language, tr() fills placeholders and falls back to
// English, the terminal's direction and cart side apply, and the billing
// screen renders right-to-left in Arabic with the cart on the side the
// terminal asks for. Offline, against its own SQLite folder.
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/testing.dart';
import 'package:provider/provider.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import 'package:erp_pos_app/api/api_client.dart';
import 'package:erp_pos_app/l10n/l10n.dart';
import 'package:erp_pos_app/screens/pos_screen.dart';
import 'package:erp_pos_app/state/session.dart';
import 'package:erp_pos_app/ui/format.dart';
import 'package:erp_pos_app/ui/theme.dart';

/// Every literal passed to tr() in lib/, as it reads at run time.
Set<String> _sourceKeys() {
  final keys = <String>{};
  final call = RegExp(r"\btr\('((?:[^'\\]|\\.)*)'");
  for (final f in Directory('lib').listSync(recursive: true).whereType<File>()) {
    if (!f.path.endsWith('.dart') || f.path.contains('l10n')) continue;
    for (final m in call.allMatches(f.readAsStringSync())) {
      keys.add(m[1]!.replaceAll(r"\'", "'").replaceAll(r'\$', r'$').replaceAll(r'\n', '\n'));
    }
  }
  return keys;
}

List<String> _placeholders(String s) => (RegExp(r'\{\d+\}').allMatches(s).map((m) => m[0]!).toList()..sort());

Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 8; i++) {
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 25)));
    await tester.pump(const Duration(milliseconds: 16));
  }
}

Future<void> _pumpBilling(WidgetTester tester) async {
  tester.view.physicalSize = const Size(1280, 800);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final api = ApiClient(baseUrl: 'http://pos-test.invalid', httpClient: MockClient((_) async => throw const SocketException('offline (test)')));
  final s = AppSession(api: api, branchId: 'b', posTerminalId: 't', terminalName: 'Counter', branchName: 'Branch', merchantCode: 'm');
  s.userId = 'u';
  s.offlineMode = true;
  await tester.pumpWidget(MaterialApp(
    theme: posTheme(Brightness.light),
    builder: (context, child) => Directionality(textDirection: PosLocale.direction, child: child!),
    home: ChangeNotifierProvider<AppSession>.value(value: s, child: const PosScreen()),
  ));
  await _settle(tester);
}

void main() {
  setUpAll(() async {
    sqfliteFfiInit();
    databaseFactory = databaseFactoryFfi;
    await databaseFactory.setDatabasesPath(Directory.systemTemp.createTempSync('pos_l10n_test').path);
  });
  tearDown(() {
    PosLocale.reset();
    PosClock.utcOffsetMinutes = null;
  });

  test('every on-screen string is translated, with the same placeholders', () {
    final keys = _sourceKeys();
    expect(keys.length, greaterThan(400));
    for (final lang in translatedLanguages()) {
      final dict = dictionaryFor(lang);
      final missing = keys.where((k) => !dict.containsKey(k)).toList();
      expect(missing, isEmpty, reason: '$lang has no translation for: ${missing.take(5).join(' | ')}');
      for (final k in keys) {
        expect(_placeholders(dict[k]!), _placeholders(k), reason: '$lang: "$k"');
      }
    }
  });

  test('tr: English as written, translations with their placeholders filled', () {
    expect(tr('Cart'), 'Cart');
    expect(tr('Order {0}', ['A-12']), 'Order A-12');
    PosLocale.apply(language: 'hi');
    expect(tr('Order {0}', ['A-12']), 'ऑर्डर A-12');
    // A translation may move a placeholder.
    PosLocale.apply(language: 'ar');
    expect(tr('Give back {0} by {1}.', ['₹10.00', 'UPI']), 'أعِد ₹10.00 عبر UPI.');
    expect(tr('A string nobody translated'), 'A string nobody translated');
    PosLocale.apply(language: 'de'); // not offered: English
    expect(PosLocale.language, 'en');
  });

  test('direction follows the language unless the terminal overrides it; cart side', () {
    expect(PosLocale.apply(language: 'ar', textDirection: 'auto'), isTrue);
    expect(PosLocale.direction, TextDirection.rtl);
    PosLocale.apply(language: 'ar', textDirection: 'ltr');
    expect(PosLocale.direction, TextDirection.ltr);
    PosLocale.apply(language: 'ta', textDirection: 'rtl', cartSide: 'left');
    expect(PosLocale.direction, TextDirection.rtl);
    expect(PosLocale.cartOnLeft, isTrue);
    expect(PosLocale.apply(language: 'ta', textDirection: 'rtl', cartSide: 'left'), isFalse, reason: 'nothing changed: no rebuild');
  });

  test('the server settings: denominations, clock, and an older server', () {
    final l = PosLocalization.fromJson({
      'language': 'ar',
      'direction': 'rtl',
      'cart_side': 'left',
      'timezone': 'Asia/Dubai',
      'utc_offset_minutes': 240,
      'denominations': ['1000.00', '500.00', '0.25'],
    });
    expect(l.denominations, [1000.0, 500.0, 0.25]);
    expect(PosLocalization.fromJson(l.toJson()).denominations, l.denominations);
    expect(const PosLocalization().denominations.first, 2000.0, reason: 'INR notes by default');
    expect(PosLocalization.fromJson(const {}).language, 'en');

    AppSession.applyLocalization(l);
    expect(PosLocale.language, 'ar');
    expect(PosLocale.cartOnLeft, isTrue);
    final t = PosClock.parse('2026-10-20T06:30:00Z')!;
    expect('${t.hour}:${t.minute}', '10:30', reason: 'Dubai is UTC+4');
  });

  testWidgets('English billing screen: left-to-right, cart on the right (as before)', (tester) async {
    await _pumpBilling(tester);
    final scan = tester.getCenter(find.widgetWithText(TextField, 'Scan or enter barcode'));
    final cart = tester.getCenter(find.text('Cart is empty — scan an item to begin'));
    expect(cart.dx, greaterThan(scan.dx));
  });

  testWidgets('Arabic billing screen: right-to-left text, the cart where the terminal puts it', (tester) async {
    PosLocale.apply(language: 'ar', cartSide: 'left');
    await _pumpBilling(tester);
    expect(find.text('السلة فارغة — امسح صنفاً للبدء'), findsOneWidget);
    final scanField = find.widgetWithText(TextField, 'امسح الباركود أو أدخله');
    expect(Directionality.of(tester.element(scanField)), TextDirection.rtl);
    final cart = tester.getCenter(find.text('السلة فارغة — امسح صنفاً للبدء'));
    expect(cart.dx, lessThan(tester.getCenter(scanField).dx), reason: 'cart side left');
  });
}
