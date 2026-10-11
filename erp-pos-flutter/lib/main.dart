import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import 'api/api_client.dart';
import 'state/session.dart';
import 'state/terminal_config.dart';
import 'screens/login_screen.dart';
import 'screens/pairing_screen.dart';
import 'screens/pos_screen.dart';
import 'ui/theme.dart';

/// Points at erp-core-go running via `docker compose up` per that repo's
/// README. Android emulators reach the host machine at 10.0.2.2, not
/// localhost — swap this per platform/environment before running on a
/// real device or iOS simulator (localhost works there).
const String kApiBaseUrl = String.fromEnvironment(
  'API_BASE_URL',
  defaultValue: 'http://10.0.2.2:8080',
);

// Development shortcut: run with --dart-define=USE_SEED_TERMINAL=true to
// skip pairing and use the seed terminal (migrations/002_seed.sql) against
// a fresh docker-compose backend. Real devices pair (Phase 12).
const bool kUseSeedTerminal = bool.fromEnvironment('USE_SEED_TERMINAL');

/// Lets RootScreen clear any pushed route/dialog when a session ends, so
/// LoginScreen isn't left hidden underneath e.g. the product search screen.
final GlobalKey<NavigatorState> kNavigatorKey = GlobalKey<NavigatorState>();

void main() {
  runApp(const PosApp());
}

/// Boots into the pairing screen until this device knows which terminal it
/// is (Phase 12), then into login/POS for that terminal. [initialConfig]
/// lets tests start already paired without touching device storage; [api]
/// lets them talk to a fake server.
class PosApp extends StatefulWidget {
  final TerminalConfig? initialConfig;
  final ApiClient? api;

  const PosApp({super.key, this.initialConfig, this.api});

  @override
  State<PosApp> createState() => _PosAppState();
}

class _PosAppState extends State<PosApp> {
  late final ApiClient _api = widget.api ?? ApiClient(baseUrl: kApiBaseUrl);
  TerminalConfig? _config;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    if (widget.initialConfig != null || kUseSeedTerminal) {
      _config = widget.initialConfig ?? TerminalConfig.seed;
      _loading = false;
    } else {
      _load();
    }
  }

  Future<void> _load() async {
    TerminalConfig? config;
    try {
      config = await TerminalConfig.load();
    } catch (_) {
      config = null; // storage unavailable: treat as unpaired
    }
    if (!mounted) return;
    setState(() {
      _config = config;
      _loading = false;
    });
  }

  Future<void> _unpair() async {
    try {
      await TerminalConfig.clear();
    } catch (_) {
      // nothing stored to clear
    }
    if (mounted) setState(() => _config = null);
  }

  // Only the paired app owns kNavigatorKey. The loading, pairing and paired
  // apps sit at different places in the tree; sharing one GlobalKey made
  // Flutter carry the old Navigator (and its pairing-screen route) over to
  // the paired app, so pairing never reached the login screen and
  // unpairing never left it.
  MaterialApp _app(Widget home, {bool paired = false}) => MaterialApp(
        title: 'ERP POS',
        navigatorKey: paired ? kNavigatorKey : null,
        theme: posTheme(Brightness.light),
        darkTheme: posTheme(Brightness.dark),
        themeMode: ThemeMode.light,
        home: home,
      );

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return _app(const Scaffold(body: Center(child: CircularProgressIndicator())));
    }
    final config = _config;
    if (config == null) {
      return _app(PairingScreen(api: _api, onPaired: (c) => setState(() => _config = c)));
    }
    return ChangeNotifierProvider(
      // A different terminal is a different session, never a mutated one.
      key: ValueKey(config.terminalId),
      create: (_) => AppSession(
        api: _api,
        branchId: config.branchId,
        posTerminalId: config.terminalId,
        terminalName: config.terminalName,
        branchName: config.branchName,
        merchantCode: config.merchantCode,
      )..onUnpairDevice = _unpair,
      child: _ActivityTracker(child: _app(const RootScreen(), paired: true)),
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
