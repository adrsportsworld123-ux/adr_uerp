// Replaces the default `flutter create` counter-app boilerplate, which
// referenced MyApp — a class that only exists in Flutter's standard
// starter template, not in this app (see lib/main.dart's PosApp). This
// test actually exercises this app: an unauthenticated session shows the
// login screen, not a blank/crashed widget tree.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:erp_pos_app/main.dart';

void main() {
  testWidgets('shows the login screen when not authenticated', (WidgetTester tester) async {
    await tester.pumpWidget(const PosApp());

    expect(find.text('ERP POS'), findsOneWidget);
    expect(find.text('Log in'), findsOneWidget);
    expect(find.byType(TextField), findsNWidgets(3)); // merchant code, email, password
  });
}
