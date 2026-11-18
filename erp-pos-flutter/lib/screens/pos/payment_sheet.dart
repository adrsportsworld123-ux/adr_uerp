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
//
// Previous outstanding (erp-core-go migrations/087): when the customer
// still owes on earlier credit sales, the sheet shows it and keeps it on
// this bill by default — all of it collected now, or an amount the cashier
// types (0 = shown on the receipt, nothing collected) — or the cashier
// leaves it off. The amount due is the bill plus what is collected; the
// server applies that part to the oldest sales first. Credit can't pay it.
// ---------------------------------------------------------------------

/// What the payment sheet hands back: the tenders, the waiver taken
/// (signed, 0 = none), and the previous outstanding collected with them
/// (null = left off the bill).
class _PaymentResult {
  final List<PaymentInput> payments;
  final double roundOff;
  final double? previousDueCollect;
  const _PaymentResult(this.payments, {this.roundOff = 0, this.previousDueCollect});
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

  /// The customer's previous outstanding (null: none, a walk-in, offline),
  /// whether it stays on this bill, and an amount typed (null = all of it).
  PreviousDueInfo? _previous;
  bool _keepPrevious = true;
  double? _customCollect;

  @override
  void initState() {
    super.initState();
    _roundOn = context.read<AppSession>().roundOffAuto;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _autoApplyPromotions();
      _loadPrevious();
    });
  }

  /// Reads the customer's previous outstanding — on opening, and after the
  /// customer changes. Kept on the bill each time it is read.
  Future<void> _loadPrevious() async {
    final info = await context.read<AppSession>().loadPreviousDue();
    if (!mounted) return;
    setState(() {
      _previous = info;
      _keepPrevious = true;
      _customCollect = null;
    });
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
      if (ok && after > before + 0.004) _message = tr('Promotions applied: {0} off', [formatRupeesNum(after - before)]);
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

  /// What this bill collects of the previous outstanding (0 when left off).
  double _collect() => _previous == null || !_keepPrevious ? 0 : _round2(_customCollect ?? _previous!.amount);

  /// Of what is collected, the part the cash, card and UPI added so far
  /// don't cover yet — what a credit split must leave room for.
  double _collectUncovered() {
    final nonCredit = _splits.where((p) => p.method != 'credit').fold<double>(0, (sum, p) => sum + p.amount);
    final left = _round2(_collect() - nonCredit);
    return left > 0 ? left : 0;
  }

  double _remaining(AppSession s) => _round2(_total(s) - _waive(s) + _collect() - _paid());

  Future<void> _otherCollect() async {
    final prev = _previous;
    if (prev == null) return;
    final text = await _promptText(context,
        title: tr('Previous outstanding to collect now (up to {0})', [formatRupeesNum(prev.amount)]),
        label: tr('Amount collected ({0})', [PosCurrency.symbol]),
        confirm: tr('Apply'));
    if (text == null || !mounted) return;
    final v = double.tryParse(text.trim());
    setState(() {
      if (v == null || v < 0 || v > prev.amount + 0.004) {
        _message = tr('Enter an amount up to {0}', [formatRupeesNum(prev.amount)]);
        return;
      }
      _customCollect = _round2(v);
      _keepPrevious = true;
      _splits.clear();
      _message = null;
    });
  }

  Future<void> _otherWaive(AppSession s) async {
    final text = await _promptText(context,
        title: tr('Round off — amount to waive (up to {0})', [formatRupeesNum(_waiveLimit(s))]), label: tr('Amount waived ({0})', [PosCurrency.symbol]), confirm: tr('Apply'));
    if (text == null || !mounted) return;
    final v = double.tryParse(text.trim());
    setState(() {
      if (v == null || v < 0 || v > _waiveLimit(s) + 0.004) {
        _message = tr('Enter an amount up to {0}', [formatRupeesNum(_waiveLimit(s))]);
        return;
      }
      _customWaive = _round2(v);
      _roundOn = v > 0;
      _message = null;
    });
  }

  String? _tenderDisabledReason(AppSession s, String method) {
    if (method == 'credit') {
      if (s.offlineMode || s.isCurrentOrderLocal) return tr('Credit needs a connection (the limit is checked live)');
      if (s.attachedCustomerId == null) return tr('Attach a customer first');
    }
    return null;
  }

  /// Adds (or changes) the sale's customer from the payment step — for a
  /// credit sale, loyalty, or a receipt with their name and mobile. The
  /// same picker and attach call as the cart's customer button (F4).
  Future<void> _pickCustomer(AppSession s) async {
    final result = await showDialog<_CustomerPickResult>(
      context: context,
      builder: (_) => ChangeNotifierProvider<AppSession>.value(value: s, child: const _CustomerPickerDialog()),
    );
    if (result == null || !mounted) return;
    await _run(() => _applyCustomerPick(s, result), result.clear ? tr('Walk-in sale — customer cleared') : tr('Customer added'));
    if (mounted) await _loadPrevious();
    if (mounted && s.customerChangeClearedDiscounts) {
      setState(() => _message = tr('The customer’s points and discounts came off. Apply discounts again before payment.'));
    }
  }

  Future<void> _clearCustomer(AppSession s) async {
    await _run(s.clearCustomer, tr('Walk-in sale — customer cleared'));
    if (mounted) await _loadPrevious();
    if (mounted && s.customerChangeClearedDiscounts) {
      setState(() => _message = tr('The customer’s points and discounts came off. Apply discounts again before payment.'));
    }
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
      _message = ok ? okText : (session.lastError ?? tr('That didn’t work'));
    });
  }

  void _complete(AppSession s) {
    final due = _remaining(s);
    final roundOff = -_waive(s);
    final collect = _previous != null && _keepPrevious ? _collect() : null;
    if (_splitMode) {
      if (due > 0.004 || _collectUncovered() > 0.004) return;
      Navigator.pop(context, _PaymentResult(List<PaymentInput>.of(_splits), roundOff: roundOff, previousDueCollect: collect));
      return;
    }
    if (_method == 'credit' && _collect() > 0) return;
    if (_method == 'cash') {
      final tendered = double.tryParse(_tenderedController.text) ?? due;
      if (tendered + 0.004 < due) return;
    }
    final ref = _referenceController.text.trim();
    Navigator.pop(
        context,
        _PaymentResult([PaymentInput(method: _method, amount: due, reference: (_method == 'card' || _method == 'upi') && ref.isNotEmpty ? ref : null)],
            roundOff: roundOff, previousDueCollect: collect));
  }

  void _addSplit(AppSession s) {
    final remaining = _remaining(s);
    // Credit pays only the bill: it leaves room for the previous
    // outstanding still to be covered by cash, card or UPI.
    final max = _method == 'credit' ? _round2(remaining - _collectUncovered()) : remaining;
    final amount = double.tryParse(_splitAmountController.text) ?? max;
    if (amount <= 0 || amount > max + 0.004) {
      setState(() => _message = _method == 'credit' && max < remaining
          ? tr('Credit can’t pay the previous outstanding — split the payment, or leave it off')
          : tr('Enter an amount up to {0}', [formatRupeesNum(max)]));
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
    final creditBlocked = !_splitMode && _method == 'credit' && _collect() > 0;
    final canComplete = !_working &&
        (_splitMode
            ? due <= 0.004 && _splits.isNotEmpty && _collectUncovered() <= 0.004
            : _tenderDisabledReason(session, _method) == null &&
                !creditBlocked &&
                (_method != 'cash' || tendered == null || tendered + 0.004 >= due));

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
            Expanded(child: Text(tr('Payment'), style: theme.textTheme.titleLarge)),
            if (order != null) Chip(label: Text(order.orderNumber, style: kMonoStyle.copyWith(fontSize: 12))),
          ]),
          const SizedBox(height: 12),
          Row(crossAxisAlignment: CrossAxisAlignment.end, children: [
            Expanded(child: Text(_splitMode && _splits.isNotEmpty ? tr('Still to pay') : tr('Amount due'), style: theme.textTheme.bodyLarge)),
            Text(formatRupeesNum(due),
                key: const Key('amount-due'),
                style: theme.textTheme.displaySmall?.copyWith(fontWeight: FontWeight.w700, fontFeatures: const [FontFeature.tabularFigures()])),
          ]),
          if (_collect() > 0)
            Align(
              alignment: Alignment.centerRight,
              child: Text(tr('Bill {0} + previous {1}', [formatRupeesNum(_round2(_total(session) - _waive(session))), formatRupeesNum(_collect())]),
                  key: const Key('bill-plus-previous'), style: theme.textTheme.bodySmall),
            ),
          const SizedBox(height: 12),
          _PaymentCustomer(enabled: live && !_working, onPick: () => _pickCustomer(session), onClear: () => _clearCustomer(session)),
          if (_previous != null && live) ...[
            const SizedBox(height: 8),
            _PreviousDueRow(
              info: _previous!,
              keep: _keepPrevious,
              collect: _collect(),
              enabled: !_working,
              onKeep: (on) => setState(() {
                _keepPrevious = on;
                _splits.clear();
                _message = null;
              }),
              onChange: _otherCollect,
            ),
          ],
          if (session.roundOffMax > 0 && _total(session) > 0) ...[
            const SizedBox(height: 8),
            Wrap(spacing: 8, runSpacing: 8, crossAxisAlignment: WrapCrossAlignment.center, children: [
              FilterChip(
                key: const Key('round-off'),
                avatar: const Icon(Icons.price_change_outlined, size: 18),
                label: Text(_waive(session) > 0 ? tr('Round off −{0}', [formatRupeesNum(_waive(session))]) : tr('Round off')),
                selected: _waive(session) > 0,
                onSelected: (_customWaive ?? _paise(session)) > 0 || _roundOn
                    ? (on) => setState(() {
                          _roundOn = on;
                          if (!on) _customWaive = null;
                        })
                    : null,
              ),
              TextButton(onPressed: () => _otherWaive(session), child: Text(tr('Other amount'))),
              if (_waive(session) > 0)
                Text(tr('Bill {0}', [formatRupeesNum(_total(session))]), key: const Key('bill-total'), style: theme.textTheme.bodySmall),
            ]),
          ],
          if (live) ...[
            const SizedBox(height: 12),
            _BeforeYouCharge(
              couponController: _couponController,
              working: _working,
              onCoupon: () => _run(() => session.applyCoupon(_couponController.text.trim()), tr('Coupon applied')),
              onPromotions: () => _run(session.applyPromotions, tr('Eligible promotions applied')),
              onLoyalty: session.attachedCustomerId != null && (session.loyaltyAvailablePoints ?? 0) > 0
                  ? () async {
                      final pts = await _promptText(context, title: tr('Redeem points ({0} available)', [session.loyaltyAvailablePoints]), label: tr('Points to redeem'), confirm: tr('Redeem'));
                      final n = int.tryParse(pts ?? '');
                      if (n != null && n > 0) await _run(() => session.redeemLoyaltyPoints(n), tr('Points redeemed'));
                    }
                  : null,
            ),
          ] else if (session.offlineMode || session.isCurrentOrderLocal) ...[
            const SizedBox(height: 12),
            _Banner(
              color: pos.warn,
              background: pos.warnSoft,
              icon: Icons.cloud_off,
              text: tr('Offline sale: coupons, promotions, loyalty and credit need a connection. Cash, card and UPI are recorded and sync later.'),
            ),
          ],
          const SizedBox(height: 16),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final (id, label, icon) in _tenders)
                _TenderButton(
                  label: tr(label),
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
              decoration: InputDecoration(labelText: tr('Cash received'), hintText: tr('Leave empty for exact amount')),
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
                  label: Text(v == due ? tr('Exact') : formatRupeesNum(v).replaceAll('.00', '')),
                  onPressed: () => setState(() => _tenderedController.text = v.toStringAsFixed(2)),
                ),
            ]),
            if (change != null && change >= 0) ...[
              const SizedBox(height: 12),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
                decoration: BoxDecoration(color: pos.paySoft, borderRadius: BorderRadius.circular(12)),
                child: Row(children: [
                  Expanded(child: Text(tr('Change to return'), style: TextStyle(color: pos.pay, fontWeight: FontWeight.w600, fontSize: 16))),
                  Text(formatRupeesNum(change), key: const Key('change-due'), style: TextStyle(color: pos.pay, fontWeight: FontWeight.w700, fontSize: 20)),
                ]),
              ),
            ] else if (change != null && change < 0)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Text(tr('That’s {0} short', [formatRupeesNum(-change)]), style: TextStyle(color: theme.colorScheme.error)),
              ),
          ],
          if (_method == 'card' || _method == 'upi')
            TextField(
              controller: _referenceController,
              decoration: InputDecoration(labelText: tr('Gateway reference (optional)'), hintText: tr('UTR / transaction id from the card machine')),
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
                  tooltip: tr('Remove this payment'),
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
                    decoration: InputDecoration(labelText: tr('Amount on {0}', [_method.toUpperCase()]), hintText: formatRupeesNum(due)),
                  ),
                ),
                const SizedBox(width: 8),
                OutlinedButton(
                  onPressed: _tenderDisabledReason(session, _method) == null ? () => _addSplit(session) : null,
                  child: Text(tr('Add payment')),
                ),
              ]),
          ],
          Align(
            alignment: Alignment.centerLeft,
            child: TextButton.icon(
              icon: Icon(_splitMode ? Icons.call_merge : Icons.call_split, size: 18),
              label: Text(_splitMode ? tr('Pay with one method') : tr('Split across methods')),
              onPressed: () => setState(() {
                _splitMode = !_splitMode;
                _splits.clear();
                _message = null;
              }),
            ),
          ),
          if (creditBlocked)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: Text(tr('Credit can’t pay the previous outstanding — split the payment, or leave it off'),
                  key: const Key('previous-due-credit'), style: TextStyle(color: theme.colorScheme.error)),
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
                child: Text(tr('Back')),
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
                    : Text(tr('Complete sale'), style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w700)),
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
              labelText: tr('Coupon code'),
              isDense: true,
              suffixIcon: IconButton(tooltip: tr('Apply coupon'), icon: const Icon(Icons.check), onPressed: working ? null : onCoupon),
            ),
            onSubmitted: (_) => onCoupon(),
          ),
        ),
        OutlinedButton.icon(onPressed: working ? null : onPromotions, icon: const Icon(Icons.local_offer_outlined, size: 18), label: Text(tr('Apply promotions'))),
        if (onLoyalty != null)
          OutlinedButton.icon(onPressed: working ? null : onLoyalty, icon: const Icon(Icons.stars_outlined, size: 18), label: Text(tr('Redeem points'))),
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

/// The payment step's customer: who the sale is for, and a button to add
/// one or change it. Live carts only, like the cart's customer button.
class _PaymentCustomer extends StatelessWidget {
  final bool enabled;
  final VoidCallback onPick;
  final VoidCallback onClear;

  const _PaymentCustomer({required this.enabled, required this.onPick, required this.onClear});

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    final theme = Theme.of(context);
    final name = session.attachedCustomerName;
    final offline = session.offlineMode || session.isCurrentOrderLocal;
    return Container(
      key: const Key('payment-customer'),
      padding: const EdgeInsets.fromLTRB(12, 6, 6, 6),
      decoration: BoxDecoration(border: Border.all(color: theme.colorScheme.outline), borderRadius: BorderRadius.circular(12)),
      child: Row(children: [
        Icon(name == null ? Icons.person_outline : Icons.person, size: 20, color: name == null ? theme.colorScheme.onSurfaceVariant : theme.colorScheme.primary),
        const SizedBox(width: 10),
        Expanded(
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, mainAxisSize: MainAxisSize.min, children: [
            Text(name ?? tr('No customer'), maxLines: 1, overflow: TextOverflow.ellipsis, style: theme.textTheme.titleSmall),
            Text(
              offline
                  ? _offlineReason
                  : name == null
                      ? tr('Add one for credit, loyalty points, or their name on the receipt')
                      : session.loyaltyAvailablePoints != null
                          ? tr('{0} pts', [session.loyaltyAvailablePoints])
                          : tr('Customer'),
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.onSurfaceVariant),
            ),
          ]),
        ),
        const SizedBox(width: 8),
        if (name != null)
          IconButton(
            key: const Key('payment-clear-customer'),
            tooltip: tr('Clear customer (walk-in)'),
            onPressed: enabled ? onClear : null,
            icon: const Icon(Icons.person_remove_alt_1, size: 20),
          ),
        OutlinedButton.icon(
          key: const Key('payment-add-customer'),
          onPressed: enabled ? onPick : null,
          icon: Icon(name == null ? Icons.person_add_alt : Icons.swap_horiz, size: 18),
          label: Text(name == null ? tr('Add customer') : tr('Change')),
        ),
      ]),
    );
  }
}

/// The customer's previous outstanding on the payment step: how much and
/// over how many bills, a switch to keep it on this bill, and what is
/// collected now (all of it, unless the cashier changes the amount).
class _PreviousDueRow extends StatelessWidget {
  final PreviousDueInfo info;
  final bool keep;
  final double collect;
  final bool enabled;
  final ValueChanged<bool> onKeep;
  final VoidCallback onChange;

  const _PreviousDueRow({required this.info, required this.keep, required this.collect, required this.enabled, required this.onKeep, required this.onChange});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final pos = theme.pos;
    return Container(
      key: const Key('previous-due'),
      padding: const EdgeInsets.fromLTRB(12, 6, 6, 6),
      decoration: BoxDecoration(
        color: keep ? pos.warnSoft : null,
        border: Border.all(color: keep ? pos.warn : theme.colorScheme.outline),
        borderRadius: BorderRadius.circular(12),
      ),
      child: Row(children: [
        Icon(Icons.history, size: 20, color: pos.warn),
        const SizedBox(width: 10),
        Expanded(
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, mainAxisSize: MainAxisSize.min, children: [
            Text('${tr('Previous outstanding')} ${formatRupeesNum(info.amount)}', key: const Key('previous-due-amount'), style: theme.textTheme.titleSmall),
            Text(
              [
                info.invoiceCount == 1 ? tr('1 earlier bill') : tr('{0} earlier bills', [info.invoiceCount]),
                if (keep) collect > 0 ? tr('Collecting {0} now', [formatRupeesNum(collect)]) : tr('Kept on the bill — nothing collected now'),
              ].join(' · '),
              style: theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.onSurfaceVariant),
            ),
          ]),
        ),
        if (keep) TextButton(key: const Key('previous-due-change'), onPressed: enabled ? onChange : null, child: Text(tr('Change amount'))),
        Tooltip(
          message: tr('Add to this bill'),
          child: Switch(key: const Key('previous-due-keep'), value: keep, onChanged: enabled ? onKeep : null),
        ),
      ]),
    );
  }
}
