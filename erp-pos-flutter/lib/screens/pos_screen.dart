import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/api_client.dart';
import '../state/session.dart';

/// The core POS screen: scan → cart → checkout, in one continuous flow —
/// this is what a cashier actually stares at all day, so it isn't split
/// across separate scan/cart screens the way a first pass might.
///
/// The barcode field is a plain TextField rather than a camera view: most
/// commodity POS barcode scanners are keyboard-wedge devices (they type
/// the decoded barcode + Enter into whatever field has focus, indistinguishable
/// from a human typing fast) — so this is the right input for real hardware,
/// not a placeholder for one. A camera-based scanner (for phones/tablets
/// without a dedicated scanner gun) is a real Phase 1 gap worth adding
/// alongside this, not a replacement for it.
class PosScreen extends StatefulWidget {
  const PosScreen({super.key});

  @override
  State<PosScreen> createState() => _PosScreenState();
}

class _PosScreenState extends State<PosScreen> {
  final _barcodeController = TextEditingController();
  final _barcodeFocusNode = FocusNode();
  bool _busy = false;

  @override
  void dispose() {
    _barcodeController.dispose();
    _barcodeFocusNode.dispose();
    super.dispose();
  }

  Future<void> _onBarcodeSubmitted(String code) async {
    if (code.trim().isEmpty || _busy) return;
    setState(() => _busy = true);

    final session = context.read<AppSession>();
    final product = await session.scanBarcode(code.trim());
    if (product != null) {
      await session.addToCart(product, 1);
    }

    _barcodeController.clear();
    if (!mounted) return;
    setState(() => _busy = false);
    _barcodeFocusNode.requestFocus();

    if (session.lastError != null) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(session.lastError!)));
    }
  }

  Future<void> _checkout() async {
    final session = context.read<AppSession>();
    final order = session.currentOrder;
    if (order == null || order.status != 'cart') return;

    final method = await showDialog<String>(
      context: context,
      builder: (ctx) => SimpleDialog(
        title: Text('Charge ₹${order.grandTotal}'),
        children: [
          for (final m in const ['cash', 'card', 'upi'])
            SimpleDialogOption(
              onPressed: () => Navigator.pop(ctx, m),
              child: Text(m.toUpperCase()),
            ),
        ],
      ),
    );
    if (method == null) return;

    setState(() => _busy = true);
    final grandTotal = double.tryParse(order.grandTotal) ?? 0;
    final ok = await session.checkout([PaymentInput(method: method, amount: grandTotal)]);
    if (!mounted) return;
    setState(() => _busy = false);

    if (ok) {
      await showDialog<void>(
        context: context,
        builder: (ctx) => AlertDialog(
          title: const Text('Sale complete'),
          content: Text('Receipt ${session.currentOrder!.orderNumber}\nTotal ₹${session.currentOrder!.grandTotal}'),
          actions: [
            FilledButton(
              onPressed: () {
                Navigator.pop(ctx);
                session.startNewSale();
              },
              child: const Text('New sale'),
            ),
          ],
        ),
      );
    } else if (session.lastError != null) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(session.lastError!)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    final order = session.currentOrder;

    return Scaffold(
      appBar: AppBar(title: const Text('New Sale')),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.all(16),
            child: TextField(
              controller: _barcodeController,
              focusNode: _barcodeFocusNode,
              autofocus: true,
              enabled: !_busy,
              decoration: InputDecoration(
                labelText: 'Scan or enter barcode',
                border: const OutlineInputBorder(),
                suffixIcon: _busy
                    ? const Padding(
                        padding: EdgeInsets.all(12),
                        child: SizedBox(height: 16, width: 16, child: CircularProgressIndicator(strokeWidth: 2)),
                      )
                    : const Icon(Icons.qr_code_scanner),
              ),
              onSubmitted: _onBarcodeSubmitted,
            ),
          ),
          Expanded(
            child: session.cartLines.isEmpty
                ? const Center(child: Text('Cart is empty — scan an item to begin'))
                : ListView.builder(
                    itemCount: session.cartLines.length,
                    itemBuilder: (context, i) {
                      final line = session.cartLines[i];
                      return ListTile(
                        title: Text(line.productName),
                        subtitle: Text('${line.sku} · qty ${line.quantity.toStringAsFixed(0)}'),
                        trailing: Text('₹${line.unitPrice}'),
                      );
                    },
                  ),
          ),
          if (order != null) _OrderTotalsBar(order: order, onCheckout: _busy ? null : _checkout),
        ],
      ),
    );
  }
}

class _OrderTotalsBar extends StatelessWidget {
  final OrderSummary order;
  final VoidCallback? onCheckout;

  const _OrderTotalsBar({required this.order, required this.onCheckout});

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: Container(
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          border: Border(top: BorderSide(color: Theme.of(context).dividerColor)),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            _totalRow('Subtotal', order.subtotal),
            _totalRow('Tax', order.taxTotal),
            _totalRow('Total', order.grandTotal, emphasize: true),
            const SizedBox(height: 12),
            FilledButton(
              onPressed: order.status == 'cart' ? onCheckout : null,
              child: Text(order.status == 'cart' ? 'Checkout' : 'Order ${order.status}'),
            ),
          ],
        ),
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
