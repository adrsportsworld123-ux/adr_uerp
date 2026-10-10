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
                        trailing: c.creditHold ? Icon(Icons.block, color: Theme.of(context).colorScheme.error, size: 18) : null,
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
              decoration: const InputDecoration(labelText: 'Phone', border: OutlineInputBorder(), isDense: true),
              onChanged: (_) => setState(() {}),
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
// Manual discount — mirrors internal/sales/discounts.go's tiers (0-5% no
// approval, 5-15% Branch Manager PIN, 15-25% Merchant Admin PIN) as a UX
// nicety only; the server enforces the real boundaries. Phase 12: the
// approver is named by EMPLOYEE CODE (what staff know) and enters their
// PIN on the shared ApprovalPinPad, instead of a raw user id.
// ---------------------------------------------------------------------

class _DiscountInput {
  final double percent;
  final String approverCode;
  final String approverPin;
  final String reason;

  _DiscountInput(this.percent, this.approverCode, this.approverPin, this.reason);
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

  double get _percent => double.tryParse(_percentController.text) ?? 0;
  bool get _needsApproval => _percent > 5;
  String get _tierLabel => _percent > 15 ? 'a Merchant Admin' : 'a Branch Manager or Merchant Admin';

  bool get _canApply =>
      _percent > 0 &&
      _percent <= 25 &&
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
      title: const Text('Manual discount'),
      content: SizedBox(
        width: 340,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              TextField(
                controller: _percentController,
                autofocus: true,
                keyboardType: const TextInputType.numberWithOptions(decimal: true),
                decoration: InputDecoration(
                  labelText: 'Discount %',
                  helperText: _percent > 25 ? 'The maximum is 25%' : 'Up to 5% needs no approval',
                  helperStyle: _percent > 25 ? TextStyle(color: theme.colorScheme.error) : null,
                ),
                onChanged: (_) => setState(() {}),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _reasonController,
                decoration: const InputDecoration(labelText: 'Reason'),
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
                      Expanded(child: Text('Above 5% needs $_tierLabel to approve.', style: TextStyle(color: theme.pos.plum))),
                    ],
                  ),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: _approverController,
                  textCapitalization: TextCapitalization.characters,
                  decoration: const InputDecoration(labelText: 'Approver employee code', hintText: 'e.g. EMP002'),
                  onChanged: (_) => setState(() {}),
                ),
                const SizedBox(height: 8),
                Text('Approver PIN', style: theme.textTheme.labelLarge),
                ApprovalPinPad(onChanged: (pin) => setState(() => _pin = pin)),
              ],
            ],
          ),
        ),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
        FilledButton(
          onPressed: _canApply
              ? () => Navigator.pop(
                    context,
                    _DiscountInput(_percent, _approverController.text.trim(), _pin, _reasonController.text.trim()),
                  )
              : null,
          child: const Text('Apply'),
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
        TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
        FilledButton(onPressed: () => Navigator.pop(ctx, controller.text.trim()), child: Text(confirm)),
      ],
    ),
  );
}
