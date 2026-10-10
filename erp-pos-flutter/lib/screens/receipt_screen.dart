import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/api_client.dart';
import '../printing/printer_service.dart';
import '../state/session.dart';
import '../ui/format.dart';
import 'printer_settings_screen.dart';

/// Shown right after a successful checkout — closes the real gap behind
/// Phase 1's "receipt-printing" Definition of Done item: until now this
/// app only showed a plain "Sale complete" dialog with the order number
/// and total, not an actual itemized receipt, and had no printing
/// integration of any kind. `receipt` is the same structured payload
/// erp-core-go's GET /sales/orders/{id}/receipt returns (§3.4/§3.8 of
/// phase0_1_design.md) — this screen is the print-ready *digital*
/// rendering of it; the Print button additionally sends the backend's
/// ESC/POS bytes to a configured network printer via PrinterService.
///
/// Phase 13: also opened from sales history with [orderId] set. Then Print
/// reprints that order, Void is not offered (it belongs to the sale just
/// made), and [onReturn] (when the sale can still be returned) starts a return.
class ReceiptScreen extends StatefulWidget {
  final ReceiptData receipt;
  final String? orderId;
  final VoidCallback? onReturn;

  const ReceiptScreen({super.key, required this.receipt, this.orderId, this.onReturn});

  bool get fromHistory => orderId != null;

  @override
  State<ReceiptScreen> createState() => _ReceiptScreenState();
}

class _ReceiptScreenState extends State<ReceiptScreen> {
  final _printer = PrinterService();
  bool _printing = false;
  bool _voiding = false;

  Future<void> _print() async {
    setState(() => _printing = true);
    final session = context.read<AppSession>();
    final ok = widget.fromHistory ? await session.printReceiptFor(widget.orderId!, _printer) : await session.printReceipt(_printer);
    if (!mounted) return;
    setState(() => _printing = false);
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(ok ? 'Sent to printer' : (session.lastError ?? 'Could not print'))),
    );
  }

  Future<void> _void() async {
    final session = context.read<AppSession>();
    final reason = await showDialog<String>(
      context: context,
      builder: (ctx) {
        final controller = TextEditingController();
        return AlertDialog(
          title: const Text('Void this sale?'),
          content: TextField(
            controller: controller,
            autofocus: true,
            decoration: const InputDecoration(labelText: 'Reason', border: OutlineInputBorder()),
          ),
          actions: [
            TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
            FilledButton(
              onPressed: () => Navigator.pop(ctx, controller.text.trim()),
              child: const Text('Void'),
            ),
          ],
        );
      },
    );
    if (reason == null || reason.isEmpty) return;

    setState(() => _voiding = true);
    final ok = await session.voidCurrentOrder(reason);
    if (!mounted) return;
    setState(() => _voiding = false);
    if (ok) {
      ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('Sale voided')));
      Navigator.of(context).pop();
    } else if (session.lastError != null) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(session.lastError!)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final r = widget.receipt;
    return Scaffold(
      appBar: AppBar(
        title: Text('Receipt ${r.orderNumber}'),
        actions: [
          IconButton(
            icon: const Icon(Icons.settings_ethernet),
            tooltip: 'Printer settings',
            onPressed: () => Navigator.of(context).push(
              MaterialPageRoute(builder: (_) => const PrinterSettingsScreen()),
            ),
          ),
        ],
      ),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Center(
            child: Column(
              children: [
                Text(r.merchantName, style: Theme.of(context).textTheme.titleLarge, textAlign: TextAlign.center),
                Text(r.branchName, style: Theme.of(context).textTheme.bodyMedium),
                const SizedBox(height: 4),
                Text('Order ${r.orderNumber}', style: Theme.of(context).textTheme.bodySmall),
                Text('Cashier: ${r.cashierName}', style: Theme.of(context).textTheme.bodySmall),
                if (r.status != 'finalized')
                  Padding(
                    padding: const EdgeInsets.only(top: 6),
                    child: Chip(label: Text(statusLabel(r.status))),
                  ),
                if (r.customerName != null) Text('Customer: ${r.customerName}', style: Theme.of(context).textTheme.bodySmall),
              ],
            ),
          ),
          const Divider(height: 32),
          for (final line in r.lines)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 4),
              child: Row(
                children: [
                  Expanded(
                    child: Text('${line.productName}\n${line.sku} · ${line.quantity} × ₹${line.unitPrice}'),
                  ),
                  Text('₹${line.lineTotal}'),
                ],
              ),
            ),
          const Divider(height: 32),
          _totalRow('Subtotal', r.subtotal),
          if (double.tryParse(r.discountTotal) != null && double.parse(r.discountTotal) > 0)
            _totalRow('Discount', '-${r.discountTotal}'),
          _totalRow('Tax', r.taxTotal),
          _totalRow('Total', r.grandTotal, emphasize: true),
          const SizedBox(height: 16),
          for (final p in r.payments)
            _totalRow(p.method.toUpperCase(), p.amount),
          const SizedBox(height: 24),
          FilledButton.icon(
            onPressed: _printing ? null : _print,
            icon: _printing
                ? const SizedBox(height: 16, width: 16, child: CircularProgressIndicator(strokeWidth: 2))
                : const Icon(Icons.print),
            label: const Text('Print receipt'),
          ),
          if (widget.onReturn != null) ...[
            const SizedBox(height: 8),
            OutlinedButton.icon(
              key: const Key('receipt-return'),
              onPressed: widget.onReturn,
              icon: const Icon(Icons.assignment_return_outlined),
              label: const Text('Return items'),
            ),
          ],
          if (!widget.fromHistory) ...[
            const SizedBox(height: 8),
            OutlinedButton.icon(
              onPressed: _voiding ? null : _void,
              icon: _voiding
                  ? const SizedBox(height: 16, width: 16, child: CircularProgressIndicator(strokeWidth: 2))
                  : const Icon(Icons.undo),
              label: const Text('Void this sale'),
            ),
          ],
          const SizedBox(height: 8),
          FilledButton.tonal(
            onPressed: () => Navigator.of(context).pop(),
            child: Text(widget.fromHistory ? 'Close' : 'New sale'),
          ),
        ],
      ),
    );
  }

  Widget _totalRow(String label, String value, {bool emphasize = false}) {
    final style = emphasize ? const TextStyle(fontWeight: FontWeight.bold, fontSize: 18) : null;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.spaceBetween,
        children: [Text(label, style: style), Text('₹$value', style: style)],
      ),
    );
  }
}
