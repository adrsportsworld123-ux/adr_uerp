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
                        trailing: Text('₹${o.grandTotal}'),
                        onTap: () => _recall(o),
                      );
                    },
                  ),
                ),
    );
  }
}
