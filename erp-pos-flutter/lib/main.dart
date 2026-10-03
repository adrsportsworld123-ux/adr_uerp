import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
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

/// Lets RootScreen clear any pushed route/dialog when a session ends, so
/// LoginScreen isn't left hidden underneath e.g. the product search screen.
final GlobalKey<NavigatorState> kNavigatorKey = GlobalKey<NavigatorState>();

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
      child: _ActivityTracker(
        child: MaterialApp(
          title: 'ERP POS',
          navigatorKey: kNavigatorKey,
          theme: ThemeData(colorSchemeSeed: Colors.indigo, useMaterial3: true),
          home: const RootScreen(),
        ),
      ),
    );
  }
}

/// Feeds real cashier input into AppSession's inactivity timeout: any
/// pointer-down anywhere, plus hardware key events — a USB/Bluetooth
/// barcode scanner is a keyboard, so a cashier scanning item after item
/// without touching the screen still counts as active. Also re-checks the
/// session when the app returns to the foreground (timers don't run while
/// a mobile app is suspended).
class _ActivityTracker extends StatefulWidget {
  final Widget child;
  const _ActivityTracker({required this.child});

  @override
  State<_ActivityTracker> createState() => _ActivityTrackerState();
}

class _ActivityTrackerState extends State<_ActivityTracker> with WidgetsBindingObserver {
  late final AppSession _session = context.read<AppSession>();

  bool _onKey(KeyEvent event) {
    _session.markActivity();
    return false; // observe only — never swallow the key
  }

  @override
  void initState() {
    super.initState();
    HardwareKeyboard.instance.addHandler(_onKey);
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) _session.checkSession();
  }

  @override
  void dispose() {
    HardwareKeyboard.instance.removeHandler(_onKey);
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Listener(
      behavior: HitTestBehavior.translucent,
      onPointerDown: (_) => _session.markActivity(),
      child: widget.child,
    );
  }
}

class RootScreen extends StatelessWidget {
  const RootScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    if (!session.isLoggedIn) {
      // A session can end while a pushed route or dialog is on top.
      WidgetsBinding.instance.addPostFrameCallback((_) => kNavigatorKey.currentState?.popUntil((r) => r.isFirst));
    }
    return session.isLoggedIn ? const PosScreen() : const LoginScreen();
  }
}
