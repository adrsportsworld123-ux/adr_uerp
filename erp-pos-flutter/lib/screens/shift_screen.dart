import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../api/api_client.dart';
import '../l10n/l10n.dart';
import '../printing/printer_service.dart';
import '../state/session.dart';
import '../ui/approval_pin_pad.dart';
import '../ui/format.dart';

/// The till's shift (Phase 13, erp-core-go internal/sales/shifts.go):
/// open with a float, see the live X report, move cash in and out, and
/// close with a counted drawer, which makes the X report final. The close
/// is recorded as the shift's cash reconciliation, so the back office sees
/// it alongside the daily counts. The X report prints on the receipt
/// printer; the day's Z report (every till) is under menu → X / Z reports
/// (till_reports_screen.dart).
///
/// Prints the shift's X report on the receipt printer, with a snackbar
/// saying how it went.
Future<void> printShiftReport(BuildContext context, String shiftId) async {
  final session = context.read<AppSession>();
  final messenger = ScaffoldMessenger.of(context);
  final ok = await session.printShiftReport(shiftId, PrinterService());
  messenger.showSnackBar(SnackBar(content: Text(ok ? tr('X report sent to the printer') : (session.lastError ?? tr('Could not print')))));
}

class ShiftScreen extends StatefulWidget {
  const ShiftScreen({super.key});

  @override
  State<ShiftScreen> createState() => _ShiftScreenState();
}

class _ShiftScreenState extends State<ShiftScreen> {
  bool _loading = true;
  bool _busy = false;
  String? _error;
  final _float = TextEditingController();
  late final String _openKey = context.read<AppSession>().newIdempotencyKey();

  @override
  void initState() {
    super.initState();
    _refresh();
  }

  @override
  void dispose() {
    _float.dispose();
    super.dispose();
  }

  Future<void> _refresh() async {
    setState(() => _loading = true);
    await context.read<AppSession>().refreshShift();
    if (mounted) setState(() => _loading = false);
  }

  Future<void> _open() async {
    final amount = double.tryParse(_float.text.trim().isEmpty ? '0' : _float.text.trim());
    if (amount == null || amount < 0) {
      setState(() => _error = tr('Enter the cash in the drawer, or 0'));
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    final session = context.read<AppSession>();
    try {
      await session.openShift(amount, _openKey);
    } on ApiException catch (e) {
      _error = switch (e.code) {
        'SHIFT_ALREADY_OPEN' => tr('This till already has an open shift.'),
        'DAY_ALREADY_RECONCILED' => tr('Today\'s cash for this branch was already counted as a daily count, so a shift can\'t open today.'),
        _ => e.message,
      };
      await session.refreshShift();
    } catch (_) {
      _error = tr('Can\'t reach the server. Shifts need a connection.');
    }
    if (mounted) setState(() => _busy = false);
  }

  Widget _scoped(Widget child) => ChangeNotifierProvider<AppSession>.value(value: context.read<AppSession>(), child: child);

  Future<void> _cashMovement() async {
    await showDialog<bool>(context: context, builder: (_) => _scoped(const CashMovementDialog()));
  }

  Future<void> _close() async {
    final closed = await Navigator.of(context).push<Shift>(MaterialPageRoute(builder: (_) => _scoped(const CloseShiftScreen())));
    if (closed == null || !mounted) return;
    await Navigator.of(context).pushReplacement(MaterialPageRoute(
      builder: (ctx) => Scaffold(
        appBar: AppBar(title: Text(tr('Shift closed · shift {0}', [closed.shiftNumber]))),
        body: ListView(padding: const EdgeInsets.all(16), children: [
          if (closed.report != null) ShiftReportView(report: closed.report!),
          const SizedBox(height: 16),
          OutlinedButton.icon(
            key: const Key('print-x-report'),
            onPressed: () => printShiftReport(ctx, closed.shiftId),
            icon: const Icon(Icons.print),
            label: Text(tr('Print X report')),
          ),
          const SizedBox(height: 8),
          FilledButton(onPressed: () => Navigator.of(ctx).pop(), child: Text(tr('Done'))),
        ]),
      ),
    ));
  }

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    final shift = session.currentShift;
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(
        title: Text(tr('Shift · {0}', [session.terminalName])),
        actions: [if (shift != null) IconButton(onPressed: _refresh, icon: const Icon(Icons.refresh), tooltip: tr('Refresh X report'))],
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : session.offlineMode
              ? Center(
                  child: Padding(
                    padding: const EdgeInsets.all(24),
                    child: Text(tr('Offline. Opening, closing and cash in/out need a connection; sales continue and sync later.'),
                        textAlign: TextAlign.center),
                  ),
                )
              : ListView(
                  padding: const EdgeInsets.all(16),
                  children: [
                    if (shift == null) ...[
                      Text(tr('No shift is open on this till'), style: theme.textTheme.titleMedium),
                      const SizedBox(height: 4),
                      Text(
                        session.shiftsRequired ? tr('Open a shift before taking payment.') : tr('Count the cash you are starting with.'),
                        style: theme.textTheme.bodySmall,
                      ),
                      const SizedBox(height: 16),
                      TextField(
                        key: const Key('opening-float'),
                        controller: _float,
                        autofocus: true,
                        keyboardType: const TextInputType.numberWithOptions(decimal: true),
                        inputFormatters: [FilteringTextInputFormatter.allow(RegExp(r'^\d*\.?\d{0,2}'))],
                        decoration: InputDecoration(labelText: tr('Opening float'), prefixText: '${PosCurrency.symbol} ', border: const OutlineInputBorder()),
                        onSubmitted: (_) => _open(),
                      ),
                      const SizedBox(height: 8),
                      Wrap(spacing: 8, children: [
                        for (final v in const [0, 1000, 2000, 5000])
                          ActionChip(label: Text(formatRupeesNum(v.toDouble())), onPressed: () => setState(() => _float.text = '$v')),
                      ]),
                      const SizedBox(height: 16),
                      FilledButton.icon(
                        key: const Key('open-shift'),
                        onPressed: _busy ? null : _open,
                        icon: const Icon(Icons.lock_open),
                        label: Text(tr('Open shift')),
                      ),
                    ] else ...[
                      Text(tr('Shift {0} · opened by {1} at {2}', [shift.shiftNumber, shift.openedBy, _hm(shift.openedAt)]), style: theme.textTheme.titleMedium),
                      const SizedBox(height: 12),
                      Wrap(spacing: 8, runSpacing: 8, children: [
                        OutlinedButton.icon(
                          key: const Key('cash-movement'),
                          onPressed: _cashMovement,
                          icon: const Icon(Icons.swap_vert),
                          label: Text(tr('Cash in / out')),
                        ),
                        OutlinedButton.icon(
                          key: const Key('print-x-report'),
                          onPressed: () => printShiftReport(context, shift.shiftId),
                          icon: const Icon(Icons.print),
                          label: Text(tr('Print X report')),
                        ),
                        FilledButton.icon(
                          key: const Key('close-shift'),
                          onPressed: _close,
                          icon: const Icon(Icons.lock_outline),
                          label: Text(tr('Close shift')),
                        ),
                      ]),
                      const SizedBox(height: 16),
                      if (shift.report != null) ShiftReportView(report: shift.report!),
                    ],
                    if (_error != null)
                      Padding(
                        padding: const EdgeInsets.only(top: 12),
                        child: Text(_error!, style: TextStyle(color: theme.colorScheme.error)),
                      ),
                  ],
                ),
    );
  }
}

String _hm(String iso) {
  final t = PosClock.parse(iso);
  if (t == null) return '';
  return '${t.hour.toString().padLeft(2, '0')}:${t.minute.toString().padLeft(2, '0')}';
}

String _methodLabel(String m) => switch (m) { 'upi' => 'UPI', 'cash' => tr('Cash'), 'card' => tr('Card'), 'credit' => tr('Credit'), _ => m };

String _kindLabel(String k) =>
    switch (k) { 'float_in' => tr('Float in'), 'drop' => tr('Drop to safe'), 'paid_in' => tr('Paid in'), 'paid_out' => tr('Paid out'), _ => k };

/// A shift's X report: sales and tenders, returns, and the drawer's cash
/// from the opening float to the expected (and, once the shift is closed,
/// counted) amount. The server marks a closed shift's report kind "Z" (its
/// frozen close); on screen it's the shift's final X report, the Z report
/// being the branch's day.
class ShiftReportView extends StatelessWidget {
  final ShiftReport report;

  const ShiftReportView({super.key, required this.report});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final c = report.cash;
    Widget row(String label, String? value, {bool strong = false, String sign = '', Color? color, Key? key}) => Padding(
          padding: const EdgeInsets.symmetric(vertical: 3),
          child: Row(children: [
            Expanded(child: Text(label, style: strong ? theme.textTheme.titleSmall : null)),
            Text('$sign${formatRupees(value ?? '0')}',
                key: key,
                style: (strong ? theme.textTheme.titleSmall : theme.textTheme.bodyMedium)
                    ?.copyWith(color: color, fontFeatures: const [FontFeature.tabularFigures()])),
          ]),
        );
    Widget section(String title, List<Widget> children) => Card(
          margin: const EdgeInsets.only(bottom: 12),
          child: Padding(
            padding: const EdgeInsets.all(12),
            child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
              Text(title, style: theme.textTheme.titleSmall?.copyWith(color: theme.colorScheme.primary)),
              const SizedBox(height: 6),
              ...children,
            ]),
          ),
        );
    final close = report.close;
    final variance = double.tryParse(close?['variance'] as String? ?? '0') ?? 0;
    return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      Text(report.kind == 'Z' ? tr('X report (final)') : tr('X report (so far)'), key: Key('report-${report.kind}'), style: theme.textTheme.labelLarge),
      const SizedBox(height: 8),
      section(tr('Sales'), [
        Text('${report.orderCount == 1 ? tr('1 sale') : tr('{0} sales', [report.orderCount])}${report.voidedCount > 0 ? ' · ${tr('{0} voided', [report.voidedCount])}' : ''}'),
        if ((double.tryParse(report.roundOffTotal) ?? 0) != 0) row(tr('Round off'), report.roundOffTotal),
        row(tr('Net sales'), report.netSales, strong: true),
        for (final e in report.byMethod.entries) row(_methodLabel(e.key), e.value),
        if (report.returnCount > 0) row(report.returnCount == 1 ? tr('Refunded (1 return)') : tr('Refunded ({0} returns)', [report.returnCount]), report.refundTotal, sign: '−'),
      ]),
      section(tr('Cash drawer'), [
        row(tr('Opening float'), c['opening_float']),
        row(tr('Cash sales'), c['cash_sales'], sign: '+'),
        if (c['cash_refunds'] != '0.00') row(tr('Cash refunds'), c['cash_refunds'], sign: '−'),
        if (c['float_in'] != '0.00') row(tr('Float in'), c['float_in'], sign: '+'),
        if (c['drops'] != '0.00') row(tr('Drops to safe'), c['drops'], sign: '−'),
        if (c['paid_in'] != '0.00') row(tr('Paid in'), c['paid_in'], sign: '+'),
        if (c['paid_out'] != '0.00') row(tr('Paid out'), c['paid_out'], sign: '−'),
        const Divider(),
        row(tr('Expected in drawer'), c['expected'], strong: true, key: const Key('expected-cash')),
        if (close != null) ...[
          row(tr('Counted'), close['counted_total'] as String?),
          row(
            variance == 0 ? tr('Difference') : (variance > 0 ? tr('Over') : tr('Short')),
            close['variance'] as String?,
            strong: true,
            color: variance == 0 ? null : theme.colorScheme.error,
            key: const Key('variance'),
          ),
          if ((close['reason'] as String? ?? '').isNotEmpty) Text(tr('Reason: {0}', [close['reason']]), style: theme.textTheme.bodySmall),
          if (close['approved_by'] != null) Text(tr('Approved by {0}', [close['approved_by']]), style: theme.textTheme.bodySmall),
        ],
      ]),
      if (report.movements.isNotEmpty)
        section(tr('Cash in / out'), [
          for (final m in report.movements)
            ListTile(
              dense: true,
              contentPadding: EdgeInsets.zero,
              title: Text('${_kindLabel(m.kind)} · ${m.reason}'),
              subtitle: Text([_hm(m.createdAt), m.performedBy, if (m.approvedBy != null) 'approved by ${m.approvedBy}'].join(' · ')),
              trailing: Text(formatRupees(m.amount)),
            ),
        ]),
    ]);
  }
}

/// Cash in or out of the drawer during a shift. A paid-out (an expense
/// paid from the drawer) needs a manager's code and PIN. The dialog does
/// the request itself so a failure keeps it open with the same key.
class CashMovementDialog extends StatefulWidget {
  const CashMovementDialog({super.key});

  @override
  State<CashMovementDialog> createState() => _CashMovementDialogState();
}

class _CashMovementDialogState extends State<CashMovementDialog> {
  String _kind = 'drop';
  final _amount = TextEditingController();
  final _reason = TextEditingController();
  final _approver = TextEditingController();
  String _pin = '';
  bool _busy = false;
  String? _error;
  late String _key = context.read<AppSession>().newIdempotencyKey();

  @override
  void dispose() {
    _amount.dispose();
    _reason.dispose();
    _approver.dispose();
    super.dispose();
  }

  bool get _needsReason => _kind == 'paid_in' || _kind == 'paid_out';
  bool get _needsApproval => _kind == 'paid_out';
  double? get _value => double.tryParse(_amount.text.trim());

  bool get _canSave =>
      !_busy &&
      (_value ?? 0) > 0 &&
      (!_needsReason || _reason.text.trim().isNotEmpty) &&
      (!_needsApproval || (_approver.text.trim().isNotEmpty && _pin.length >= 4));

  Future<void> _save() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await context.read<AppSession>().addCashMovement(
            kind: _kind,
            amount: _value!,
            reason: _reason.text.trim(),
            approverCode: _needsApproval ? _approver.text.trim() : null,
            approverPin: _needsApproval ? _pin : null,
            idempotencyKey: _key,
          );
      if (mounted) Navigator.pop(context, true);
    } on ApiException catch (e) {
      setState(() {
        _busy = false;
        _error = switch (e.code) {
          'CASH_MOVEMENT_NOT_APPROVED' => tr('Approval failed. Check the manager\'s employee code and PIN.'),
          'INSUFFICIENT_CASH' => e.message,
          'SHIFT_CLOSED' => tr('This shift is already closed.'),
          _ => e.message,
        };
      });
    } catch (_) {
      setState(() {
        _busy = false;
        _error = tr('Can\'t reach the server. Nothing was recorded.');
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return AlertDialog(
      title: Text(tr('Cash in / out')),
      content: SizedBox(
        width: 400,
        child: SingleChildScrollView(
          child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.stretch, children: [
            SegmentedButton<String>(
              segments: [
                ButtonSegment(value: 'float_in', label: Text(tr('Float in'))),
                ButtonSegment(value: 'drop', label: Text(tr('Drop'))),
                ButtonSegment(value: 'paid_in', label: Text(tr('Paid in'))),
                ButtonSegment(value: 'paid_out', label: Text(tr('Paid out'))),
              ],
              selected: {_kind},
              // A new kind is a different movement: a new key.
              onSelectionChanged: (v) => setState(() {
                _kind = v.first;
                _key = context.read<AppSession>().newIdempotencyKey();
              }),
            ),
            const SizedBox(height: 6),
            Text(
              switch (_kind) {
                'float_in' => tr('Change brought from the safe into the drawer.'),
                'drop' => tr('Cash taken from the drawer to the safe.'),
                'paid_in' => tr('Money received that isn\'t a sale.'),
                _ => tr('An expense paid from the drawer. Needs a manager.'),
              },
              style: theme.textTheme.bodySmall,
            ),
            const SizedBox(height: 12),
            TextField(
              key: const Key('movement-amount'),
              controller: _amount,
              autofocus: true,
              keyboardType: const TextInputType.numberWithOptions(decimal: true),
              inputFormatters: [FilteringTextInputFormatter.allow(RegExp(r'^\d*\.?\d{0,2}'))],
              onChanged: (_) => setState(() {}),
              decoration: InputDecoration(labelText: tr('Amount'), prefixText: '${PosCurrency.symbol} ', border: const OutlineInputBorder()),
            ),
            const SizedBox(height: 8),
            TextField(
              key: const Key('movement-reason'),
              controller: _reason,
              onChanged: (_) => setState(() {}),
              decoration: InputDecoration(labelText: _needsReason ? tr('What for') : tr('Note (optional)'), border: const OutlineInputBorder()),
            ),
            if (_needsApproval) ...[
              const SizedBox(height: 8),
              TextField(
                key: const Key('movement-approver'),
                controller: _approver,
                textCapitalization: TextCapitalization.characters,
                onChanged: (_) => setState(() {}),
                decoration: InputDecoration(labelText: tr('Approver employee code'), border: const OutlineInputBorder(), isDense: true),
              ),
              const SizedBox(height: 8),
              ApprovalPinPad(onChanged: (p) => setState(() => _pin = p)),
            ],
            if (_error != null)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Text(_error!, key: const Key('movement-error'), style: TextStyle(color: theme.colorScheme.error)),
              ),
          ]),
        ),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: Text(tr('Cancel'))),
        FilledButton(key: const Key('movement-save'), onPressed: _canSave ? _save : null, child: Text(tr('Record'))),
      ],
    );
  }
}

/// Counting the drawer to close the shift: notes and coins, the counted
/// total against the expected cash as it's typed, and, only when they
/// differ, a reason and a manager's approval (the same rule as the daily
/// cash reconciliation). Pops the closed shift.
class CloseShiftScreen extends StatefulWidget {
  const CloseShiftScreen({super.key});

  @override
  State<CloseShiftScreen> createState() => _CloseShiftScreenState();
}

/// A denomination's test key: "2000", or "0.25" for a coin below one.
String _denominationKey(double d) => d == d.roundToDouble() ? '${d.toInt()}' : '$d';

class _CloseShiftScreenState extends State<CloseShiftScreen> {
  // The notes and coins the merchant counts (Localization, migrations/064;
  // the currency's standard set unless the merchant set its own).
  late final List<double> _denominations;
  late final Map<double, TextEditingController> _counts;
  final _reason = TextEditingController();
  final _approver = TextEditingController();
  String _pin = '';
  bool _busy = false;
  bool _syncing = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _denominations = context.read<AppSession>().localization.denominations;
    _counts = {for (final d in _denominations) d: TextEditingController()};
    _prepare();
  }

  /// Push any offline sales first so their cash is in the expected figure,
  /// then take a fresh X report.
  Future<void> _prepare() async {
    final session = context.read<AppSession>();
    try {
      if (session.pendingSyncCount > 0) await session.syncNow();
    } catch (_) {}
    await session.refreshShift();
    if (mounted) setState(() => _syncing = false);
  }

  @override
  void dispose() {
    for (final c in _counts.values) {
      c.dispose();
    }
    _reason.dispose();
    _approver.dispose();
    super.dispose();
  }

  int _count(double d) => int.tryParse(_counts[d]!.text.trim()) ?? 0;
  double get _counted => _denominations.fold(0.0, (sum, d) => sum + d * _count(d));
  double get _expected => double.tryParse(context.read<AppSession>().currentShift?.report?.expectedCash ?? '') ?? 0;
  int get _varianceP => (_counted * 100).round() - (_expected * 100).round();

  bool get _canClose =>
      !_busy && !_syncing && (_varianceP == 0 || (_reason.text.trim().isNotEmpty && _approver.text.trim().isNotEmpty && _pin.length >= 4));

  Future<void> _submit() async {
    final session = context.read<AppSession>();
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final closed = await session.closeShift(
        {for (final d in _denominations) d: _count(d)},
        reason: _varianceP == 0 ? null : _reason.text.trim(),
        approverCode: _varianceP == 0 ? null : _approver.text.trim(),
        approverPin: _varianceP == 0 ? null : _pin,
      );
      if (mounted) Navigator.pop(context, closed);
    } on ApiException catch (e) {
      // The expected cash can move while counting (a sale finishing on
      // another session, an offline sale syncing): take a fresh figure.
      await session.refreshShift();
      if (!mounted) return;
      setState(() {
        _busy = false;
        _error = switch (e.code) {
          'VARIANCE_NOT_AUTHORIZED' => tr('The count doesn\'t match. A Branch Manager or Merchant Admin must approve with their code and PIN.'),
          'INVALID_REQUEST' => tr('The count doesn\'t match the expected cash (it may have just changed). Give a reason and a manager\'s approval.'),
          _ => e.message,
        };
      });
    } catch (_) {
      setState(() {
        _busy = false;
        _error = tr('Can\'t reach the server. The shift is still open.');
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    final theme = Theme.of(context);
    final variance = _varianceP / 100;
    return Scaffold(
      appBar: AppBar(title: Text(tr('Close shift · count the drawer'))),
      body: _syncing
          ? const Center(child: CircularProgressIndicator())
          : ListView(padding: const EdgeInsets.all(16), children: [
              if (session.pendingSyncCount > 0)
                Container(
                  key: const Key('unsynced-warning'),
                  margin: const EdgeInsets.only(bottom: 12),
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(color: theme.colorScheme.errorContainer, borderRadius: BorderRadius.circular(8)),
                  child: Text(
                    tr('{0} offline sale(s) haven\'t synced. Their cash is in the drawer but not in the expected figure, so the count will look over. Sync first if you can.', [session.pendingSyncCount]),
                    style: TextStyle(color: theme.colorScheme.onErrorContainer),
                  ),
                ),
              for (final d in _denominations)
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 3),
                  child: Row(children: [
                    SizedBox(width: 90, child: Text(formatRupeesNum(d), style: theme.textTheme.titleSmall)),
                    const Text('×  '),
                    SizedBox(
                      width: 90,
                      child: TextField(
                        key: Key('count-${_denominationKey(d)}'),
                        controller: _counts[d],
                        keyboardType: TextInputType.number,
                        inputFormatters: [FilteringTextInputFormatter.digitsOnly],
                        textAlign: TextAlign.center,
                        onChanged: (_) => setState(() {}),
                        decoration: const InputDecoration(isDense: true, border: OutlineInputBorder(), hintText: '0'),
                      ),
                    ),
                    const Spacer(),
                    Text(formatRupeesNum(d * _count(d))),
                  ]),
                ),
              const Divider(height: 24),
              _total(theme, tr('Counted'), _counted, key: const Key('counted-total')),
              _total(theme, tr('Expected'), _expected),
              _total(theme, variance == 0 ? tr('Difference') : (variance > 0 ? tr('Over') : tr('Short')), variance.abs(),
                  color: variance == 0 ? null : theme.colorScheme.error, key: const Key('close-variance')),
              if (_varianceP != 0) ...[
                const SizedBox(height: 12),
                TextField(
                  key: const Key('close-reason'),
                  controller: _reason,
                  onChanged: (_) => setState(() {}),
                  decoration: InputDecoration(labelText: tr('Why doesn\'t it match?'), border: const OutlineInputBorder()),
                ),
                const SizedBox(height: 8),
                TextField(
                  key: const Key('close-approver'),
                  controller: _approver,
                  textCapitalization: TextCapitalization.characters,
                  onChanged: (_) => setState(() {}),
                  decoration: InputDecoration(labelText: tr('Approver employee code'), border: const OutlineInputBorder(), isDense: true),
                ),
                const SizedBox(height: 8),
                Center(
                  child: ConstrainedBox(
                    constraints: const BoxConstraints(maxWidth: 320),
                    child: ApprovalPinPad(onChanged: (p) => setState(() => _pin = p)),
                  ),
                ),
              ],
              if (_error != null)
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Text(_error!, key: const Key('close-error'), style: TextStyle(color: theme.colorScheme.error)),
                ),
              const SizedBox(height: 16),
              FilledButton.icon(
                key: const Key('confirm-close'),
                onPressed: _canClose ? _submit : null,
                icon: const Icon(Icons.lock_outline),
                label: Text(tr('Close shift')),
              ),
            ]),
    );
  }

  Widget _total(ThemeData theme, String label, double v, {Color? color, Key? key}) => Padding(
        padding: const EdgeInsets.symmetric(vertical: 2),
        child: Row(children: [
          Expanded(child: Text(label, style: theme.textTheme.titleSmall)),
          Text(formatRupeesNum(v), key: key, style: theme.textTheme.titleSmall?.copyWith(color: color)),
        ]),
      );
}
