import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import 'api/api_client.dart';
import 'state/session.dart';
import 'screens/login_screen.dart';
import 'screens/pos_screen.dart';

/// Points at erp-core-go running via `docker compose up` per that repo's
/// README. Android emulators reach the host machine at 10.0.2.2, not
/// localhost — swap this per platform/environment before running on a
/// real device or iOS simulator (localhost works there).
const String kApiBaseUrl = String.fromEnvironment(
  'API_BASE_URL',
  defaultValue: 'http://10.0.2.2:8080',
);

// Matches migrations/002_seed.sql in erp-core-go. See the TODO in
// state/session.dart — a real device-binding flow replaces this.
const String kSeedBranchId = '22222222-2222-2222-2222-222222222222';
const String kSeedPosTerminalId = '33333333-3333-3333-3333-333333333333';

void main() {
  runApp(const PosApp());
}

class PosApp extends StatelessWidget {
  const PosApp({super.key});

  @override
  Widget build(BuildContext context) {
    return ChangeNotifierProvider(
      create: (_) => AppSession(
        api: ApiClient(baseUrl: kApiBaseUrl),
        branchId: kSeedBranchId,
        posTerminalId: kSeedPosTerminalId,
      ),
      child: MaterialApp(
        title: 'ERP POS',
        theme: ThemeData(colorSchemeSeed: Colors.indigo, useMaterial3: true),
        home: const RootScreen(),
      ),
    );
  }
}

class RootScreen extends StatelessWidget {
  const RootScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    return session.isLoggedIn ? const PosScreen() : const LoginScreen();
  }
}
