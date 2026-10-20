import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../l10n/l10n.dart';
import '../state/session.dart';
import '../ui/theme.dart';

/// Login screen — the first half of Phase 0's "walking skeleton" goal
/// (login screen → barcode scan screen hitting a real endpoint). Talks to
/// POST /auth/login exactly as documented in phase0_1_design.md §3.1, or
/// POST /auth/pin-login (FRD §18 quick-login) when the cashier switches to
/// PIN mode below — the backend has supported both since the Phase 1
/// hardening pass; this screen previously only exposed the password path.
class LoginScreen extends StatefulWidget {
  const LoginScreen({super.key});

  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

enum _LoginMode { password, pin }

class _LoginScreenState extends State<LoginScreen> {
  _LoginMode _mode = _LoginMode.password;

  final _merchantCodeController = TextEditingController(text: 'adr_sports_world');
  final _emailController = TextEditingController(text: '');
  final _passwordController = TextEditingController();

  final _employeeCodeController = TextEditingController(text: 'EMP001');
  final _pinController = TextEditingController();

  bool _submitting = false;

  @override
  void initState() {
    super.initState();
    // Phase 12: a paired terminal knows its merchant; no retyping it.
    final code = context.read<AppSession>().merchantCode;
    if (code.isNotEmpty) _merchantCodeController.text = code;
  }

  Future<void> _confirmUnpair() async {
    final session = context.read<AppSession>();
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(tr('Unpair this device?')),
        content: Text(
          tr('This device will stop being {0}. To use it as a till again, an admin issues a new pairing code on the POS Terminals screen.', [session.terminalName]),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx, false), child: Text(tr('Cancel'))),
          FilledButton(onPressed: () => Navigator.pop(ctx, true), child: Text(tr('Unpair'))),
        ],
      ),
    );
    if (ok == true) session.onUnpairDevice?.call();
  }

  @override
  void dispose() {
    _merchantCodeController.dispose();
    _emailController.dispose();
    _passwordController.dispose();
    _employeeCodeController.dispose();
    _pinController.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    setState(() => _submitting = true);
    final session = context.read<AppSession>();
    final ok = _mode == _LoginMode.password
        ? await session.login(
            _merchantCodeController.text.trim(),
            _emailController.text.trim(),
            _passwordController.text,
          )
        : await session.loginWithPin(
            _employeeCodeController.text.trim(),
            _pinController.text.trim(),
          );
    if (!mounted) return;
    setState(() => _submitting = false);
    if (!ok && session.lastError != null) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(session.lastError!)));
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 360),
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                const Icon(Icons.storefront, size: 48),
                const SizedBox(height: 8),
                Text('ERP POS', style: Theme.of(context).textTheme.headlineSmall, textAlign: TextAlign.center),
                const SizedBox(height: 4),
                Text(
                  [context.read<AppSession>().terminalName, context.read<AppSession>().branchName]
                      .where((s) => s.isNotEmpty)
                      .join(' · '),
                  key: const Key('terminal-identity'),
                  textAlign: TextAlign.center,
                  style: Theme.of(context).textTheme.bodyMedium?.copyWith(color: Theme.of(context).colorScheme.onSurfaceVariant),
                ),
                const SizedBox(height: 16),
                // Set when AppSession ended a session (inactivity or a
                // rejected refresh) rather than a manual sign-out.
                if (context.watch<AppSession>().sessionEndedNotice case final notice?) ...[
                  Container(
                    key: const Key('session-ended-notice'),
                    padding: const EdgeInsets.all(12),
                    decoration: BoxDecoration(
                      color: Theme.of(context).pos.warnSoft,
                      border: Border.all(color: Theme.of(context).pos.warn.withValues(alpha: 0.4)),
                      borderRadius: BorderRadius.circular(8),
                    ),
                    child: Text(notice, style: TextStyle(color: Theme.of(context).pos.warn)),
                  ),
                  const SizedBox(height: 16),
                ],
                SegmentedButton<_LoginMode>(
                  segments: [
                    ButtonSegment(value: _LoginMode.password, label: Text(tr('Password'))),
                    const ButtonSegment(value: _LoginMode.pin, label: Text('PIN')),
                  ],
                  selected: {_mode},
                  onSelectionChanged: (s) => setState(() => _mode = s.first),
                ),
                const SizedBox(height: 16),
                if (_mode == _LoginMode.password) ..._passwordFields() else ..._pinFields(),
                const SizedBox(height: 24),
                FilledButton(
                  onPressed: _submitting ? null : _submit,
                  child: _submitting
                      ? const SizedBox(height: 20, width: 20, child: CircularProgressIndicator(strokeWidth: 2))
                      : Text(tr('Log in')),
                ),
                if (context.read<AppSession>().onUnpairDevice != null) ...[
                  const SizedBox(height: 8),
                  TextButton(onPressed: _submitting ? null : _confirmUnpair, child: Text(tr('Unpair this device'))),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }

  List<Widget> _passwordFields() => [
        TextField(
          controller: _merchantCodeController,
          decoration: InputDecoration(labelText: tr('Merchant code'), border: const OutlineInputBorder()),
        ),
        const SizedBox(height: 12),
        TextField(
          controller: _emailController,
          keyboardType: TextInputType.emailAddress,
          decoration: InputDecoration(labelText: tr('Email'), border: const OutlineInputBorder()),
        ),
        const SizedBox(height: 12),
        TextField(
          controller: _passwordController,
          obscureText: true,
          onSubmitted: (_) => _submit(),
          decoration: InputDecoration(labelText: tr('Password'), border: const OutlineInputBorder()),
        ),
      ];

  List<Widget> _pinFields() => [
        TextField(
          controller: _employeeCodeController,
          decoration: InputDecoration(labelText: tr('Employee code'), border: const OutlineInputBorder()),
        ),
        const SizedBox(height: 12),
        TextField(
          controller: _pinController,
          obscureText: true,
          keyboardType: TextInputType.number,
          maxLength: 6,
          onSubmitted: (_) => _submit(),
          decoration: const InputDecoration(labelText: 'PIN', border: OutlineInputBorder(), counterText: ''),
        ),
        Text(
          tr('Sign in with your employee code and PIN. Only the device paired to this terminal is accepted (FRD §18).'),
          style: Theme.of(context).textTheme.bodySmall,
          textAlign: TextAlign.center,
        ),
      ];
}
