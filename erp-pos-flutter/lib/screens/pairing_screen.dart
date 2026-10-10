import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../api/api_client.dart';
import '../state/device_id.dart';
import '../state/terminal_config.dart';
import '../ui/theme.dart';

/// First-run screen (Phase 12): turns this device into a specific POS
/// terminal. An admin opens Organization › POS Terminals in the web admin,
/// presses "Pair device" on a terminal and reads out an 8-character code;
/// the cashier types it here with the merchant code. Pairing binds this
/// device's fingerprint to that terminal server-side, so PIN logins from
/// any other device are refused (FRD §18 "registered devices only").
class PairingScreen extends StatefulWidget {
  final ApiClient api;
  final ValueChanged<TerminalConfig> onPaired;

  const PairingScreen({super.key, required this.api, required this.onPaired});

  @override
  State<PairingScreen> createState() => _PairingScreenState();
}

class _PairingScreenState extends State<PairingScreen> {
  final _merchantController = TextEditingController();
  final _codeController = TextEditingController();
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _merchantController.dispose();
    _codeController.dispose();
    super.dispose();
  }

  bool get _canSubmit => _merchantController.text.trim().isNotEmpty && _codeController.text.trim().length == 8 && !_busy;

  Future<void> _pair() async {
    if (!_canSubmit) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final result = await widget.api.pairTerminal(
        merchantCode: _merchantController.text.trim(),
        pairingCode: _codeController.text.trim(),
        deviceFingerprint: await DeviceId.get(),
      );
      final config = TerminalConfig(
        terminalId: result.terminalId,
        terminalName: result.terminalName,
        branchId: result.branchId,
        branchName: result.branchName,
        merchantCode: result.merchantCode,
        merchantName: result.merchantName,
      );
      await config.save();
      if (!mounted) return;
      widget.onPaired(config);
    } on ApiException catch (e) {
      setState(() => _error = e.message);
    } catch (_) {
      setState(() => _error = "Can't reach the server. Pairing needs a connection — check the network and try again.");
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      body: Center(
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(24),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 400),
            child: Card(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    Icon(Icons.point_of_sale, size: 40, color: theme.colorScheme.primary),
                    const SizedBox(height: 12),
                    Text('Pair this terminal', style: theme.textTheme.headlineSmall, textAlign: TextAlign.center),
                    const SizedBox(height: 8),
                    Text(
                      'In the web admin, open Organization › POS Terminals, choose this till and press "Pair device". '
                      'Enter the code it shows. Codes work once and expire after 15 minutes.',
                      style: theme.textTheme.bodyMedium?.copyWith(color: theme.colorScheme.onSurfaceVariant),
                      textAlign: TextAlign.center,
                    ),
                    const SizedBox(height: 20),
                    TextField(
                      controller: _merchantController,
                      autofocus: true,
                      textInputAction: TextInputAction.next,
                      decoration: const InputDecoration(labelText: 'Merchant code', hintText: 'e.g. adr_sports_world'),
                      onChanged: (_) => setState(() {}),
                    ),
                    const SizedBox(height: 12),
                    TextField(
                      key: const Key('pairing-code'),
                      controller: _codeController,
                      textCapitalization: TextCapitalization.characters,
                      maxLength: 8,
                      style: kMonoStyle.copyWith(fontSize: 22, letterSpacing: 6),
                      inputFormatters: [
                        FilteringTextInputFormatter.allow(RegExp('[A-Za-z0-9]')),
                        TextInputFormatter.withFunction((o, n) => n.copyWith(text: n.text.toUpperCase())),
                      ],
                      decoration: const InputDecoration(labelText: 'Pairing code', counterText: ''),
                      onChanged: (_) => setState(() {}),
                      onSubmitted: (_) => _pair(),
                    ),
                    if (_error != null) ...[
                      const SizedBox(height: 12),
                      Text(_error!, style: TextStyle(color: theme.colorScheme.error), key: const Key('pairing-error')),
                    ],
                    const SizedBox(height: 20),
                    FilledButton(
                      onPressed: _canSubmit ? _pair : null,
                      child: _busy
                          ? const SizedBox(width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2))
                          : const Text('Pair terminal'),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}
