import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../state/session.dart';

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

  final _merchantCodeController = TextEditingController(text: 'acme-sports');
  final _emailController = TextEditingController(text: 'ravi@acme-sports.test');
  final _passwordController = TextEditingController();

  final _employeeCodeController = TextEditingController(text: 'EMP001');
  final _pinController = TextEditingController();

  bool _submitting = false;

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
                const SizedBox(height: 16),
                SegmentedButton<_LoginMode>(
                  segments: const [
                    ButtonSegment(value: _LoginMode.password, label: Text('Password')),
                    ButtonSegment(value: _LoginMode.pin, label: Text('PIN')),
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
                      : const Text('Log in'),
                ),
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
          decoration: const InputDecoration(labelText: 'Merchant code', border: OutlineInputBorder()),
        ),
        const SizedBox(height: 12),
        TextField(
          controller: _emailController,
          keyboardType: TextInputType.emailAddress,
          decoration: const InputDecoration(labelText: 'Email', border: OutlineInputBorder()),
        ),
        const SizedBox(height: 12),
        TextField(
          controller: _passwordController,
          obscureText: true,
          onSubmitted: (_) => _submit(),
          decoration: const InputDecoration(labelText: 'Password', border: OutlineInputBorder()),
        ),
      ];

  List<Widget> _pinFields() => [
        TextField(
          controller: _employeeCodeController,
          decoration: const InputDecoration(labelText: 'Employee code', border: OutlineInputBorder()),
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
          'This terminal binds to the first device that logs in with a given '
          'employee code — see FRD §18.',
          style: Theme.of(context).textTheme.bodySmall,
          textAlign: TextAlign.center,
        ),
      ];
}
