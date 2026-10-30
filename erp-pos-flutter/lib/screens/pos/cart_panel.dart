part of '../pos_screen.dart';

// ---------------------------------------------------------------------
// Cart panel (design system §06, pins 3 and 5): quantity steppers instead
// of a dialog for ±1, a selected line that the keyboard acts on (+ − Del),
// totals, and the one big Pay button showing the amount. Live-only actions
// (discount, customer, quantity change) stay visible but disabled with the
// reason while offline, so cashiers learn the rule instead of hunting for
// a missing button.
// ---------------------------------------------------------------------

String get _offlineReason => tr('Needs a connection — not available offline');

class _CartPanel extends StatelessWidget {
  final bool busy;
  final String? selectedLineId;
  final ValueChanged<String> onSelect;
  final void Function(OrderLine line, double delta) onStep;
  final ValueChanged<OrderLine> onRemove;
  final ValueChanged<OrderLine> onEditQuantity;
  final VoidCallback onCustomer;
  final VoidCallback onHold;
  final VoidCallback onDiscount;
  final VoidCallback onPay;

  const _CartPanel({
    required this.busy,
    required this.selectedLineId,
    required this.onSelect,
    required this.onStep,
    required this.onRemove,
    required this.onEditQuantity,
    required this.onCustomer,
    required this.onHold,
    required this.onDiscount,
    required this.onPay,
  });

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    final theme = Theme.of(context);
    final order = session.currentOrder;
    final lines = order?.lines ?? const <OrderLine>[];
    final isCart = order != null && order.status == 'cart';
    final live = isCart && !session.isCurrentOrderLocal && !session.offlineMode;
    final itemCount = lines.fold<double>(0, (sum, l) => sum + (double.tryParse(l.quantity) ?? 0));

    return Container(
      decoration: BoxDecoration(color: theme.colorScheme.surface, border: Border(left: BorderSide(color: theme.colorScheme.outline))),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
            child: Row(
              children: [
                Expanded(child: _CustomerChip(enabled: !busy && live, onTap: onCustomer)),
                const SizedBox(width: 8),
                if (lines.isNotEmpty)
                  Chip(
                    label: Text(itemCount == 1 ? tr('1 item') : tr('{0} items', [formatQty(itemCount.toString())])),
                    visualDensity: VisualDensity.compact,
                  ),
              ],
            ),
          ),
          const Divider(height: 1),
          Expanded(
            child: lines.isEmpty
                ? _EmptyCart(offline: session.offlineMode)
                : ListView.separated(
                    itemCount: lines.length,
                    separatorBuilder: (_, __) => const Divider(height: 1),
                    itemBuilder: (context, i) => _CartLine(
                      line: lines[i],
                      selected: lines[i].lineId == selectedLineId,
                      editable: isCart && !busy,
                      quantityEditable: live && !busy,
                      onSelect: () => onSelect(lines[i].lineId),
                      onStep: (d) => onStep(lines[i], d),
                      onRemove: () => onRemove(lines[i]),
                      onEditQuantity: () => onEditQuantity(lines[i]),
                    ),
                  ),
          ),
          if (order != null) _Totals(order: order),
          SafeArea(
            top: false,
            child: Padding(
              padding: const EdgeInsets.fromLTRB(16, 4, 16, 16),
              child: Row(
                children: [
                  Expanded(
                    flex: 2,
                    child: _ActionButton(
                      icon: Icons.pause_circle_outline,
                      label: tr('Hold'),
                      keyHint: 'F8',
                      tooltip: live ? tr('Park this cart and serve someone else') : _offlineReason,
                      onPressed: live && lines.isNotEmpty && !busy ? onHold : null,
                    ),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    flex: 2,
                    child: _ActionButton(
                      icon: Icons.percent,
                      label: tr('Discount'),
                      keyHint: 'F7',
                      tooltip: live ? tr('Manual discount') : _offlineReason,
                      onPressed: live && lines.isNotEmpty && !busy ? onDiscount : null,
                    ),
                  ),
                  const SizedBox(width: 8),
                  Expanded(flex: 5, child: _PayButton(order: order, onPressed: isCart && lines.isNotEmpty && !busy ? onPay : null)),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _CustomerChip extends StatelessWidget {
  final bool enabled;
  final VoidCallback onTap;

  const _CustomerChip({required this.enabled, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    final theme = Theme.of(context);
    final name = session.attachedCustomerName;
    return Tooltip(
      message: enabled ? tr('Attach a customer (F4)') : (session.currentOrder == null ? tr('Add an item first') : _offlineReason),
      child: OutlinedButton(
        style: OutlinedButton.styleFrom(
          minimumSize: const Size(0, 44),
          alignment: Alignment.centerLeft,
          padding: const EdgeInsets.symmetric(horizontal: 12),
        ),
        onPressed: enabled ? onTap : null,
        child: Row(
          children: [
            Icon(name == null ? Icons.person_add_alt : Icons.person, size: 18),
            const SizedBox(width: 8),
            Expanded(
              // 'Customer' alone (no prefix) when none is attached — the
              // widget test and screen readers both rely on that label.
              child: Text(name ?? tr('Customer'), maxLines: 1, overflow: TextOverflow.ellipsis),
            ),
            if (name != null && session.loyaltyAvailablePoints != null)
              Text(tr('{0} pts', [session.loyaltyAvailablePoints]), style: theme.textTheme.bodySmall),
            const SizedBox(width: 6),
            const _KeyHint('F4'),
          ],
        ),
      ),
    );
  }
}

class _EmptyCart extends StatelessWidget {
  final bool offline;

  const _EmptyCart({required this.offline});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          Icon(Icons.shopping_cart_outlined, size: 40, color: theme.colorScheme.primary),
          const SizedBox(height: 8),
          Text(tr('Cart is empty — scan an item to begin'), style: theme.textTheme.titleSmall, textAlign: TextAlign.center),
          const SizedBox(height: 6),
          Text(
            offline ? tr('Offline: cash sales continue and sync later.') : tr('F2 scan · F4 customer · F7 discount · F8 hold · F9 recall · F12 pay'),
            style: theme.textTheme.bodySmall,
            textAlign: TextAlign.center,
          ),
        ]),
      ),
    );
  }
}

class _CartLine extends StatelessWidget {
  final OrderLine line;
  final bool selected;
  final bool editable;
  final bool quantityEditable;
  final VoidCallback onSelect;
  final ValueChanged<double> onStep;
  final VoidCallback onRemove;
  final VoidCallback onEditQuantity;

  const _CartLine({
    required this.line,
    required this.selected,
    required this.editable,
    required this.quantityEditable,
    required this.onSelect,
    required this.onStep,
    required this.onRemove,
    required this.onEditQuantity,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final qty = double.tryParse(line.quantity) ?? 0;
    return Material(
      color: selected ? theme.colorScheme.primaryContainer.withValues(alpha: 0.45) : Colors.transparent,
      child: InkWell(
        onTap: onSelect,
        onLongPress: quantityEditable ? onEditQuantity : null,
        child: Container(
          decoration: BoxDecoration(border: Border(left: BorderSide(color: selected ? theme.colorScheme.primary : Colors.transparent, width: 3))),
          padding: const EdgeInsets.fromLTRB(13, 10, 8, 10),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Expanded(child: Text(line.productName, style: theme.textTheme.titleSmall)),
                  Text(formatRupees(line.lineTotal),
                      style: theme.textTheme.titleSmall?.copyWith(fontWeight: FontWeight.w700, fontFeatures: const [FontFeature.tabularFigures()])),
                ],
              ),
              const SizedBox(height: 4),
              Row(
                children: [
                  Expanded(
                    child: Text(
                        line.openPrice
                            ? tr('{0} · qty {1} · open price {2}', [line.sku, formatQty(line.quantity) + unitSuffix(line.unitSymbol), formatRupees(line.unitPrice)])
                            : tr('{0} · qty {1}', [line.sku, formatQty(line.quantity) + unitSuffix(line.unitSymbol)]),
                        style: theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.onSurfaceVariant)),
                  ),
                  _Stepper(
                    qty: qty,
                    enabled: quantityEditable,
                    onMinus: qty > 1 ? () => onStep(-1) : null,
                    onPlus: () => onStep(1),
                  ),
                  IconButton(
                    tooltip: tr('Remove'),
                    icon: const Icon(Icons.close, size: 18),
                    onPressed: editable ? onRemove : null,
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _Stepper extends StatelessWidget {
  final double qty;
  final bool enabled;
  final VoidCallback? onMinus;
  final VoidCallback onPlus;

  const _Stepper({required this.qty, required this.enabled, required this.onMinus, required this.onPlus});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Tooltip(
      message: enabled ? tr('Change quantity (+ / −)') : _offlineReason,
      child: Container(
        decoration: BoxDecoration(border: Border.all(color: theme.colorScheme.outline), borderRadius: BorderRadius.circular(10)),
        child: Row(mainAxisSize: MainAxisSize.min, children: [
          IconButton(
            visualDensity: VisualDensity.compact,
            tooltip: tr('Decrease quantity'),
            icon: const Icon(Icons.remove, size: 18),
            onPressed: enabled ? onMinus : null,
          ),
          SizedBox(
            width: 28,
            child: Text(formatQty(qty.toString()),
                textAlign: TextAlign.center, style: const TextStyle(fontWeight: FontWeight.w700, fontFeatures: [FontFeature.tabularFigures()])),
          ),
          IconButton(
            visualDensity: VisualDensity.compact,
            tooltip: tr('Increase quantity'),
            icon: const Icon(Icons.add, size: 18),
            onPressed: enabled ? onPlus : null,
          ),
        ]),
      ),
    );
  }
}

class _Totals extends StatelessWidget {
  final OrderSummary order;

  const _Totals({required this.order});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final hasDiscount = (double.tryParse(order.discountTotal) ?? 0) > 0;
    final muted = theme.textTheme.bodyMedium?.copyWith(color: theme.colorScheme.onSurfaceVariant, fontFeatures: const [FontFeature.tabularFigures()]);
    Widget row(String label, String value, {TextStyle? style, Color? valueColor}) => Padding(
          padding: const EdgeInsets.symmetric(vertical: 2),
          child: Row(children: [
            Expanded(child: Text(label, style: style ?? muted)),
            Text(value, style: (style ?? muted)?.copyWith(color: valueColor)),
          ]),
        );
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
      decoration: BoxDecoration(border: Border(top: BorderSide(color: theme.colorScheme.outline))),
      child: Column(children: [
        row(tr('Subtotal'), formatRupees(order.subtotal)),
        if (hasDiscount) row(tr('Discount'), '−${formatRupees(order.discountTotal)}', valueColor: theme.pos.pay),
        for (final t in context.watch<AppSession>().taxRowsFor(order)) row(t.label, formatRupees(t.amount)),
        const SizedBox(height: 4),
        row(tr('Total'), formatRupees(order.grandTotal),
            style: theme.textTheme.headlineSmall?.copyWith(fontWeight: FontWeight.w700, fontFeatures: const [FontFeature.tabularFigures()])),
        if (order.status != 'cart')
          Padding(
            padding: const EdgeInsets.only(top: 4),
            child: Text(tr('Order {0}', [order.status]), style: theme.textTheme.bodySmall),
          ),
      ]),
    );
  }
}

class _ActionButton extends StatelessWidget {
  final IconData icon;
  final String label;
  final String keyHint;
  final String tooltip;
  final VoidCallback? onPressed;

  const _ActionButton({required this.icon, required this.label, required this.keyHint, required this.tooltip, required this.onPressed});

  @override
  Widget build(BuildContext context) {
    return Tooltip(
      message: tooltip,
      child: OutlinedButton(
        style: OutlinedButton.styleFrom(minimumSize: const Size(0, 64), padding: const EdgeInsets.symmetric(horizontal: 6)),
        onPressed: onPressed,
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          Icon(icon, size: 20),
          const SizedBox(height: 2),
          Text(label),
          _KeyHint(keyHint),
        ]),
      ),
    );
  }
}

class _PayButton extends StatelessWidget {
  final OrderSummary? order;
  final VoidCallback? onPressed;

  const _PayButton({required this.order, required this.onPressed});

  @override
  Widget build(BuildContext context) {
    final pos = Theme.of(context).pos;
    final total = order == null ? '' : ' ${formatRupees(order!.grandTotal)}';
    return FilledButton(
      key: const Key('pay-button'),
      style: FilledButton.styleFrom(
        backgroundColor: pos.pay,
        foregroundColor: pos.onPay,
        minimumSize: const Size(0, 64),
        textStyle: const TextStyle(fontFamily: 'Geist', fontSize: 18, fontWeight: FontWeight.w700),
      ),
      onPressed: onPressed,
      child: FittedBox(
        child: Row(mainAxisSize: MainAxisSize.min, children: [
          Text(tr('Pay{0}', [total])),
          const SizedBox(width: 8),
          _KeyHint('F12', color: pos.onPay),
        ]),
      ),
    );
  }
}
