import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../api/api_client.dart';
import '../state/session.dart';
import '../ui/approval_pin_pad.dart';
import '../ui/format.dart';
import '../ui/open_price_dialog.dart';
import '../ui/theme.dart';
import 'held_orders_screen.dart';
import 'orders_screen.dart';
import 'printer_settings_screen.dart';
import 'product_search_screen.dart';
import 'receipt_screen.dart';
import 'shift_screen.dart';
import 'sync_queue_screen.dart';

part 'pos/cart_panel.dart';
part 'pos/dialogs.dart';
part 'pos/payment_sheet.dart';
part 'pos/product_grid.dart';
part 'pos/status_bar.dart';

/// The billing screen (Phase 12 redesign, design system §06): what a
/// cashier stares at all day, so it's one continuous scan → cart → pay flow.
///
/// - The scan field always has focus. Most counter scanners are
///   keyboard-wedge devices (they type the barcode + Enter into whatever has
///   focus), so a plain text field is the right input for real hardware;
///   typed letters also search the product grid.
/// - Every common action has a function key (F2 scan, F4 customer,
///   F7 discount, F8 hold, F9 recall, F12 pay, + − Del on the selected
///   line), so a normal sale needs no pointer at all.
/// - Layout follows the width: two panes from 1000 px (tablet landscape,
///   POS terminals, desktop), stacked panes from 640 px (tablet portrait),
///   and a single column with a cart bar below that (phones).
///
/// A camera-based scanner for phones without a scanner gun is still open.
class PosScreen extends StatefulWidget {
  const PosScreen({super.key});

  @override
  State<PosScreen> createState() => _PosScreenState();
}

class _PosScreenState extends State<PosScreen> {
  final _scanController = TextEditingController();
  final _scanFocus = FocusNode(debugLabel: 'scan');
  bool _busy = false;
  String? _selectedLineId;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) async {
      final session = context.read<AppSession>();
      session.refreshPendingSyncCount();
      // Phase 13: when the merchant requires shifts and this till has
      // none open, start on the shift screen instead of a till that
      // can't take payment.
      await session.refreshShift();
      session.refreshPermissions();
      if (mounted && session.shiftsRequired && session.currentShift == null && !session.offlineMode) {
        await _openShift();
      }
    });
  }

  @override
  void dispose() {
    _scanController.dispose();
    _scanFocus.dispose();
    super.dispose();
  }

  // ------------------------------------------------------------- helpers

  void _snack(String text) {
    if (!mounted) return;
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(text)));
  }

  Future<T?> _withBusy<T>(Future<T> Function() fn) async {
    setState(() => _busy = true);
    try {
      return await fn();
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _refocusScan() {
    if (mounted) _scanFocus.requestFocus();
  }

  /// Dialogs and sheets are routes above this screen; hand them the
  /// session explicitly so the screen works wherever its provider sits.
  Widget _scoped(Widget child) => ChangeNotifierProvider<AppSession>.value(value: context.read<AppSession>(), child: child);

  bool get _liveCart {
    final s = context.read<AppSession>();
    final o = s.currentOrder;
    return o != null && o.status == 'cart' && !s.isCurrentOrderLocal && !s.offlineMode;
  }

  OrderLine? get _selectedLine {
    final lines = context.read<AppSession>().currentOrder?.lines ?? const <OrderLine>[];
    for (final l in lines) {
      if (l.lineId == _selectedLineId) return l;
    }
    return null;
  }

  // -------------------------------------------------------------- actions

  Future<void> _onScanSubmitted(String code) async {
    final trimmed = code.trim();
    if (trimmed.isEmpty || _busy) return;
    final session = context.read<AppSession>();
    await _withBusy(() async {
      final product = await session.scanBarcode(trimmed);
      if (product != null && mounted) {
        if (await addProductToCart(context, session, product, 1)) {
          _selectLast(session);
          _scanController.clear();
        }
      }
    });
    _refocusScan();
    if (session.lastError != null) _snack(session.lastError!);
  }

  Future<void> _addFromGrid(GridProduct item) async {
    final session = context.read<AppSession>();
    final ok = await _withBusy(() => addProductToCart(context, session, item.lookup, 1));
    if (ok == true) {
      _selectLast(session);
    } else if (session.lastError != null) {
      _snack(session.lastError!);
    }
    _refocusScan();
  }

  void _selectLast(AppSession session) {
    final lines = session.currentOrder?.lines ?? const <OrderLine>[];
    if (lines.isNotEmpty) setState(() => _selectedLineId = lines.last.lineId);
  }

  Future<void> _stepLine(OrderLine line, double delta) async {
    if (!_liveCart) {
      _snack(_offlineReason);
      return;
    }
    final next = (double.tryParse(line.quantity) ?? 0) + delta;
    if (next < 1) return;
    final session = context.read<AppSession>();
    final ok = await _withBusy(() => session.updateLineQuantity(line.lineId, next));
    if (ok != true && session.lastError != null) _snack(session.lastError!);
    _refocusScan();
  }

  Future<void> _removeLine(OrderLine line) async {
    final session = context.read<AppSession>();
    final ok = await _withBusy(() => session.removeLine(line.lineId));
    if (ok == true && _selectedLineId == line.lineId) setState(() => _selectedLineId = null);
    if (ok != true && session.lastError != null) _snack(session.lastError!);
    _refocusScan();
  }

  Future<void> _editQuantity(OrderLine line) async {
    final value = await _promptText(context, title: line.productName, label: 'Quantity', confirm: 'Update');
    final qty = double.tryParse(value ?? '');
    if (qty == null || qty <= 0 || !mounted) return;
    final session = context.read<AppSession>();
    final ok = await _withBusy(() => session.updateLineQuantity(line.lineId, qty));
    if (ok != true && session.lastError != null) _snack(session.lastError!);
    _refocusScan();
  }

  Future<void> _pickCustomer() async {
    if (!_liveCart) return;
    final session = context.read<AppSession>();
    final result = await showDialog<_CustomerPickResult>(context: context, builder: (_) => _scoped(const _CustomerPickerDialog()));
    if (result == null) return _refocusScan();
    final ok = await _withBusy(() => result.existing != null
        ? session.attachCustomer(customerId: result.existing!.customerId, name: result.existing!.name)
        : session.attachCustomer(name: result.walkInName, phone: result.walkInPhone));
    if (ok != true && session.lastError != null) _snack(session.lastError!);
    _refocusScan();
  }

  Future<void> _applyDiscount() async {
    if (!_liveCart) return;
    final session = context.read<AppSession>();
    final input = await showDialog<_DiscountInput>(context: context, builder: (_) => _scoped(const _DiscountDialog()));
    if (input == null) return _refocusScan();
    final ok = await _withBusy(() => session.applyManualDiscount(
          valuePercent: input.percent,
          authorizedByCode: input.approverCode,
          authorizedPin: input.approverPin,
          reason: input.reason,
        ));
    _snack(ok == true
        ? 'Discount applied'
        : (session.lastError?.contains('authorizer') == true
            ? 'The approver’s code or PIN doesn’t allow a discount this size'
            : session.lastError ?? 'Could not apply the discount'));
    _refocusScan();
  }

  Future<void> _holdOrder() async {
    if (!_liveCart) return;
    final session = context.read<AppSession>();
    final note = await _promptText(context, title: 'Hold this cart', label: 'Note (optional)', confirm: 'Hold');
    if (note == null) return _refocusScan();
    final ok = await _withBusy(() => session.holdCurrentOrder(note: note.isEmpty ? null : note));
    if (ok == true) setState(() => _selectedLineId = null);
    _snack(ok == true ? 'Cart held — recall it with F9' : (session.lastError ?? 'Could not hold the cart'));
    _refocusScan();
  }

  Future<void> _openHeldOrders() async {
    if (context.read<AppSession>().offlineMode) {
      _snack('Held orders need a connection');
      return;
    }
    final unavailable = await Navigator.of(context).push<List<UnavailableLine>>(MaterialPageRoute(builder: (_) => const HeldOrdersScreen()));
    if (unavailable == null || !mounted) return _refocusScan();
    if (unavailable.isEmpty) {
      _snack('Cart recalled');
    } else {
      await showDialog<void>(
        context: context,
        builder: (ctx) => AlertDialog(
          title: const Text('Cart recalled — some items need attention'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text('Stock could not be held for:'),
              const SizedBox(height: 8),
              for (final line in unavailable) Text('• ${line.reason}'),
              const SizedBox(height: 8),
              const Text('Remove or adjust these lines before checkout.'),
            ],
          ),
          actions: [FilledButton(onPressed: () => Navigator.pop(ctx), child: const Text('OK'))],
        ),
      );
    }
    _refocusScan();
  }

  Future<void> _pay() async {
    final session = context.read<AppSession>();
    final order = session.currentOrder;
    if (order == null || order.status != 'cart' || order.lines.isEmpty || _busy) return;

    final wide = MediaQuery.sizeOf(context).width >= 640;
    final payments = wide
        ? await showDialog<List<PaymentInput>>(
            context: context,
            builder: (_) => _scoped(Dialog(
              insetPadding: const EdgeInsets.all(24),
              child: ConstrainedBox(
                constraints: const BoxConstraints(maxWidth: 560),
                child: const SingleChildScrollView(padding: EdgeInsets.all(24), child: _PaymentSheet()),
              ),
            )),
          )
        : await showModalBottomSheet<List<PaymentInput>>(
            context: context,
            isScrollControlled: true,
            useSafeArea: true,
            showDragHandle: true,
            builder: (ctx) => _scoped(SingleChildScrollView(
              padding: EdgeInsets.fromLTRB(16, 0, 16, 16 + MediaQuery.viewInsetsOf(ctx).bottom),
              child: const _PaymentSheet(),
            )),
          );
    if (payments == null || payments.isEmpty) return _refocusScan();

    final ok = await _withBusy(() => session.checkout(payments));
    if (!mounted) return;
    if (ok != true) {
      if (session.lastError != null) _snack(session.lastError!);
      return _refocusScan();
    }
    final receipt = await session.fetchReceipt();
    if (!mounted) return;
    if (receipt != null) {
      await Navigator.of(context).push(MaterialPageRoute(builder: (_) => ReceiptScreen(receipt: receipt)));
    } else {
      final o = session.currentOrder!;
      await showDialog<void>(
        context: context,
        builder: (ctx) => AlertDialog(
          title: const Text('Sale complete'),
          content: Text('Receipt ${o.orderNumber}\nTotal ${formatRupees(o.grandTotal)}'
              '${session.isCurrentOrderLocal ? '\nSaved offline — it will sync automatically.' : ''}'),
          actions: [FilledButton(onPressed: () => Navigator.pop(ctx), child: const Text('OK'))],
        ),
      );
    }
    session.startNewSale();
    setState(() => _selectedLineId = null);
    _refocusScan();
  }

  Future<void> _sync() async {
    final session = context.read<AppSession>();
    await _withBusy(session.syncNow);
    _snack(session.offlineMode
        ? 'Still offline — sales are safe on this device and will sync when the connection is back'
        : session.pendingSyncCount == 0
            ? 'All sales synced'
            : '${session.pendingSyncCount} sale(s) still waiting to sync');
  }

  /// Phase 13: history works offline too (it then shows this device's
  /// queued sales only); returns inside it need the server.
  Future<void> _openShift() async {
    await Navigator.of(context).push(MaterialPageRoute(builder: (_) => _scoped(const ShiftScreen())));
    _refocusScan();
  }

  Future<void> _openSalesHistory() async {
    await Navigator.of(context).push(MaterialPageRoute(builder: (_) => _scoped(const OrdersScreen())));
    _refocusScan();
  }

  Future<void> _onMenu(_MenuAction action) async {
    final session = context.read<AppSession>();
    switch (action) {
      case _MenuAction.heldOrders:
        await _openHeldOrders();
      case _MenuAction.salesHistory:
        await _openSalesHistory();
      case _MenuAction.productSearch:
        await Navigator.of(context).push(MaterialPageRoute(builder: (_) => const ProductSearchScreen()));
        _refocusScan();
      case _MenuAction.productLookup:
        await Navigator.of(context).push(MaterialPageRoute(builder: (_) => _scoped(const ProductSearchScreen(lookupMode: true))));
        _refocusScan();
      case _MenuAction.syncNow:
        await _sync();
      case _MenuAction.syncQueue:
        await Navigator.of(context).push(MaterialPageRoute(builder: (_) => _scoped(const SyncQueueScreen())));
        _refocusScan();
      case _MenuAction.printer:
        await Navigator.of(context).push(MaterialPageRoute(builder: (_) => const PrinterSettingsScreen()));
        _refocusScan();
      case _MenuAction.signOut:
        final o = session.currentOrder;
        if (o != null && o.status == 'cart' && o.lines.isNotEmpty) {
          final ok = await showDialog<bool>(
            context: context,
            builder: (ctx) => AlertDialog(
              title: const Text('Sign out with items in the cart?'),
              content: const Text('Hold the cart first (F8) if you want to come back to it.'),
              actions: [
                TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('Cancel')),
                FilledButton(onPressed: () => Navigator.pop(ctx, true), child: const Text('Sign out')),
              ],
            ),
          );
          if (ok != true) return;
        }
        session.logout();
    }
  }

  // ------------------------------------------------------------ shortcuts

  Map<ShortcutActivator, VoidCallback> get _shortcuts => {
        const SingleActivator(LogicalKeyboardKey.f2): () {
          _scanController.selection = TextSelection(baseOffset: 0, extentOffset: _scanController.text.length);
          _scanFocus.requestFocus();
        },
        const SingleActivator(LogicalKeyboardKey.f4): _pickCustomer,
        const SingleActivator(LogicalKeyboardKey.f7): _applyDiscount,
        const SingleActivator(LogicalKeyboardKey.f8): _holdOrder,
        const SingleActivator(LogicalKeyboardKey.f6): _openSalesHistory,
        const SingleActivator(LogicalKeyboardKey.f9): _openHeldOrders,
        const SingleActivator(LogicalKeyboardKey.f12): _pay,
        const SingleActivator(LogicalKeyboardKey.numpadAdd): () => _stepSelected(1),
        const SingleActivator(LogicalKeyboardKey.numpadSubtract): () => _stepSelected(-1),
        // Plain +/−/Delete only reach here when the scan field is empty —
        // a non-empty field keeps them as text (see _keyFilter).
        const SingleActivator(LogicalKeyboardKey.equal, shift: true): () => _stepSelected(1),
        const SingleActivator(LogicalKeyboardKey.minus): () => _stepSelected(-1),
        const SingleActivator(LogicalKeyboardKey.delete): () {
          final l = _selectedLine;
          if (l != null) _removeLine(l);
        },
        const SingleActivator(LogicalKeyboardKey.escape): () => setState(() => _selectedLineId = null),
      };

  void _stepSelected(double delta) {
    final l = _selectedLine;
    if (l != null) _stepLine(l, delta);
  }

  /// While the cashier is typing a search into the scan field, + − Delete
  /// are text editing, not cart commands; once the field is empty they act
  /// on the selected cart line. Function keys always work.
  KeyEventResult _keyFilter(FocusNode node, KeyEvent event) {
    if (event is! KeyDownEvent) return KeyEventResult.ignored;
    final k = event.logicalKey;
    final editingKey = k == LogicalKeyboardKey.minus || k == LogicalKeyboardKey.equal || k == LogicalKeyboardKey.delete;
    if (editingKey && _scanFocus.hasFocus && _scanController.text.isNotEmpty) return KeyEventResult.skipRemainingHandlers;
    return KeyEventResult.ignored;
  }

  // --------------------------------------------------------------- layout

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    final theme = Theme.of(context);

    final productPane = _ProductPane(
      scanController: _scanController,
      scanFocus: _scanFocus,
      busy: _busy,
      onScanSubmitted: _onScanSubmitted,
      onAdd: _addFromGrid,
    );
    Widget cartPanel() => _CartPanel(
          busy: _busy,
          selectedLineId: _selectedLineId,
          onSelect: (id) => setState(() => _selectedLineId = id),
          onStep: _stepLine,
          onRemove: _removeLine,
          onEditQuantity: _editQuantity,
          onCustomer: _pickCustomer,
          onHold: _holdOrder,
          onDiscount: _applyDiscount,
          onPay: _pay,
        );

    return Focus(
      onKeyEvent: _keyFilter,
      child: CallbackShortcuts(
        bindings: _shortcuts,
        child: Scaffold(
          appBar: _StatusBar(busy: _busy, onSync: _sync, onShift: _openShift, onMenu: _onMenu),
          body: Column(
            children: [
              if (session.offlineMode)
                Container(
                  key: const Key('offline-banner'),
                  width: double.infinity,
                  color: theme.pos.warnSoft,
                  padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                  child: Text(
                    'Offline — cash, card and UPI sales continue and sync later. Discounts, customers, coupons and credit need a connection.',
                    style: TextStyle(color: theme.pos.warn),
                  ),
                ),
              Expanded(
                child: LayoutBuilder(builder: (context, constraints) {
                  final w = constraints.maxWidth;
                  if (w >= 1000) {
                    return Row(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
                      Expanded(child: productPane),
                      SizedBox(width: w >= 1280 ? 420 : 380, child: cartPanel()),
                    ]);
                  }
                  if (w >= 640) {
                    return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
                      Expanded(flex: 5, child: productPane),
                      Expanded(flex: 6, child: cartPanel()),
                    ]);
                  }
                  return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
                    Expanded(child: productPane),
                    _PhoneCartBar(
                      onOpen: () => Navigator.of(context).push(MaterialPageRoute(
                        builder: (_) => _scoped(Scaffold(appBar: AppBar(title: const Text('Cart')), body: SafeArea(child: cartPanel()))),
                      )),
                    ),
                  ]);
                }),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// Phone layout's bottom bar (design system §06, phone mock): item count
/// and total, opening the full cart and payment.
class _PhoneCartBar extends StatelessWidget {
  final VoidCallback onOpen;

  const _PhoneCartBar({required this.onOpen});

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    final pos = Theme.of(context).pos;
    final order = session.currentOrder;
    final lines = order?.lines ?? const <OrderLine>[];
    final count = lines.fold<double>(0, (s, l) => s + (double.tryParse(l.quantity) ?? 0));
    return SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(12, 8, 12, 12),
        child: FilledButton(
          key: const Key('phone-cart-bar'),
          style: FilledButton.styleFrom(backgroundColor: pos.pay, foregroundColor: pos.onPay, minimumSize: const Size.fromHeight(60)),
          onPressed: lines.isEmpty ? null : onOpen,
          child: Row(children: [
            Expanded(
              child: Text(lines.isEmpty
                  ? 'Cart is empty'
                  : '${formatQty(count.toString())} item${count == 1 ? '' : 's'} · ${formatRupees(order!.grandTotal)}'),
            ),
            const Text('Review & pay'),
            const Icon(Icons.chevron_right),
          ]),
        ),
      ),
    );
  }
}
