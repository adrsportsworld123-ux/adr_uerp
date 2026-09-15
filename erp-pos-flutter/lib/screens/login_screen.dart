import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../state/session.dart';

/// Login screen — the first half of Phase 0's "walking skeleton" goal
/// (login screen → barcode scan screen hitting a real endpoint). Talks to
/// POST /auth/login exactly as documented in phase0_1_design.md §3.1.
class LoginScreen extends StatefulWidget {
  const LoginScreen({super.key});

  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  final _merchantCodeController = TextEditingController(text: 'acme-sports');
  final _emailController = TextEditingController(text: 'ravi@acme-sports.test');
  final _passwordController = TextEditingController();
  bool _submitting = false;

  @override
  void dispose() {
    _merchantCodeController.dispose();
    _emailController.dispose();
    _passwordController.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    setState(() => _submitting = true);
    final session = context.read<AppSession>();
    final ok = await session.login(
      _merchantCodeController.text.trim(),
      _emailController.text.trim(),
      _passwordController.text,
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
                const SizedBox(height: 24),
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
                const SizedBox(height: 24),
                FilledButton(
                  onPressed: _submitting ? null : _submit,
                  child: _submitting
                      ? const SizedBox(height: 20, width: 20, child: CircularProgressIndicator(strokeWidth: 2))
                      : const Text('Log in'),
                ),
                const SizedBox(height: 8),
                Text(
                  'PIN quick-login (FRD §18) is a Phase 1 follow-up — this '
                  'screen covers the username/password path only.',
                  style: Theme.of(context).textTheme.bodySmall,
                  textAlign: TextAlign.center,
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
