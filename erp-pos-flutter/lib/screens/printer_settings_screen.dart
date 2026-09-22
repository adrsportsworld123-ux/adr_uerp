import 'package:flutter/material.dart';

import '../printing/printer_service.dart';

/// Configures the network ESC/POS printer PrinterService sends receipts
/// to (raw TCP, port 9100 by default — see PrinterService's own doc
/// comment for why this protocol and not a Bluetooth SDK). No printer
/// configured is a valid, supported state: ReceiptScreen's Print button
/// just surfaces a clear "no printer configured" message rather than
/// failing silently.
class PrinterSettingsScreen extends StatefulWidget {
  const PrinterSettingsScreen({super.key});

  @override
  State<PrinterSettingsScreen> createState() => _PrinterSettingsScreenState();
}

class _PrinterSettingsScreenState extends State<PrinterSettingsScreen> {
  final _printer = PrinterService();
  final _hostController = TextEditingController();
  final _portController = TextEditingController(text: '${PrinterService.defaultPort}');
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final host = await _printer.getConfiguredHost();
    final port = await _printer.getConfiguredPort();
    if (!mounted) return;
    setState(() {
      _hostController.text = host ?? '';
      _portController.text = '$port';
      _loading = false;
    });
  }

  @override
  void dispose() {
    _hostController.dispose();
    _portController.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final host = _hostController.text.trim();
    final port = int.tryParse(_portController.text.trim()) ?? PrinterService.defaultPort;
    if (host.isEmpty) {
      await _printer.clearPrinter();
    } else {
      await _printer.setPrinter(host: host, port: port);
    }
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(host.isEmpty ? 'Printer cleared' : 'Printer saved')),
    );
    Navigator.of(context).pop();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Printer settings')),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    'Network ESC/POS printer only (raw TCP, port 9100 by default) — '
                    'the same protocol most thermal receipt printers speak on a LAN. '
                    'Leave the host blank to disable printing.',
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                  const SizedBox(height: 16),
                  TextField(
                    controller: _hostController,
                    decoration: const InputDecoration(labelText: 'Printer IP address', border: OutlineInputBorder()),
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: _portController,
                    keyboardType: TextInputType.number,
                    decoration: const InputDecoration(labelText: 'Port', border: OutlineInputBorder()),
                  ),
                  const SizedBox(height: 24),
                  FilledButton(onPressed: _save, child: const Text('Save')),
                ],
              ),
            ),
    );
  }
}
