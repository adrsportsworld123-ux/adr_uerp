import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/api_client.dart';
import '../state/session.dart';
import 'printer_settings_screen.dart';
import 'receipt_screen.dart';

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
  final _couponController = TextEditingController();
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      context.read<AppSession>().refreshPendingSyncCount();
    });
  }

  @override
  void dispose() {
    _barcodeController.dispose();
    _barcodeFocusNode.dispose();
    _couponController.dispose();
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

  Future<void> _editQuantity(OrderLine line) async {
    final session = context.read<AppSession>();
    final controller = TextEditingController(text: line.quantity);
    final newQty = await showDialog<double>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(line.productName),
        content: TextField(
          controller: controller,
          autofocus: true,
          keyboardType: const TextInputType.numberWithOptions(decimal: true),
          decoration: const InputDecoration(labelText: 'Quantity', border: OutlineInputBorder()),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, double.tryParse(controller.text)),
            child: const Text('Update'),
          ),
        ],
      ),
    );
    if (newQty == null || newQty <= 0) return;
    final ok = await session.updateLineQuantity(line.lineId, newQty);
    if (!mounted) return;
    if (!ok && session.lastError != null) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(session.lastError!)));
    }
  }

  Future<void> _pickCustomer() async {
    final session = context.read<AppSession>();
    final result = await showDialog<_CustomerPickResult>(
      context: context,
      builder: (ctx) => const _CustomerPickerDialog(),
    );
    if (result == null) return;

    final ok = result.existing != null
        ? await session.attachCustomer(customerId: result.existing!.customerId, name: result.existing!.name)
        : await session.attachCustomer(name: result.walkInName, phone: result.walkInPhone);
    if (!mounted) return;
    if (!ok && session.lastError != null) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(session.lastError!)));
    }
  }

  Future<void> _applyDiscount() async {
    final session = context.read<AppSession>();
    final result = await showDialog<_DiscountInput>(
      context: context,
      builder: (ctx) => const _DiscountDialog(),
    );
    if (result == null) return;
    final ok = await session.applyManualDiscount(
      valuePercent: result.percent,
      authorizedBy: result.authorizedBy,
      authorizedPin: result.authorizedPin,
      reason: result.reason,
    );
    if (!mounted) return;
    if (session.lastError != null) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(ok ? 'Discount applied' : session.lastError!)));
    }
  }

  Future<void> _applyPromotions() async {
    final session = context.read<AppSession>();
    setState(() => _busy = true);
    final ok = await session.applyPromotions();
    if (!mounted) return;
    setState(() => _busy = false);
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(ok ? 'Eligible promotions applied' : (session.lastError ?? 'Could not apply promotions'))),
    );
  }

  Future<void> _applyCoupon() async {
    final code = _couponController.text.trim();
    if (code.isEmpty) return;
    final session = context.read<AppSession>();
    setState(() => _busy = true);
    final ok = await session.applyCoupon(code);
    if (!mounted) return;
    setState(() => _busy = false);
    if (ok) _couponController.clear();
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(ok ? 'Coupon applied' : (session.lastError ?? 'Could not apply coupon'))),
    );
  }

  Future<void> _redeemLoyalty() async {
    final session = context.read<AppSession>();
    final controller = TextEditingController();
    final points = await showDialog<int>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('Redeem points (${session.loyaltyAvailablePoints ?? 0} available)'),
        content: TextField(
          controller: controller,
          autofocus: true,
          keyboardType: TextInputType.number,
          decoration: const InputDecoration(labelText: 'Points to redeem', border: OutlineInputBorder()),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
          FilledButton(onPressed: () => Navigator.pop(ctx, int.tryParse(controller.text)), child: const Text('Redeem')),
        ],
      ),
    );
    if (points == null || points <= 0) return;
    final ok = await session.redeemLoyaltyPoints(points);
    if (!mounted) return;
    if (session.lastError != null || ok) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(ok ? 'Points redeemed' : session.lastError!)));
    }
  }

  Future<void> _checkout() async {
    final session = context.read<AppSession>();
    final order = session.currentOrder;
    if (order == null || order.status != 'cart') return;

    final input = await showDialog<_CheckoutInput>(
      context: context,
      builder: (ctx) => _CheckoutDialog(grandTotal: order.grandTotal, customerAttached: session.attachedCustomerId != null),
    );
    if (input == null) return;

    setState(() => _busy = true);
    final grandTotal = double.tryParse(order.grandTotal) ?? 0;
    final ok = await session.checkout([
      PaymentInput(method: input.method, amount: grandTotal, reference: input.reference),
    ]);
    if (!mounted) return;
    setState(() => _busy = false);

    if (ok) {
      final receipt = await session.fetchReceipt();
      if (!mounted) return;
      if (receipt != null) {
        await Navigator.of(context).push(MaterialPageRoute(builder: (_) => ReceiptScreen(receipt: receipt)));
      } else {
        await showDialog<void>(
          context: context,
          builder: (ctx) => AlertDialog(
            title: const Text('Sale complete'),
            content: Text('Receipt ${session.currentOrder!.orderNumber}\nTotal ₹${session.currentOrder!.grandTotal}'),
            actions: [
              FilledButton(onPressed: () => Navigator.pop(ctx), child: const Text('OK')),
            ],
          ),
        );
      }
      session.startNewSale();
    } else if (session.lastError != null) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(session.lastError!)));
    }
  }

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    final order = session.currentOrder;
    final cartActive = order != null && order.status == 'cart' && order.lines.isNotEmpty && !session.isCurrentOrderLocal && !session.offlineMode;

    return Scaffold(
      appBar: AppBar(
        title: const Text('New Sale'),
        actions: [
          TextButton.icon(
            onPressed: _busy || session.isCurrentOrderLocal || session.offlineMode || order == null ? null : _pickCustomer,
            icon: const Icon(Icons.person, color: Colors.white),
            label: Text(
              session.attachedCustomerName ?? 'Customer',
              style: const TextStyle(color: Colors.white),
            ),
          ),
          IconButton(
            icon: const Icon(Icons.settings_ethernet),
            tooltip: 'Printer settings',
            onPressed: () => Navigator.of(context).push(MaterialPageRoute(builder: (_) => const PrinterSettingsScreen())),
          ),
          if (session.offlineMode || session.pendingSyncCount > 0)
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 8),
              child: Center(
                child: TextButton.icon(
                  onPressed: _busy
                      ? null
                      : () async {
                          setState(() => _busy = true);
                          await session.syncNow();
                          if (mounted) setState(() => _busy = false);
                        },
                  icon: Icon(
                    session.offlineMode ? Icons.cloud_off : Icons.sync,
                    color: Colors.white,
                  ),
                  label: Text(
                    session.pendingSyncCount > 0
                        ? '${session.pendingSyncCount} pending'
                        : 'Offline',
                    style: const TextStyle(color: Colors.white),
                  ),
                ),
              ),
            ),
        ],
      ),
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
          if (cartActive)
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              child: Wrap(
                spacing: 8,
                runSpacing: 4,
                children: [
                  OutlinedButton(onPressed: _busy ? null : _applyDiscount, child: const Text('Discount')),
                  OutlinedButton(onPressed: _busy ? null : _applyPromotions, child: const Text('Apply promotions')),
                  if (session.attachedCustomerId != null && (session.loyaltyAvailablePoints ?? 0) > 0)
                    OutlinedButton(onPressed: _busy ? null : _redeemLoyalty, child: const Text('Redeem points')),
                  SizedBox(
                    width: 180,
                    child: TextField(
                      controller: _couponController,
                      enabled: !_busy,
                      decoration: InputDecoration(
                        labelText: 'Coupon code',
                        isDense: true,
                        border: const OutlineInputBorder(),
                        suffixIcon: IconButton(icon: const Icon(Icons.check), onPressed: _busy ? null : _applyCoupon),
                      ),
                      onSubmitted: (_) => _applyCoupon(),
                    ),
                  ),
                ],
              ),
            ),
          const SizedBox(height: 8),
          Expanded(
            child: (order == null || order.lines.isEmpty)
                ? const Center(child: Text('Cart is empty — scan an item to begin'))
                : ListView.builder(
                    itemCount: order.lines.length,
                    itemBuilder: (context, i) {
                      final line = order.lines[i];
                      return ListTile(
                        title: Text(line.productName),
                        subtitle: Text('${line.sku} · qty ${line.quantity}'),
                        onTap: _busy || order.status != 'cart' || session.isCurrentOrderLocal ? null : () => _editQuantity(line),
                        trailing: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Text('₹${line.lineTotal}'),
                            IconButton(
                              icon: const Icon(Icons.close, size: 18),
                              tooltip: 'Remove',
                              onPressed: _busy || order.status != 'cart'
                                  ? null
                                  : () => session.removeLine(line.lineId),
                            ),
                          ],
                        ),
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
    final hasDiscount = double.tryParse(order.discountTotal) != null && double.parse(order.discountTotal) > 0;
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
            if (hasDiscount) _totalRow('Discount', '-${order.discountTotal}'),
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

// ---------------------------------------------------------------------
// Customer picker: search an existing customer, or register a walk-in
// inline — both paths call AppSession.attachCustomer, mirroring
// internal/sales/customer.go's own {customer_id} vs {name,phone,email}
// duality.
// ---------------------------------------------------------------------

class _CustomerPickResult {
  final Customer? existing;
  final String? walkInName;
  final String? walkInPhone;

  _CustomerPickResult.existing(this.existing) : walkInName = null, walkInPhone = null;
  _CustomerPickResult.walkIn(this.walkInName, this.walkInPhone) : existing = null;
}

class _CustomerPickerDialog extends StatefulWidget {
  const _CustomerPickerDialog();

  @override
  State<_CustomerPickerDialog> createState() => _CustomerPickerDialogState();
}

class _CustomerPickerDialogState extends State<_CustomerPickerDialog> {
  final _searchController = TextEditingController();
  final _walkInNameController = TextEditingController();
  final _walkInPhoneController = TextEditingController();
  List<Customer> _results = [];
  bool _searching = false;

  Future<void> _search(String query) async {
    if (query.trim().isEmpty) {
      setState(() => _results = []);
      return;
    }
    setState(() => _searching = true);
    try {
      final results = await context.read<AppSession>().searchCustomers(query.trim());
      if (!mounted) return;
      setState(() => _results = results);
    } catch (_) {
      // Best-effort search — leave the previous results on screen.
    } finally {
      if (mounted) setState(() => _searching = false);
    }
  }

  @override
  void dispose() {
    _searchController.dispose();
    _walkInNameController.dispose();
    _walkInPhoneController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Attach a customer'),
      content: SizedBox(
        width: 360,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            TextField(
              controller: _searchController,
              autofocus: true,
              decoration: InputDecoration(
                labelText: 'Search by name/phone/email',
                border: const OutlineInputBorder(),
                suffixIcon: _searching
                    ? const Padding(
                        padding: EdgeInsets.all(12),
                        child: SizedBox(height: 16, width: 16, child: CircularProgressIndicator(strokeWidth: 2)),
                      )
                    : const Icon(Icons.search),
              ),
              onChanged: _search,
            ),
            if (_results.isNotEmpty)
              ConstrainedBox(
                constraints: const BoxConstraints(maxHeight: 180),
                child: ListView(
                  shrinkWrap: true,
                  children: [
                    for (final c in _results)
                      ListTile(
                        dense: true,
                        title: Text(c.name),
                        subtitle: Text(c.phone),
                        trailing: c.creditHold ? const Icon(Icons.block, color: Colors.red, size: 18) : null,
                        onTap: () => Navigator.pop(context, _CustomerPickResult.existing(c)),
                      ),
                  ],
                ),
              ),
            const Divider(height: 24),
            Text('Or register a walk-in', style: Theme.of(context).textTheme.labelMedium),
            const SizedBox(height: 8),
            TextField(
              controller: _walkInNameController,
              decoration: const InputDecoration(labelText: 'Name', border: OutlineInputBorder(), isDense: true),
            ),
            const SizedBox(height: 8),
            TextField(
              controller: _walkInPhoneController,
              keyboardType: TextInputType.phone,
              decoration: const InputDecoration(labelText: 'Phone', border: OutlineInputBorder(), isDense: true),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
        FilledButton(
          onPressed: _walkInNameController.text.trim().isEmpty && _walkInPhoneController.text.trim().isEmpty
              ? null
              : () => Navigator.pop(
                    context,
                    _CustomerPickResult.walkIn(_walkInNameController.text.trim(), _walkInPhoneController.text.trim()),
                  ),
          child: const Text('Add walk-in'),
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------
// Manual discount dialog — mirrors internal/sales/discounts.go's tiers
// (0-5% no approval, 5-15% Branch Manager PIN, 15-25% Merchant Admin
// PIN) as a UX nicety only; the server enforces the real boundaries.
// ---------------------------------------------------------------------

class _DiscountInput {
  final double percent;
  final String authorizedBy;
  final String authorizedPin;
  final String reason;

  _DiscountInput(this.percent, this.authorizedBy, this.authorizedPin, this.reason);
}

class _DiscountDialog extends StatefulWidget {
  const _DiscountDialog();

  @override
  State<_DiscountDialog> createState() => _DiscountDialogState();
}

class _DiscountDialogState extends State<_DiscountDialog> {
  final _percentController = TextEditingController();
  final _reasonController = TextEditingController();
  final _authorizedByController = TextEditingController();
  final _authorizedPinController = TextEditingController();

  double get _percent => double.tryParse(_percentController.text) ?? 0;
  bool get _needsApproval => _percent > 5;

  @override
  void dispose() {
    _percentController.dispose();
    _reasonController.dispose();
    _authorizedByController.dispose();
    _authorizedPinController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return StatefulBuilder(
      builder: (context, setState) => AlertDialog(
        title: const Text('Manual discount'),
        content: SizedBox(
          width: 320,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextField(
                controller: _percentController,
                autofocus: true,
                keyboardType: const TextInputType.numberWithOptions(decimal: true),
                decoration: const InputDecoration(labelText: 'Discount %', border: OutlineInputBorder()),
                onChanged: (_) => setState(() {}),
              ),
              const SizedBox(height: 8),
              TextField(
                controller: _reasonController,
                decoration: const InputDecoration(labelText: 'Reason', border: OutlineInputBorder()),
              ),
              if (_needsApproval) ...[
                const SizedBox(height: 8),
                Text(
                  'Above 5% needs a Branch Manager/Merchant Admin PIN',
                  style: Theme.of(context).textTheme.bodySmall,
                ),
                const SizedBox(height: 8),
                TextField(
                  controller: _authorizedByController,
                  decoration: const InputDecoration(labelText: 'Approver user ID', border: OutlineInputBorder()),
                ),
                const SizedBox(height: 8),
                TextField(
                  controller: _authorizedPinController,
                  obscureText: true,
                  decoration: const InputDecoration(labelText: 'Approver PIN', border: OutlineInputBorder()),
                ),
              ],
            ],
          ),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
          FilledButton(
            onPressed: _percent <= 0 || _reasonController.text.trim().isEmpty
                ? null
                : () => Navigator.pop(
                      context,
                      _DiscountInput(_percent, _authorizedByController.text.trim(), _authorizedPinController.text.trim(),
                          _reasonController.text.trim()),
                    ),
            child: const Text('Apply'),
          ),
        ],
      ),
    );
  }
}

// ---------------------------------------------------------------------
// Checkout dialog — cash/card/upi/credit, an optional gateway reference
// for card/upi, and credit disabled unless a customer is already
// attached (the backend rejects a credit payment with no customer, but
// disabling it here saves a round trip).
// ---------------------------------------------------------------------

class _CheckoutInput {
  final String method;
  final String? reference;

  _CheckoutInput(this.method, this.reference);
}

class _CheckoutDialog extends StatefulWidget {
  final String grandTotal;
  final bool customerAttached;

  const _CheckoutDialog({required this.grandTotal, required this.customerAttached});

  @override
  State<_CheckoutDialog> createState() => _CheckoutDialogState();
}

class _CheckoutDialogState extends State<_CheckoutDialog> {
  String? _method;
  final _referenceController = TextEditingController();

  @override
  void dispose() {
    _referenceController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final needsReference = _method == 'card' || _method == 'upi';
    return AlertDialog(
      title: Text('Charge ₹${widget.grandTotal}'),
      content: RadioGroup<String>(
        groupValue: _method,
        onChanged: (v) {
          if (v == 'credit' && !widget.customerAttached) return;
          setState(() => _method = v);
        },
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            for (final m in const ['cash', 'card', 'upi', 'credit'])
              RadioListTile<String>(
                value: m,
                title: Text(m.toUpperCase()),
                subtitle: m == 'credit' && !widget.customerAttached ? const Text('Attach a customer first') : null,
              ),
            if (needsReference)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: TextField(
                  controller: _referenceController,
                  decoration: const InputDecoration(labelText: 'Gateway reference (optional)', border: OutlineInputBorder()),
                ),
              ),
          ],
        ),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
        FilledButton(
          onPressed: _method == null
              ? null
              : () => Navigator.pop(context, _CheckoutInput(_method!, needsReference ? _referenceController.text.trim() : null)),
          child: const Text('Confirm'),
        ),
      ],
    );
  }
}
