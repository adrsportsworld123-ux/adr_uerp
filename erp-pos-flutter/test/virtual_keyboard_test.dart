import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:erp_pos_app/api/api_client.dart';
import 'package:erp_pos_app/ui/virtual_keyboard.dart';

// The till's on-screen keyboard (erp-core-go migrations/072): off by
// default; on, it types into whichever field has focus, letters or a
// number pad to suit the field, without taking the focus away.
void main() {
  late TextEditingController name, qty;
  String? submitted;

  Widget app() => MaterialApp(
        builder: (context, child) => VirtualKeyboardHost(child: child!),
        home: Scaffold(
          body: Column(
            children: [
              TextField(key: const Key('name'), controller: name),
              TextField(
                key: const Key('qty'),
                controller: qty,
                keyboardType: TextInputType.number,
                inputFormatters: [FilteringTextInputFormatter.digitsOnly],
                onSubmitted: (v) => submitted = v,
              ),
            ],
          ),
        ),
      );

  setUp(() {
    name = TextEditingController();
    qty = TextEditingController();
    submitted = null;
    PosKeyboard.enabled.value = false;
  });

  testWidgets('off (the default): no keyboard', (tester) async {
    await tester.pumpWidget(app());
    await tester.tap(find.byKey(const Key('name')));
    await tester.pump();
    expect(find.text('q'), findsNothing);
  });

  testWidgets('letters type into the focused field and keep its focus', (tester) async {
    PosKeyboard.enabled.value = true;
    await tester.pumpWidget(app());
    await tester.tap(find.byKey(const Key('name')));
    await tester.pump();
    expect(find.text('q'), findsOneWidget);

    await tester.tap(find.text('q'));
    await tester.tap(find.text('w'));
    await tester.tap(find.bySemanticsLabel('Shift'));
    await tester.pump();
    await tester.tap(find.text('A'));
    await tester.pump();
    expect(name.text, 'qwA');
    await tester.tap(find.text('Space'));
    await tester.tap(find.text('1'));
    await tester.tap(find.bySemanticsLabel('Backspace'));
    await tester.pump();
    expect(name.text, 'qwA ');
    final field = tester.widget<EditableText>(find.descendant(of: find.byKey(const Key('name')), matching: find.byType(EditableText)));
    expect(field.focusNode.hasFocus, isTrue, reason: 'a key tap never moves focus');

    await tester.tap(find.text('?123'));
    await tester.pump();
    await tester.tap(find.text('@'));
    await tester.pump();
    expect(name.text, 'qwA @');
  });

  testWidgets('a number field gets the number pad; its formatter still applies', (tester) async {
    PosKeyboard.enabled.value = true;
    await tester.pumpWidget(app());
    await tester.tap(find.byKey(const Key('qty')));
    await tester.pump();
    expect(find.text('q'), findsNothing);
    await tester.tap(find.text('1'));
    await tester.tap(find.text('2'));
    await tester.tap(find.text('.'));
    await tester.pump();
    expect(qty.text, '12', reason: 'digits only: the dot is refused by the field');
    await tester.tap(find.bySemanticsLabel('Enter'));
    await tester.pump();
    expect(submitted, '12');
  });

  testWidgets('hide tucks it away; the keyboard button brings it back', (tester) async {
    PosKeyboard.enabled.value = true;
    await tester.pumpWidget(app());
    await tester.tap(find.byKey(const Key('name')));
    await tester.pump();
    await tester.tap(find.bySemanticsLabel('Hide keyboard'));
    await tester.pump();
    expect(find.text('q'), findsNothing);
    await tester.tap(find.bySemanticsLabel('Show keyboard'));
    await tester.pump();
    expect(find.text('q'), findsOneWidget);
    // Switched off at the terminal: gone at once.
    PosKeyboard.enabled.value = false;
    await tester.pump();
    expect(find.text('q'), findsNothing);
  });

  test('the till setting is read from the server and cached', () {
    expect(PosLocalization.fromJson({'language': 'en'}).virtualKeyboard, isFalse);
    final l = PosLocalization.fromJson({'language': 'en', 'virtual_keyboard': true});
    expect(l.virtualKeyboard, isTrue);
    expect(PosLocalization.fromJson(l.toJson()).virtualKeyboard, isTrue);
  });
}
