import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/api_client.dart';
import '../state/session.dart';

/// Lists every cart currently on hold at this branch (GET
/// /sales/orders/held) and recalls one back into the active cart on tap —
/// see erp-core-go's internal/sales/hold.go. Reached from the POS screen's
/// app bar. Recalling here replaces whatever's on the POS screen, so
/// AppSession.recallOrder itself refuses (with a clear error surfaced
/// below) if there's already an unsaved live cart in progress — the
/// cashier holds or finishes it first.
///
/// Pops with the recalled order's `unavailableLines` (possibly empty) on a
/// successful recall, or nothing (`null`) if the cashier backs out.
///
/// Each row can also be voided (internal/sales/void_held.go): a reason is
/// required; its stock holds go back on sale at once. Voiding another
/// cashier's hold needs sales.void — the server says so if it's missing.
class HeldOrdersScreen extends StatefulWidget {
  const HeldOrdersScreen({super.key});

  @override
  State<HeldOrdersScreen> createState() => _HeldOrdersScreenState();
}

class _HeldOrdersScreenState extends State<HeldOrdersScreen> {
  List<HeldOrderSummary> _held = [];
  bool _loading = true;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loading = true);
    final held = await context.read<AppSession>().listHeldOrders();
    if (!mounted) return;
    setState(() {
      _held = held;
      _loading = false;
    });
  }

  Future<void> _recall(HeldOrderSummary order) async {
    if (_busy) return;
    setState(() => _busy = true);
    final session = context.read<AppSession>();
    final result = await session.recallOrder(order.orderId);
    if (!mounted) return;
    setState(() => _busy = false);
    if (result == null) {
      if (session.lastError != null) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(session.lastError!)));
      }
      return;
    }
    Navigator.of(context).pop(result.unavailableLines);
  }

  Future<void> _void(HeldOrderSummary order) async {
    if (_busy) return;
    final reason = await showDialog<String>(context: context, builder: (_) => _VoidHeldDialog(order: order));
    if (reason == null || !mounted) return;
    setState(() => _busy = true);
    final session = context.read<AppSession>();
    final ok = await session.voidHeldOrder(order.orderId, reason);
    if (!mounted) return;
    setState(() => _busy = false);
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(ok ? 'Held order ${order.orderNumber} voided — its items are back on sale' : (session.lastError ?? 'Could not void'))),
    );
    if (ok) _load();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Held Orders')),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : _held.isEmpty
              ? const Center(child: Text('No carts are currently on hold'))
              : RefreshIndicator(
                  onRefresh: _load,
                  child: ListView.builder(
                    itemCount: _held.length,
                    itemBuilder: (context, i) {
                      final o = _held[i];
                      return ListTile(
                        enabled: !_busy,
                        leading: const Icon(Icons.pause_circle_outline),
                        title: Text(o.orderNumber),
                        subtitle: Text(
                          [
                            if (o.holdNote.isNotEmpty) o.holdNote,
                            '${o.lineCount} item(s)',
                          ].join(' · '),
                        ),
                        trailing: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Text('₹${o.grandTotal}'),
                            const SizedBox(width: 8),
                            IconButton(
                              key: Key('void-held-${o.orderId}'),
                              tooltip: 'Void this held order',
                              icon: const Icon(Icons.delete_outline),
                              onPressed: _busy ? null : () => _void(o),
                            ),
                          ],
                        ),
                        onTap: () => _recall(o),
                      );
                    },
                  ),
                ),
    );
  }
}

/// Asks why the hold is being dropped; pops with the reason, or null.
class _VoidHeldDialog extends StatefulWidget {
  final HeldOrderSummary order;

  const _VoidHeldDialog({required this.order});

  @override
  State<_VoidHeldDialog> createState() => _VoidHeldDialogState();
}

class _VoidHeldDialogState extends State<_VoidHeldDialog> {
  final _reason = TextEditingController();

  @override
  void dispose() {
    _reason.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final reason = _reason.text.trim();
    return AlertDialog(
      title: Text('Void held order ${widget.order.orderNumber}?'),
      content: SizedBox(
        width: 360,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text("${widget.order.lineCount} item(s) · ₹${widget.order.grandTotal}. The items go back on sale; it can't be recalled afterwards."),
            const SizedBox(height: 12),
            TextField(
              key: const Key('void-held-reason'),
              controller: _reason,
              autofocus: true,
              decoration: const InputDecoration(labelText: 'Reason', hintText: 'e.g. Customer left', border: OutlineInputBorder()),
              onChanged: (_) => setState(() {}),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
        FilledButton(
          key: const Key('void-held-confirm'),
          style: FilledButton.styleFrom(backgroundColor: Theme.of(context).colorScheme.error),
          onPressed: reason.isEmpty ? null : () => Navigator.pop(context, reason),
          child: const Text('Void'),
        ),
      ],
    );
  }
}
