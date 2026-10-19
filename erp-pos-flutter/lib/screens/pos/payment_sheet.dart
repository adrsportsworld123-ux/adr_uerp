part of '../pos_screen.dart';

// ---------------------------------------------------------------------
// Payment sheet (design system §06 "Payment"): replaces the radio-list
// checkout dialog. Coupon / promotions / loyalty sit here, applied right
// before money is taken; the total updates live as they apply. Cash shows
// the change to hand back; split lets the cashier take part of the bill on
// one tender and the rest on another. Returns the payments to record, or
// null if the cashier backs out. The server still validates the total.
//
// Round-off (Sales Policy, erp-core-go migrations/059): when the merchant
// allows it, the cashier can waive a small balance — the paise by default,
// or another amount up to the limit — so ₹105.75 is settled for ₹105. The
// bill keeps its total; the waiver travels with the payments.
// ---------------------------------------------------------------------

/// What the payment sheet hands back: the tenders and the waiver taken
/// (signed, 0 = none).
class _PaymentResult {
  final List<PaymentInput> payments;
  final double roundOff;
  const _PaymentResult(this.payments, {this.roundOff = 0});
}

const _tenders = [
  ('cash', 'Cash', Icons.payments_outlined),
  ('card', 'Card', Icons.credit_card),
  ('upi', 'UPI', Icons.qr_code_2),
  ('credit', 'Credit', Icons.account_balance_wallet_outlined),
];

double _round2(double v) => (v * 100).roundToDouble() / 100;

class _PaymentSheet extends StatefulWidget {
  const _PaymentSheet();

  @override
  State<_PaymentSheet> createState() => _PaymentSheetState();
}

class _PaymentSheetState extends State<_PaymentSheet> {
  String _method = 'cash';
  final _tenderedController = TextEditingController();
  final _referenceController = TextEditingController();
  final _couponController = TextEditingController();
  final _splitAmountController = TextEditingController();
  final List<PaymentInput> _splits = [];
  bool _splitMode = false;
  bool _working = false;
  String? _message;

  /// Round-off: on for this sale, and an amount the cashier typed (null =
  /// the bill's paise).
  bool _roundOn = false;
  double? _customWaive;

  @override
  void initState() {
    super.initState();
    _roundOn = context.read<AppSession>().roundOffAuto;
    WidgetsBinding.instance.addPostFrameCallback((_) => _autoApplyPromotions());
  }

  /// Opening the sheet applies every eligible promotion (best price per
  /// line, internal/promotions/apply.go) before money is taken. The
  /// server call is a re-evaluation, so opening the sheet again never
  /// stacks a second discount. "Complete sale" waits for it (_working).
  /// Says so only when the total dropped; a failure stays quiet — the
  /// "Apply promotions" button is still there to retry.
  Future<void> _autoApplyPromotions() async {
    final session = context.read<AppSession>();
    final order = session.currentOrder;
    if (order == null || order.lines.isEmpty || session.isCurrentOrderLocal || session.offlineMode) return;
    final before = double.tryParse(order.discountTotal) ?? 0;
    setState(() => _working = true);
    final ok = await session.applyPromotions();
    if (!mounted) return;
    final after = double.tryParse(session.currentOrder?.discountTotal ?? '') ?? before;
    setState(() {
      _working = false;
      if (ok && after > before + 0.004) _message = 'Promotions applied: ${formatRupeesNum(after - before)} off';
    });
  }

  @override
  void dispose() {
    _tenderedController.dispose();
    _referenceController.dispose();
    _couponController.dispose();
    _splitAmountController.dispose();
    super.dispose();
  }

  double _total(AppSession s) => double.tryParse(s.currentOrder?.grandTotal ?? '') ?? 0;
  double _paid() => _splits.fold(0, (sum, p) => sum + p.amount);
  double _paise(AppSession s) => _round2(_total(s) - _total(s).floorToDouble());

  /// The most this sale may waive: the merchant's limit, and never the whole bill.
  double _waiveLimit(AppSession s) => _round2(s.roundOffMax < _total(s) - 0.01 ? s.roundOffMax : _total(s) - 0.01);

  /// What this sale waives (0 when round-off is off, or the amount is over the limit).
  double _waive(AppSession s) {
    if (s.roundOffMax <= 0 || !_roundOn) return 0;
    final w = _round2(_customWaive ?? _paise(s));
    return w > 0 && w <= _waiveLimit(s) + 0.004 ? w : 0;
  }

  double _remaining(AppSession s) => _round2(_total(s) - _waive(s) - _paid());

  Future<void> _otherWaive(AppSession s) async {
    final text = await _promptText(context,
        title: 'Round off — amount to waive (up to ${formatRupeesNum(_waiveLimit(s))})', label: 'Amount waived (${PosCurrency.symbol})', confirm: 'Apply');
    if (text == null || !mounted) return;
    final v = double.tryParse(text.trim());
    setState(() {
      if (v == null || v < 0 || v > _waiveLimit(s) + 0.004) {
        _message = 'Enter an amount up to ${formatRupeesNum(_waiveLimit(s))}';
        return;
      }
      _customWaive = _round2(v);
      _roundOn = v > 0;
      _message = null;
    });
  }

  String? _tenderDisabledReason(AppSession s, String method) {
    if (method == 'credit') {
      if (s.offlineMode || s.isCurrentOrderLocal) return 'Credit needs a connection (the limit is checked live)';
      if (s.attachedCustomerId == null) return 'Attach a customer first';
    }
    return null;
  }

  Future<void> _run(Future<bool> Function() action, String okText) async {
    setState(() {
      _working = true;
      _message = null;
    });
    final session = context.read<AppSession>();
    final ok = await action();
    if (!mounted) return;
    setState(() {
      _working = false;
      _message = ok ? okText : (session.lastError ?? 'That didn’t work');
    });
  }

  void _complete(AppSession s) {
    final due = _remaining(s);
    final roundOff = -_waive(s);
    if (_splitMode) {
      if (due > 0.004) return;
      Navigator.pop(context, _PaymentResult(List<PaymentInput>.of(_splits), roundOff: roundOff));
      return;
    }
    if (_method == 'cash') {
      final tendered = double.tryParse(_tenderedController.text) ?? due;
      if (tendered + 0.004 < due) return;
    }
    final ref = _referenceController.text.trim();
    Navigator.pop(
        context,
        _PaymentResult([PaymentInput(method: _method, amount: due, reference: (_method == 'card' || _method == 'upi') && ref.isNotEmpty ? ref : null)],
            roundOff: roundOff));
  }

  void _addSplit(AppSession s) {
    final remaining = _remaining(s);
    final amount = double.tryParse(_splitAmountController.text) ?? remaining;
    if (amount <= 0 || amount > remaining + 0.004) {
      setState(() => _message = 'Enter an amount up to ${formatRupeesNum(remaining)}');
      return;
    }
    final ref = _referenceController.text.trim();
    setState(() {
      _splits.add(PaymentInput(method: _method, amount: _round2(amount), reference: ref.isEmpty ? null : ref));
      _splitAmountController.clear();
      _referenceController.clear();
      _message = null;
    });
  }

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    final theme = Theme.of(context);
    final pos = theme.pos;
    final order = session.currentOrder;
    final live = order != null && !session.isCurrentOrderLocal && !session.offlineMode;
    final due = _remaining(session);
    final tendered = double.tryParse(_tenderedController.text);
    final change = _method == 'cash' && tendered != null ? _round2(tendered - due) : null;
    final canComplete = !_working &&
        (_splitMode
            ? due <= 0.004 && _splits.isNotEmpty
            : _tenderDisabledReason(session, _method) == null && (_method != 'cash' || tendered == null || tendered + 0.004 >= due));

    final quick = <double>{due, ((due / 100).ceil() * 100).toDouble(), ((due / 500).ceil() * 500).toDouble(), ((due / 2000).ceil() * 2000).toDouble()}
        .where((v) => v >= due)
        .take(4)
        .toList();

    return CallbackShortcuts(
      bindings: {
        const SingleActivator(LogicalKeyboardKey.enter): () {
          if (canComplete) _complete(session);
        },
        const SingleActivator(LogicalKeyboardKey.f12): () {
          if (canComplete) _complete(session);
        },
      },
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(children: [
            Expanded(child: Text('Payment', style: theme.textTheme.titleLarge)),
            if (order != null) Chip(label: Text(order.orderNumber, style: kMonoStyle.copyWith(fontSize: 12))),
          ]),
          const SizedBox(height: 12),
          Row(crossAxisAlignment: CrossAxisAlignment.end, children: [
            Expanded(child: Text(_splitMode && _splits.isNotEmpty ? 'Still to pay' : 'Amount due', style: theme.textTheme.bodyLarge)),
            Text(formatRupeesNum(due),
                key: const Key('amount-due'),
                style: theme.textTheme.displaySmall?.copyWith(fontWeight: FontWeight.w700, fontFeatures: const [FontFeature.tabularFigures()])),
          ]),
          if (session.roundOffMax > 0 && _total(session) > 0) ...[
            const SizedBox(height: 8),
            Wrap(spacing: 8, runSpacing: 8, crossAxisAlignment: WrapCrossAlignment.center, children: [
              FilterChip(
                key: const Key('round-off'),
                avatar: const Icon(Icons.price_change_outlined, size: 18),
                label: Text(_waive(session) > 0 ? 'Round off −${formatRupeesNum(_waive(session))}' : 'Round off'),
                selected: _waive(session) > 0,
                onSelected: (_customWaive ?? _paise(session)) > 0 || _roundOn
                    ? (on) => setState(() {
                          _roundOn = on;
                          if (!on) _customWaive = null;
                        })
                    : null,
              ),
              TextButton(onPressed: () => _otherWaive(session), child: const Text('Other amount')),
              if (_waive(session) > 0)
                Text('Bill ${formatRupeesNum(_total(session))}', key: const Key('bill-total'), style: theme.textTheme.bodySmall),
            ]),
          ],
          if (live) ...[
            const SizedBox(height: 12),
            _BeforeYouCharge(
              couponController: _couponController,
              working: _working,
              onCoupon: () => _run(() => session.applyCoupon(_couponController.text.trim()), 'Coupon applied'),
              onPromotions: () => _run(session.applyPromotions, 'Eligible promotions applied'),
              onLoyalty: session.attachedCustomerId != null && (session.loyaltyAvailablePoints ?? 0) > 0
                  ? () async {
                      final pts = await _promptText(context, title: 'Redeem points (${session.loyaltyAvailablePoints} available)', label: 'Points to redeem', confirm: 'Redeem');
                      final n = int.tryParse(pts ?? '');
                      if (n != null && n > 0) await _run(() => session.redeemLoyaltyPoints(n), 'Points redeemed');
                    }
                  : null,
            ),
          ] else if (session.offlineMode || session.isCurrentOrderLocal) ...[
            const SizedBox(height: 12),
            _Banner(
              color: pos.warn,
              background: pos.warnSoft,
              icon: Icons.cloud_off,
              text: 'Offline sale: coupons, promotions, loyalty and credit need a connection. Cash, card and UPI are recorded and sync later.',
            ),
          ],
          const SizedBox(height: 16),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final (id, label, icon) in _tenders)
                _TenderButton(
                  label: label,
                  icon: icon,
                  selected: _method == id,
                  disabledReason: _tenderDisabledReason(session, id),
                  onTap: () => setState(() => _method = id),
                ),
            ],
          ),
          const SizedBox(height: 12),
          if (!_splitMode && _method == 'cash') ...[
            TextField(
              key: const Key('cash-tendered'),
              controller: _tenderedController,
              autofocus: true,
              keyboardType: const TextInputType.numberWithOptions(decimal: true),
              style: const TextStyle(fontSize: 22, fontWeight: FontWeight.w600),
              decoration: const InputDecoration(labelText: 'Cash received', hintText: 'Leave empty for exact amount'),
              onChanged: (_) => setState(() {}),
              // Enter completes the sale straight from the cash field, so a
              // keyboard-only cashier never has to leave it.
              onSubmitted: (_) {
                if (canComplete) _complete(session);
              },
            ),
            const SizedBox(height: 8),
            Wrap(spacing: 8, runSpacing: 8, children: [
              for (final v in quick)
                ActionChip(
                  label: Text(v == due ? 'Exact' : formatRupeesNum(v).replaceAll('.00', '')),
                  onPressed: () => setState(() => _tenderedController.text = v.toStringAsFixed(2)),
                ),
            ]),
            if (change != null && change >= 0) ...[
              const SizedBox(height: 12),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
                decoration: BoxDecoration(color: pos.paySoft, borderRadius: BorderRadius.circular(12)),
                child: Row(children: [
                  Expanded(child: Text('Change to return', style: TextStyle(color: pos.pay, fontWeight: FontWeight.w600, fontSize: 16))),
                  Text(formatRupeesNum(change), key: const Key('change-due'), style: TextStyle(color: pos.pay, fontWeight: FontWeight.w700, fontSize: 20)),
                ]),
              ),
            ] else if (change != null && change < 0)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Text('That’s ${formatRupeesNum(-change)} short', style: TextStyle(color: theme.colorScheme.error)),
              ),
          ],
          if (_method == 'card' || _method == 'upi')
            TextField(
              controller: _referenceController,
              decoration: const InputDecoration(labelText: 'Gateway reference (optional)', hintText: 'UTR / transaction id from the card machine'),
            ),
          if (_splitMode) ...[
            const SizedBox(height: 8),
            for (var i = 0; i < _splits.length; i++)
              ListTile(
                dense: true,
                contentPadding: EdgeInsets.zero,
                leading: const Icon(Icons.check_circle, size: 18),
                title: Text('${_splits[i].method.toUpperCase()} ${formatRupeesNum(_splits[i].amount)}'),
                trailing: IconButton(
                  tooltip: 'Remove this payment',
                  icon: const Icon(Icons.close, size: 18),
                  onPressed: () => setState(() => _splits.removeAt(i)),
                ),
              ),
            if (due > 0.004)
              Row(children: [
                Expanded(
                  child: TextField(
                    controller: _splitAmountController,
                    keyboardType: const TextInputType.numberWithOptions(decimal: true),
                    decoration: InputDecoration(labelText: 'Amount on ${_method.toUpperCase()}', hintText: formatRupeesNum(due)),
                  ),
                ),
                const SizedBox(width: 8),
                OutlinedButton(
                  onPressed: _tenderDisabledReason(session, _method) == null ? () => _addSplit(session) : null,
                  child: const Text('Add payment'),
                ),
              ]),
          ],
          Align(
            alignment: Alignment.centerLeft,
            child: TextButton.icon(
              icon: Icon(_splitMode ? Icons.call_merge : Icons.call_split, size: 18),
              label: Text(_splitMode ? 'Pay with one method' : 'Split across methods'),
              onPressed: () => setState(() {
                _splitMode = !_splitMode;
                _splits.clear();
                _message = null;
              }),
            ),
          ),
          if (_message != null)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: Text(_message!, key: const Key('payment-message')),
            ),
          const SizedBox(height: 4),
          Row(children: [
            Expanded(
              flex: 2,
              child: OutlinedButton(
                style: OutlinedButton.styleFrom(minimumSize: const Size(0, 56)),
                onPressed: _working ? null : () => Navigator.pop(context),
                child: const Text('Back'),
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              flex: 4,
              child: FilledButton(
                key: const Key('complete-sale'),
                style: FilledButton.styleFrom(backgroundColor: pos.pay, foregroundColor: pos.onPay, minimumSize: const Size(0, 56)),
                onPressed: canComplete ? () => _complete(session) : null,
                child: _working
                    ? const SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2))
                    : const Text('Complete sale', style: TextStyle(fontSize: 17, fontWeight: FontWeight.w700)),
              ),
            ),
          ]),
        ],
      ),
    );
  }
}

class _BeforeYouCharge extends StatelessWidget {
  final TextEditingController couponController;
  final bool working;
  final VoidCallback onCoupon;
  final VoidCallback onPromotions;
  final VoidCallback? onLoyalty;

  const _BeforeYouCharge({
    required this.couponController,
    required this.working,
    required this.onCoupon,
    required this.onPromotions,
    required this.onLoyalty,
  });

  @override
  Widget build(BuildContext context) {
    return Wrap(
      spacing: 8,
      runSpacing: 8,
      crossAxisAlignment: WrapCrossAlignment.center,
      children: [
        SizedBox(
          width: 200,
          child: TextField(
            controller: couponController,
            enabled: !working,
            textCapitalization: TextCapitalization.characters,
            decoration: InputDecoration(
              labelText: 'Coupon code',
              isDense: true,
              suffixIcon: IconButton(tooltip: 'Apply coupon', icon: const Icon(Icons.check), onPressed: working ? null : onCoupon),
            ),
            onSubmitted: (_) => onCoupon(),
          ),
        ),
        OutlinedButton.icon(onPressed: working ? null : onPromotions, icon: const Icon(Icons.local_offer_outlined, size: 18), label: const Text('Apply promotions')),
        if (onLoyalty != null)
          OutlinedButton.icon(onPressed: working ? null : onLoyalty, icon: const Icon(Icons.stars_outlined, size: 18), label: const Text('Redeem points')),
      ],
    );
  }
}

class _TenderButton extends StatelessWidget {
  final String label;
  final IconData icon;
  final bool selected;
  final String? disabledReason;
  final VoidCallback onTap;

  const _TenderButton({required this.label, required this.icon, required this.selected, required this.disabledReason, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final enabled = disabledReason == null;
    return Tooltip(
      message: disabledReason ?? label,
      child: Semantics(
        button: true,
        selected: selected,
        enabled: enabled,
        label: enabled ? label : '$label, unavailable: $disabledReason',
        excludeSemantics: true,
        child: Opacity(
          opacity: enabled ? 1 : 0.45,
          child: InkWell(
            borderRadius: BorderRadius.circular(12),
            onTap: enabled ? onTap : null,
            child: Container(
              width: 96,
              height: 72,
              decoration: BoxDecoration(
                color: selected ? scheme.primaryContainer.withValues(alpha: 0.5) : scheme.surface,
                borderRadius: BorderRadius.circular(12),
                border: Border.all(color: selected ? scheme.primary : scheme.outline, width: selected ? 2 : 1),
              ),
              child: Column(mainAxisAlignment: MainAxisAlignment.center, children: [
                Icon(icon, color: selected ? scheme.primary : null),
                const SizedBox(height: 4),
                Text(label, style: TextStyle(fontWeight: FontWeight.w600, color: selected ? scheme.primary : null)),
              ]),
            ),
          ),
        ),
      ),
    );
  }
}

class _Banner extends StatelessWidget {
  final Color color;
  final Color background;
  final IconData icon;
  final String text;

  const _Banner({required this.color, required this.background, required this.icon, required this.text});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(color: background, borderRadius: BorderRadius.circular(10)),
      child: Row(children: [
        Icon(icon, size: 18, color: color),
        const SizedBox(width: 8),
        Expanded(child: Text(text, style: TextStyle(color: color))),
      ]),
    );
  }
}
