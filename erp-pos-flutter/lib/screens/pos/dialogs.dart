part of '../pos_screen.dart';

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
  final String? walkInEmail;

  _CustomerPickResult.existing(this.existing)
      : walkInName = null,
        walkInPhone = null,
        walkInEmail = null;
  _CustomerPickResult.walkIn(this.walkInName, this.walkInPhone, this.walkInEmail) : existing = null;
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
  final _walkInEmailController = TextEditingController();
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

  // Same rule as the server (erp-core-go customers.ValidEmail): optional,
  // but a typo like "ravi.gmail.com" is caught here, not at checkout.
  String? get _emailError {
    final e = _walkInEmailController.text.trim();
    if (e.isEmpty) return null;
    final at = e.lastIndexOf('@');
    final ok = at > 0 &&
        at < e.length - 1 &&
        '@'.allMatches(e).length == 1 &&
        !e.contains(' ') &&
        e.substring(at + 1).contains('.') &&
        !e.endsWith('.') &&
        !e.substring(at + 1).startsWith('.');
    return ok ? null : tr('Not a valid email address');
  }

  @override
  void dispose() {
    _searchController.dispose();
    _walkInNameController.dispose();
    _walkInPhoneController.dispose();
    _walkInEmailController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text(tr('Attach a customer')),
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
                labelText: tr('Search by name/phone/email'),
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
                        trailing: c.creditHold ? Icon(Icons.block, color: Theme.of(context).colorScheme.error, size: 18) : null,
                        onTap: () => Navigator.pop(context, _CustomerPickResult.existing(c)),
                      ),
                  ],
                ),
              ),
            const Divider(height: 24),
            Text(tr('Or register a walk-in'), style: Theme.of(context).textTheme.labelMedium),
            const SizedBox(height: 8),
            TextField(
              controller: _walkInNameController,
              decoration: InputDecoration(labelText: tr('Name'), border: const OutlineInputBorder(), isDense: true),
              // Bug fix: this dialog is a real State (not a StatefulBuilder),
              // so without onChanged->setState here, typing a name/phone
              // never rebuilds it — the "Add walk-in" button's disabled
              // check below reads these controllers' .text but was only
              // ever evaluated once, at the dialog's first build, when both
              // fields were still empty. That's the "Add Walk-in button not
              // working" symptom reported live: the button visually exists
              // but silently stays disabled no matter what's typed.
              onChanged: (_) => setState(() {}),
            ),
            const SizedBox(height: 8),
            TextField(
              controller: _walkInPhoneController,
              keyboardType: TextInputType.phone,
              decoration: InputDecoration(labelText: tr('Phone'), border: const OutlineInputBorder(), isDense: true),
              onChanged: (_) => setState(() {}),
            ),
            const SizedBox(height: 8),
            TextField(
              key: const Key('walkin-email'),
              controller: _walkInEmailController,
              keyboardType: TextInputType.emailAddress,
              decoration: InputDecoration(
                labelText: tr('Email (optional)'),
                helperText: tr('For e-receipts'),
                border: const OutlineInputBorder(),
                isDense: true,
                errorText: _emailError,
              ),
              onChanged: (_) => setState(() {}),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: Text(tr('Cancel'))),
        FilledButton(
          onPressed: (_walkInNameController.text.trim().isEmpty && _walkInPhoneController.text.trim().isEmpty) || _emailError != null
              ? null
              : () => Navigator.pop(
                    context,
                    _CustomerPickResult.walkIn(
                      _walkInNameController.text.trim(),
                      _walkInPhoneController.text.trim(),
                      _walkInEmailController.text.trim().isEmpty ? null : _walkInEmailController.text.trim(),
                    ),
                  ),
          child: Text(tr('Add walk-in')),
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------
// Manual discount — shows the merchant's discount tiers (Phase 15 approval
// policy, AppSession.discountTiers; 0-5% none, 5-15% Branch Manager, 15-25%
// Merchant Admin by default) as a UX nicety only; the server enforces them. Phase 12: the
// approver is named by EMPLOYEE CODE (what staff know) and enters their
// PIN on the shared ApprovalPinPad, instead of a raw user id.
//
// The discount is a percent or a fixed rupee amount off the sale (₹100
// off). A fixed amount falls in the tier of the percent of the cart it
// comes to, measured like the server does: against the lines' price ×
// quantity, before any discount.
// ---------------------------------------------------------------------

class _DiscountInput {
  /// True: [value] is rupees off the sale. False: [value] is a percent.
  final bool fixed;
  final double value;
  final String approverCode;
  final String approverPin;
  final String reason;

  _DiscountInput(this.fixed, this.value, this.approverCode, this.approverPin, this.reason);
}

class _DiscountDialog extends StatefulWidget {
  const _DiscountDialog();

  @override
  State<_DiscountDialog> createState() => _DiscountDialogState();
}

class _DiscountDialogState extends State<_DiscountDialog> {
  final _percentController = TextEditingController();
  final _reasonController = TextEditingController();
  final _approverController = TextEditingController();
  String _pin = '';
  bool _fixed = false;

  double get _value => double.tryParse(_percentController.text) ?? 0;

  /// The cart's price × quantity before discounts, what both kinds of
  /// discount are measured against.
  double get _base {
    final lines = context.read<AppSession>().currentOrder?.lines ?? const <OrderLine>[];
    return lines.fold(0.0, (sum, l) => sum + (double.tryParse(l.unitPrice) ?? 0) * (double.tryParse(l.quantity) ?? 0));
  }

  /// What other discounts have left to take off.
  double get _remaining => _base - (double.tryParse(context.read<AppSession>().currentOrder?.discountTotal ?? '') ?? 0);

  /// The percent the approval tiers judge: typed, or what the amount comes to.
  double get _percent => !_fixed ? _value : (_base > 0 ? _value / _base * 100 : 0);
  List<DiscountTier> get _tiers => context.read<AppSession>().discountTiers;
  double get _max => _tiers.last.upTo;

  /// The largest fixed amount allowed: the policy's top percent of the
  /// cart, and never more than is left to discount.
  double get _maxAmount {
    final byPolicy = (_base * _max).floorToDouble() / 100;
    return byPolicy < _remaining ? byPolicy : _remaining;
  }

  bool get _overMax => _fixed ? _value > _maxAmount + 0.001 : _value > _max;

  /// A rupee amount has at most two decimals.
  bool get _validAmount => !_fixed || (_value * 100 - (_value * 100).roundToDouble()).abs() < 1e-6;
  double get _free => _tiers.where((t) => t.approver == 'none').fold(0.0, (m, t) => t.upTo > m ? t.upTo : m);
  DiscountTier? get _tier => DiscountTier.forPercent(_tiers, _percent);
  bool get _needsApproval => _tier != null && _tier!.approver != 'none';
  String get _tierLabel => _tier?.approver == 'merchant_admin' ? 'a Merchant Admin' : 'a Branch Manager or Merchant Admin';

  String _pct(double v) => v == v.roundToDouble() ? v.toStringAsFixed(0) : v.toString();

  String get _helperText {
    final free = _free > 0 ? tr('Up to {0}% needs no approval', [_pct(_free)]) : tr('Every discount needs approval');
    if (!_fixed) return _value > _max ? tr('The maximum is {0}%', [_pct(_max)]) : free;
    if (!_validAmount) return tr('Rupees and paise only, e.g. 99.50');
    if (_overMax) return tr('The maximum on this sale is {0}', [formatRupeesNum(_maxAmount)]);
    if (_value <= 0) return _free > 0 ? tr('{0} ({1} on this sale)', [free, formatRupeesNum(_base * _free / 100)]) : free;
    return '${_percent.toStringAsFixed(2)}% of the sale';
  }

  bool get _canApply =>
      _value > 0 &&
      !_overMax &&
      _validAmount &&
      _reasonController.text.trim().isNotEmpty &&
      (!_needsApproval || (_approverController.text.trim().isNotEmpty && _pin.length >= 4));

  @override
  void dispose() {
    _percentController.dispose();
    _reasonController.dispose();
    _approverController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return AlertDialog(
      title: Text(tr('Manual discount')),
      content: SizedBox(
        width: 340,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              SegmentedButton<bool>(
                key: const Key('discount-mode'),
                segments: [
                  ButtonSegment(value: false, label: Text(tr('Percent %'))),
                  ButtonSegment(value: true, label: Text(tr('Amount {0}', [PosCurrency.symbol]))),
                ],
                selected: {_fixed},
                onSelectionChanged: (v) => setState(() {
                  _fixed = v.first;
                  _percentController.clear();
                }),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _percentController,
                autofocus: true,
                keyboardType: const TextInputType.numberWithOptions(decimal: true),
                decoration: InputDecoration(
                  labelText: _fixed ? tr('Discount amount ({0})', [PosCurrency.symbol]) : tr('Discount %'),
                  helperText: _helperText,
                  helperMaxLines: 2,
                  helperStyle: _overMax || !_validAmount ? TextStyle(color: theme.colorScheme.error) : null,
                ),
                onChanged: (_) => setState(() {}),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _reasonController,
                decoration: InputDecoration(labelText: tr('Reason')),
                // Rebuild on every keystroke so Apply's enabled state is
                // always current (the original "Apply stays disabled" bug).
                onChanged: (_) => setState(() {}),
              ),
              if (_needsApproval) ...[
                const SizedBox(height: 16),
                Container(
                  padding: const EdgeInsets.all(10),
                  decoration: BoxDecoration(color: theme.pos.plumSoft, borderRadius: BorderRadius.circular(10)),
                  child: Row(
                    children: [
                      Icon(Icons.verified_user_outlined, color: theme.pos.plum, size: 18),
                      const SizedBox(width: 8),
                      Expanded(child: Text(tr('This discount needs {0} to approve.', [_tierLabel]), style: TextStyle(color: theme.pos.plum))),
                    ],
                  ),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: _approverController,
                  textCapitalization: TextCapitalization.characters,
                  decoration: InputDecoration(labelText: tr('Approver employee code'), hintText: 'e.g. EMP002'),
                  onChanged: (_) => setState(() {}),
                ),
                const SizedBox(height: 8),
                Text(tr('Approver PIN'), style: theme.textTheme.labelLarge),
                ApprovalPinPad(onChanged: (pin) => setState(() => _pin = pin)),
              ],
            ],
          ),
        ),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: Text(tr('Cancel'))),
        FilledButton(
          onPressed: _canApply
              ? () => Navigator.pop(
                    context,
                    _DiscountInput(_fixed, _value, _approverController.text.trim(), _pin, _reasonController.text.trim()),
                  )
              : null,
          child: Text(tr('Apply')),
        ),
      ],
    );
  }
}

// ---------------------------------------------------------------------
// Small prompts
// ---------------------------------------------------------------------

Future<String?> _promptText(BuildContext context, {required String title, required String label, String confirm = 'OK'}) {
  final controller = TextEditingController();
  return showDialog<String>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: Text(title),
      content: TextField(
        controller: controller,
        autofocus: true,
        decoration: InputDecoration(labelText: label),
        onSubmitted: (v) => Navigator.pop(ctx, v.trim()),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(ctx), child: Text(tr('Cancel'))),
        FilledButton(onPressed: () => Navigator.pop(ctx, controller.text.trim()), child: Text(confirm)),
      ],
    ),
  );
}
