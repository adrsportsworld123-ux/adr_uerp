import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:erp_pos_app/ui/virtual_keyboard.dart';

void main() {
  testWidgets('debug', (tester) async {
    final c = TextEditingController();
    PosKeyboard.enabled.value = true;
    await tester.pumpWidget(MaterialApp(builder: (context, child) => VirtualKeyboardHost(child: child!), home: Scaffold(body: TextField(controller: c))));
    await tester.tap(find.byType(TextField));
    await tester.pump();
    await tester.tap(find.bySemanticsLabel('Hide keyboard'));
    await tester.pump();
    debugPrint('after hide q: ${find.text('q').evaluate().length}, show btn: ${find.bySemanticsLabel('Show keyboard').evaluate().length}');
    debugPrint('focus: ${FocusManager.instance.primaryFocus}');
    debugPrint('icon at ${tester.getCenter(find.byIcon(Icons.keyboard))}, semantics at ${tester.getCenter(find.bySemanticsLabel('Show keyboard'))}');
    final hits = tester.hitTestOnBinding(tester.getCenter(find.byIcon(Icons.keyboard)));
    debugPrint('hit path: ${hits.path.take(6).map((e) => e.target.runtimeType).toList()}');
    await tester.tap(find.byIcon(Icons.keyboard));
    await tester.pump();
    debugPrint('after show q: ${find.text('q').evaluate().length}, focus: ${FocusManager.instance.primaryFocus}');
  });
}
