import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/api_client.dart';
import '../l10n/l10n.dart';
import '../state/session.dart';
import '../ui/approval_pin_pad.dart';
import '../ui/format.dart';

/// Return items from a completed sale (Phase 13; FRD §3 "Return &
/// Exchange"). The cashier picks quantities per line, a reason and how the
/// money goes back. When the merchant requires approval, a Branch Manager or
/// Merchant Admin enters their employee code and PIN here. The amount shown
/// is an estimate from the per-unit price; the server computes the exact
/// refund from what was paid and that is what the confirmation shows.
///
/// Pops `true` once a return is recorded.
class ReturnScreen extends StatefulWidget {
  final String orderId;
  final String orderNumber;

  const ReturnScreen({super.key, required this.orderId, required this.orderNumber});

  @override
  State<ReturnScreen> createState() => _ReturnScreenState();
}

const _reasons = ['Wrong size', 'Defective', 'Changed mind', 'Damaged in transit'];

class _ReturnScreenState extends State<ReturnScreen> {
  ReturnableOrder? _order;
  String? _loadError;
  final Map<String, double> _qty = {};
  String? _reason;
  final _otherReason = TextEditingController();
  String _method = 'cash';
  final _reference = TextEditingController();
  bool _restock = true;
  final _approverCode = TextEditingController();
  String _pin = '';
  bool _submitting = false;
  // Set when the server asks for an approver the estimate didn't expect.
  bool _approvalDemanded = false;
  String? _error;
  // One key for this screen: a retry after a lost response replays the
  // same return instead of refunding twice.
  late final String _idempotencyKey = context.read<AppSession>().newIdempotencyKey();

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _otherReason.dispose();
    _reference.dispose();
    _approverCode.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final o = await context.read<AppSession>().returnableFor(widget.orderId);
      if (!mounted) return;
      setState(() => _order = o);
    } on ApiException catch (e) {
      if (mounted) setState(() => _loadError = e.message);
    } catch (_) {
      if (mounted) setState(() => _loadError = tr('Can\'t reach the server. Returns need a connection.'));
    }
  }

  double _max(ReturnableLine l) => double.tryParse(l.returnableQuantity) ?? 0;

  double get _estimate {
    var sum = 0.0;
    for (final l in _order?.lines ?? const <ReturnableLine>[]) {
      sum += (_qty[l.lineId] ?? 0) * (double.tryParse(l.unitRefund) ?? 0);
    }
    return sum;
  }

  bool get _anySelected => _qty.values.any((q) => q > 0);
  String get _reasonText => _reason == 'Other' ? _otherReason.text.trim() : (_reason ?? '');
  bool get _creditAllowed => (double.tryParse(_order?.creditOutstanding ?? '0') ?? 0) > 0;

  bool get _canSubmit =>
      !_submitting &&
      _anySelected &&
      _reasonText.isNotEmpty &&
      (!_needsApproval || (_approverCode.text.trim().isNotEmpty && _pin.length >= 4));

  /// Approval (Sales Policy) applies only above the approval policy's refund
  /// threshold. The server decides on the exact refund and says so if it
  /// disagrees with this estimate.
  bool get _needsApproval {
    final o = _order;
    if (o == null) return false;
    if (_approvalDemanded) return true;
    return o.requiresApproval && (o.approvalAbove == null || _estimate > o.approvalAbove!);
  }

  void _setQty(ReturnableLine l, double q) {
    final max = _max(l);
    setState(() => _qty[l.lineId] = q.clamp(0, max).toDouble());
  }

  Future<void> _submit() async {
    final session = context.read<AppSession>();
    setState(() {
      _submitting = true;
      _error = null;
    });
    try {
      final result = await session.createReturn(
        widget.orderId,
        ReturnRequest(
          quantities: Map.of(_qty),
          reason: _reasonText,
          refundMethod: _method,
          refundReference: _reference.text.trim(),
          restock: _restock,
          approverCode: _approverCode.text.trim(),
          approverPin: _pin,
          idempotencyKey: _idempotencyKey,
        ),
      );
      if (!mounted) return;
      await showDialog<void>(
        context: context,
        builder: (ctx) => AlertDialog(
          icon: const Icon(Icons.check_circle_outline),
          title: Text(tr('Refund {0}', [formatRupees(result.refundAmount)])),
          content: Text([
            'Return ${result.returnNumber} recorded.',
            tr('Give back {0} by {1}.', [formatRupees(result.refundAmount), _methodLabel(result.refundMethod)]),
            if (result.taxBreakup?.rows.isNotEmpty == true)
              tr('Tax given back: {0}.', [result.taxBreakup!.rows.map((t) => '${t.label} ${formatRupees(t.amount)}').join(' · ')]),
            result.restocked ? tr('Items are back in stock.') : tr('Items were not restocked.'),
          ].join('\n')),
          actions: [FilledButton(onPressed: () => Navigator.pop(ctx), child: Text(tr('Done')))],
        ),
      );
      if (mounted) Navigator.of(context).pop(true);
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _submitting = false;
        if (e.code == 'RETURN_NOT_APPROVED' && !_needsApproval) {
          _approvalDemanded = true;
          _error = tr('This refund needs a manager\'s approval.');
          return;
        }
        _error = switch (e.code) {
          'RETURN_NOT_APPROVED' => tr('Approval failed. Check the manager\'s employee code and PIN.'),
          'RETURN_WINDOW_CLOSED' => tr('This sale is past the return window.'),
          'CREDIT_REFUND_EXCEEDED' => tr('Store credit can only refund what is still unpaid on this sale. Choose cash, card or UPI.'),
          _ => e.message,
        };
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _error = tr('Can\'t reach the server. Nothing was refunded. Try again when back online.');
      });
    }
  }

  static String _methodLabel(String m) => switch (m) {
        'cash' => 'cash',
        'card' => 'card',
        'upi' => 'UPI',
        'credit' => 'store credit',
        _ => m,
      };

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final o = _order;
    return Scaffold(
      appBar: AppBar(title: Text(tr('Return · {0}', [widget.orderNumber]))),
      body: _loadError != null
          ? Center(child: Padding(padding: const EdgeInsets.all(24), child: Text(_loadError!, textAlign: TextAlign.center)))
          : o == null
              ? const Center(child: CircularProgressIndicator())
              : !o.returnable
                  ? Center(
                      child: Padding(
                        padding: const EdgeInsets.all(24),
                        child: Text(
                          !o.windowOpen
                              ? tr('This sale is past the return window.')
                              : tr('This sale can\'t be returned ({0}).', [statusLabel(o.status).toLowerCase()]),
                          textAlign: TextAlign.center,
                        ),
                      ),
                    )
                  : _form(theme, o),
      bottomNavigationBar: o != null && o.returnable
          ? SafeArea(
              child: Padding(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
                child: Column(mainAxisSize: MainAxisSize.min, children: [
                  if (_error != null)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 8),
                      child: Text(_error!, key: const Key('return-error'), style: TextStyle(color: theme.colorScheme.error)),
                    ),
                  SizedBox(
                    width: double.infinity,
                    height: 52,
                    child: FilledButton.icon(
                      key: const Key('return-submit'),
                      onPressed: _canSubmit ? _submit : null,
                      icon: _submitting
                          ? const SizedBox(width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2))
                          : const Icon(Icons.assignment_return),
                      label: Text(_anySelected ? tr('Refund about {0}', [formatRupeesNum(_estimate)]) : tr('Choose items to return')),
                    ),
                  ),
                ]),
              ),
            )
          : null,
    );
  }

  Widget _form(ThemeData theme, ReturnableOrder o) {
    final returnable = o.lines.where((l) => _max(l) > 0).toList();
    final done = o.lines.where((l) => _max(l) <= 0).toList();
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Row(children: [
          Expanded(child: Text(tr('Items'), style: theme.textTheme.titleMedium)),
          TextButton(
            onPressed: () => setState(() {
              for (final l in returnable) {
                _qty[l.lineId] = _max(l);
              }
            }),
            child: Text(tr('Return everything')),
          ),
        ]),
        for (final l in returnable) _lineRow(theme, l),
        for (final l in done)
          ListTile(
            dense: true,
            enabled: false,
            title: Text(l.productName),
            subtitle: Text(tr('{0} · already returned', [l.sku])),
          ),
        const Divider(height: 32),
        Text(tr('Reason'), style: theme.textTheme.titleMedium),
        const SizedBox(height: 8),
        Wrap(spacing: 8, runSpacing: 8, children: [
          // The reason is recorded in English; only the chip is translated.
          for (final r in [..._reasons, 'Other'])
            ChoiceChip(
              label: Text(tr(r)),
              selected: _reason == r,
              onSelected: (_) => setState(() {
                _reason = r;
                if (r == 'Defective' || r == 'Damaged in transit') _restock = false;
              }),
            ),
        ]),
        if (_reason == 'Other')
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: TextField(
              key: const Key('return-reason-other'),
              controller: _otherReason,
              onChanged: (_) => setState(() {}),
              decoration: InputDecoration(labelText: tr('Describe the reason'), border: const OutlineInputBorder()),
            ),
          ),
        const Divider(height: 32),
        Text(tr('Refund by'), style: theme.textTheme.titleMedium),
        const SizedBox(height: 8),
        SegmentedButton<String>(
          segments: [
            ButtonSegment(value: 'cash', label: Text(tr('Cash')), icon: const Icon(Icons.payments_outlined)),
            ButtonSegment(value: 'card', label: Text(tr('Card')), icon: const Icon(Icons.credit_card)),
            const ButtonSegment(value: 'upi', label: Text('UPI'), icon: Icon(Icons.qr_code_2)),
            ButtonSegment(value: 'credit', label: Text(tr('Credit')), icon: const Icon(Icons.account_balance_wallet_outlined), enabled: _creditAllowed),
          ],
          selected: {_method},
          onSelectionChanged: (v) => setState(() => _method = v.first),
        ),
        if (!_creditAllowed)
          Padding(
            padding: const EdgeInsets.only(top: 4),
            child: Text(tr('Credit refunds only apply to sales with unpaid credit.'), style: theme.textTheme.bodySmall),
          ),
        if (_method == 'card' || _method == 'upi')
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: TextField(
              controller: _reference,
              decoration: InputDecoration(
                labelText: _method == 'upi' ? tr('UPI reference (optional)') : tr('Card refund reference (optional)'),
                border: const OutlineInputBorder(),
              ),
            ),
          ),
        SwitchListTile(
          key: const Key('return-restock'),
          contentPadding: EdgeInsets.zero,
          title: Text(tr('Put items back in stock')),
          subtitle: Text(tr('Turn off for damaged or unsellable items')),
          value: _restock,
          onChanged: (v) => setState(() => _restock = v),
        ),
        if (_needsApproval) ...[
          const Divider(height: 32),
          Text(tr('Manager approval'), style: theme.textTheme.titleMedium),
          const SizedBox(height: 4),
          Text(tr('A Branch Manager or Merchant Admin approves this return.'), style: theme.textTheme.bodySmall),
          const SizedBox(height: 8),
          TextField(
            key: const Key('return-approver-code'),
            controller: _approverCode,
            textCapitalization: TextCapitalization.characters,
            onChanged: (_) => setState(() {}),
            decoration: InputDecoration(labelText: tr('Approver employee code'), border: const OutlineInputBorder()),
          ),
          const SizedBox(height: 8),
          Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 320),
              child: ApprovalPinPad(onChanged: (pin) => setState(() => _pin = pin)),
            ),
          ),
        ],
      ],
    );
  }

  Widget _lineRow(ThemeData theme, ReturnableLine l) {
    final q = _qty[l.lineId] ?? 0;
    final max = _max(l);
    final returned = double.tryParse(l.returnedQuantity) ?? 0;
    return Card(
      margin: const EdgeInsets.symmetric(vertical: 4),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(12, 8, 4, 8),
        child: Row(children: [
          Expanded(
            child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              Text(l.productName, style: theme.textTheme.titleSmall),
              Text(
                [
                  l.sku,
                  'sold ${formatQty(l.soldQuantity)}',
                  if (returned > 0) 'returned ${formatQty(l.returnedQuantity)}',
                  tr('{0} each', [formatRupees(l.unitRefund)]),
                ].join(' · '),
                style: theme.textTheme.bodySmall,
              ),
            ]),
          ),
          IconButton(
            tooltip: tr('One less'),
            onPressed: q > 0 ? () => _setQty(l, q - 1) : null,
            icon: const Icon(Icons.remove_circle_outline),
          ),
          SizedBox(
            width: 40,
            child: Text(formatQty(q.toString()), key: Key('return-qty-${l.sku}'), textAlign: TextAlign.center, style: theme.textTheme.titleMedium),
          ),
          IconButton(
            key: Key('return-plus-${l.sku}'),
            tooltip: tr('One more'),
            // Fractional leftovers (weighed items) go back in one step.
            onPressed: q < max ? () => _setQty(l, q + 1 > max ? max : q + 1) : null,
            icon: const Icon(Icons.add_circle_outline),
          ),
        ]),
      ),
    );
  }
}
