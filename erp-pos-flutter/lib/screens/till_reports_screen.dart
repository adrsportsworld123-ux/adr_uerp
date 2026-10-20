import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/api_client.dart';
import '../l10n/l10n.dart';
import '../printing/printer_service.dart';
import '../state/session.dart';
import '../ui/format.dart';
import 'shift_screen.dart';

/// X and Z reports on the till (erp-core-go internal/sales shifts.go and
/// day_report.go; menu → X / Z reports). X: this till's shifts (or the
/// whole branch's) over chosen dates; open one for its X report, live while
/// it's open, and print it. Z: the branch's business day, every till; print
/// it, and close a day that has ended (reports.day_close), which freezes
/// it with the branch's next Z number. Online only.
class TillReportsScreen extends StatefulWidget {
  const TillReportsScreen({super.key});

  @override
  State<TillReportsScreen> createState() => _TillReportsScreenState();
}

String _ymd(DateTime d) => '${d.year}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';

String _when(String iso) {
  final t = PosClock.parse(iso);
  if (t == null) return '';
  return '${t.day.toString().padLeft(2, '0')}/${t.month.toString().padLeft(2, '0')} ${t.hour.toString().padLeft(2, '0')}:${t.minute.toString().padLeft(2, '0')}';
}

String _method(String m) => switch (m) {
      'upi' => 'UPI',
      'cash' => tr('Cash'),
      'card' => tr('Card'),
      'credit' => tr('On credit'),
      'bank_transfer' => tr('Bank transfer'),
      'cheque' => tr('Cheque'),
      'pos' => tr('Till'),
      'backoffice' => tr('Back office'),
      'online' => tr('Online'),
      _ => m,
    };

class _TillReportsScreenState extends State<TillReportsScreen> {
  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    return DefaultTabController(
      length: 2,
      child: Scaffold(
        appBar: AppBar(
          title: Text(tr('X / Z reports')),
          bottom: TabBar(tabs: [
            Tab(key: const Key('tab-x'), text: tr('X · shifts')),
            Tab(key: const Key('tab-z'), text: tr('Z · day')),
          ]),
        ),
        body: session.offlineMode
            ? Center(child: Padding(padding: const EdgeInsets.all(24), child: Text(tr('Offline. Reports need a connection.'), textAlign: TextAlign.center)))
            : const TabBarView(children: [_ShiftsTab(), _DayTab()]),
      ),
    );
  }
}

class _ShiftsTab extends StatefulWidget {
  const _ShiftsTab();

  @override
  State<_ShiftsTab> createState() => _ShiftsTabState();
}

class _ShiftsTabState extends State<_ShiftsTab> {
  DateTimeRange _range = DateTimeRange(start: PosClock.now().subtract(const Duration(days: 6)), end: PosClock.now());
  bool _thisTill = true;
  List<ShiftSummary>? _shifts;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _error = null);
    try {
      final list = await context.read<AppSession>().listShifts(thisTerminalOnly: _thisTill, from: _ymd(_range.start), to: _ymd(_range.end));
      if (mounted) setState(() => _shifts = list);
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } catch (_) {
      if (mounted) setState(() => _error = tr('Can\'t reach the server.'));
    }
  }

  Future<void> _pickDates() async {
    final r = await showDateRangePicker(context: context, firstDate: DateTime(2020), lastDate: PosClock.now(), initialDateRange: _range);
    if (r == null) return;
    setState(() => _range = r);
    await _load();
  }

  Future<void> _open(ShiftSummary s) async {
    final session = context.read<AppSession>();
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => ChangeNotifierProvider<AppSession>.value(value: session, child: _ShiftDetail(shiftId: s.shiftId)),
    ));
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final shifts = _shifts;
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(padding: const EdgeInsets.all(12), children: [
        Wrap(spacing: 8, runSpacing: 8, crossAxisAlignment: WrapCrossAlignment.center, children: [
          OutlinedButton.icon(
            key: const Key('x-dates'),
            onPressed: _pickDates,
            icon: const Icon(Icons.date_range),
            label: Text('${_ymd(_range.start)} – ${_ymd(_range.end)}'),
          ),
          FilterChip(
            key: const Key('x-this-till'),
            label: Text(tr('This till only')),
            selected: _thisTill,
            onSelected: (v) {
              setState(() => _thisTill = v);
              _load();
            },
          ),
        ]),
        const SizedBox(height: 8),
        if (_error != null) Text(_error!, style: TextStyle(color: theme.colorScheme.error)),
        if (shifts == null && _error == null) const Padding(padding: EdgeInsets.all(24), child: Center(child: CircularProgressIndicator())),
        if (shifts != null && shifts.isEmpty) Padding(padding: const EdgeInsets.all(24), child: Text(tr('No shifts opened in these dates.'), textAlign: TextAlign.center)),
        for (final s in shifts ?? const <ShiftSummary>[])
          Card(
            child: ListTile(
              key: Key('shift-${s.shiftId}'),
              leading: Icon(s.status == 'open' ? Icons.lock_open : Icons.lock_outline, color: s.status == 'open' ? theme.colorScheme.primary : null),
              title: Text(tr('{0} · shift {1}', [s.terminalName, s.shiftNumber])),
              subtitle: Text([
                'Opened ${_when(s.openedAt)} by ${s.openedBy}',
                if (s.closedAt != null) 'closed ${_when(s.closedAt!)}' else 'open',
              ].join(' · ')),
              trailing: Column(mainAxisAlignment: MainAxisAlignment.center, crossAxisAlignment: CrossAxisAlignment.end, children: [
                Text(s.netSales != null ? formatRupees(s.netSales!) : tr('Live'), style: theme.textTheme.titleSmall),
                if (s.variance != null && double.tryParse(s.variance!) != 0)
                  Text(tr('Variance {0}', [formatRupees(s.variance!)]), style: theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.error)),
              ]),
              onTap: () => _open(s),
            ),
          ),
      ]),
    );
  }
}

class _ShiftDetail extends StatefulWidget {
  final String shiftId;

  const _ShiftDetail({required this.shiftId});

  @override
  State<_ShiftDetail> createState() => _ShiftDetailState();
}

class _ShiftDetailState extends State<_ShiftDetail> {
  Shift? _shift;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final s = await context.read<AppSession>().loadShift(widget.shiftId);
      if (mounted) setState(() => _shift = s);
    } catch (e) {
      if (mounted) setState(() => _error = e is ApiException ? e.message : tr('Can\'t reach the server.'));
    }
  }

  @override
  Widget build(BuildContext context) {
    final s = _shift;
    return Scaffold(
      appBar: AppBar(
        title: Text(s == null ? tr('X report') : tr('X report · {0} shift {1}', [s.terminalName, s.shiftNumber])),
        actions: [
          if (s != null && s.isOpen) IconButton(onPressed: _load, icon: const Icon(Icons.refresh), tooltip: tr('Refresh')),
          if (s != null) IconButton(key: const Key('print-x-report'), onPressed: () => printShiftReport(context, s.shiftId), icon: const Icon(Icons.print), tooltip: tr('Print X report')),
        ],
      ),
      body: _error != null
          ? Center(child: Text(_error!))
          : s == null
              ? const Center(child: CircularProgressIndicator())
              : ListView(padding: const EdgeInsets.all(16), children: [
                  Text(tr('Opened {0} by {1}{2}', [_when(s.openedAt), s.openedBy, s.closedAt != null ? ' · ${tr('closed {0}', [_when(s.closedAt!)])}' : ''])),
                  const SizedBox(height: 12),
                  if (s.report != null) ShiftReportView(report: s.report!),
                ]),
    );
  }
}

class _DayTab extends StatefulWidget {
  const _DayTab();

  @override
  State<_DayTab> createState() => _DayTabState();
}

class _DayTabState extends State<_DayTab> {
  DateTime _date = PosClock.now();
  DayReport? _rep;
  String? _error;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  bool get _ended => _ymd(_date).compareTo(_ymd(PosClock.now())) < 0;

  Future<void> _load() async {
    setState(() => _error = null);
    try {
      final r = await context.read<AppSession>().loadDayReport(_ymd(_date));
      if (mounted) setState(() => _rep = r);
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } catch (_) {
      if (mounted) setState(() => _error = tr('Can\'t reach the server.'));
    }
  }

  Future<void> _pickDate() async {
    final d = await showDatePicker(context: context, firstDate: DateTime(2020), lastDate: PosClock.now(), initialDate: _date);
    if (d == null) return;
    setState(() {
      _date = d;
      _rep = null;
    });
    await _load();
  }

  Future<void> _print() async {
    final session = context.read<AppSession>();
    final messenger = ScaffoldMessenger.of(context);
    final ok = await session.printDayReport(_ymd(_date), PrinterService());
    messenger.showSnackBar(SnackBar(content: Text(ok ? tr('Z report sent to the printer') : (session.lastError ?? tr('Could not print')))));
  }

  Future<void> _closeDay() async {
    final rep = _rep!;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(tr('Close {0}?', [rep.date])),
        content: Text(tr('The day\'s Z report is frozen with the branch\'s next Z number: {0} bills, {1} net sales. Sales that sync for this day afterwards show as a difference.', [rep.orderCount, formatRupees(rep.netSales)])),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx, false), child: Text(tr('Cancel'))),
          FilledButton(key: const Key('confirm-close-day'), onPressed: () => Navigator.pop(ctx, true), child: Text(tr('Close day'))),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    setState(() => _busy = true);
    final messenger = ScaffoldMessenger.of(context);
    try {
      final r = await context.read<AppSession>().closeDay(rep.date);
      if (mounted) setState(() => _rep = r);
      messenger.showSnackBar(SnackBar(content: Text(tr('Day closed: Z no. {0}', [r.zNumber]))));
    } on ApiException catch (e) {
      messenger.showSnackBar(SnackBar(
        content: Text(switch (e.code) {
          'FORBIDDEN' => tr('Closing the day needs a manager (reports.day_close).'),
          _ => e.message,
        }),
      ));
    } catch (_) {
      messenger.showSnackBar(SnackBar(content: Text(tr('Can\'t reach the server.'))));
    }
    if (mounted) setState(() => _busy = false);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final session = context.watch<AppSession>();
    final r = _rep;
    Widget row(String label, String value, {bool strong = false, Key? key}) => Padding(
          padding: const EdgeInsets.symmetric(vertical: 2),
          child: Row(children: [
            Expanded(child: Text(label, style: strong ? theme.textTheme.titleSmall : null)),
            Text(value, key: key, style: (strong ? theme.textTheme.titleSmall : theme.textTheme.bodyMedium)?.copyWith(fontFeatures: const [FontFeature.tabularFigures()])),
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
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(padding: const EdgeInsets.all(12), children: [
        Wrap(spacing: 8, runSpacing: 8, children: [
          OutlinedButton.icon(key: const Key('z-date'), onPressed: _pickDate, icon: const Icon(Icons.event), label: Text(_ymd(_date))),
          OutlinedButton.icon(key: const Key('print-z-report'), onPressed: r == null ? null : _print, icon: const Icon(Icons.print), label: Text(tr('Print Z report'))),
          if (r != null && !r.isClosed && session.can('reports.day_close'))
            FilledButton.icon(
              key: const Key('close-day'),
              onPressed: _busy || !_ended ? null : _closeDay,
              icon: const Icon(Icons.lock_outline),
              label: Text(_ended ? tr('Close day') : tr('Close day (after today)')),
            ),
        ]),
        const SizedBox(height: 8),
        if (_error != null) Text(_error!, style: TextStyle(color: theme.colorScheme.error)),
        if (r == null && _error == null) const Padding(padding: EdgeInsets.all(24), child: Center(child: CircularProgressIndicator())),
        if (r != null) ...[
          Text('${r.branchName} · ${r.date}', style: theme.textTheme.titleMedium),
          Text(
            r.isClosed ? tr('Z no. {0} · closed by {1}', [r.zNumber, r.closedBy]) : tr('Day not closed: figures so far'),
            key: const Key('z-status'),
            style: theme.textTheme.bodySmall?.copyWith(color: r.isClosed ? null : theme.colorScheme.tertiary),
          ),
          if (r.changedSinceClose != null)
            Padding(
              padding: const EdgeInsets.only(top: 4),
              child: Text(
                tr('Changed since close: {0} bills, {1}', [r.changedSinceClose!['order_count'], formatRupees('${r.changedSinceClose!['net_sales']}')]),
                style: TextStyle(color: theme.colorScheme.error),
              ),
            ),
          if (r.openShifts > 0)
            Padding(
              padding: const EdgeInsets.only(top: 4),
              child: Text(tr('{0} shift(s) still open: close them before closing the day.', [r.openShifts]), style: TextStyle(color: theme.colorScheme.error)),
            ),
          const SizedBox(height: 12),
          section(tr('Sales'), [
            row(tr('Bills'), '${r.orderCount}'),
            row(tr('Subtotal'), formatRupees(r.subtotal)),
            row(tr('Discounts'), '−${formatRupees(r.discountTotal)}'),
            row(tr('Tax'), formatRupees(r.taxTotal)),
            if (r.taxRegime == 'gst_split')
              for (final h in [('CGST', r.cgstTotal), ('SGST', r.sgstTotal), ('IGST', r.igstTotal)])
                if ((double.tryParse(h.$2) ?? 0) != 0) row('· ${h.$1}', formatRupees(h.$2)),
            if ((double.tryParse(r.roundOffTotal) ?? 0) != 0) row(tr('Round off'), formatRupees(r.roundOffTotal), key: const Key('z-round-off')),
            row(tr('Net sales'), formatRupees(r.netSales), strong: true, key: const Key('z-net-sales')),
            row(tr('Average bill'), formatRupees(r.averageBill)),
            if (r.firstOrder != null) Text(tr('Bills {0} to {1}', [r.firstOrder, r.lastOrder]), style: theme.textTheme.bodySmall),
          ]),
          section(tr('Tenders'), [
            for (final e in r.byMethod.entries) row(_method(e.key), formatRupees(e.value)),
            if (r.byMethod.isEmpty) Text(tr('No payments')),
            const Divider(),
            for (final e in r.byChannel.entries) row(_method(e.key), formatRupees(e.value)),
          ]),
          section(tr('Tax by rate'), [
            for (final t in r.taxRates) row(tr('GST {0}% on {1}', [t['rate'], formatRupees('${t['taxable_value']}')]), formatRupees('${t['tax']}')),
            if (r.taxRates.isEmpty) Text(tr('No taxable sales')),
          ]),
          section(tr('Returns & voids'), [
            row(tr('Returns'), '${r.returnCount}'),
            row(tr('Refunded'), formatRupees(r.refundTotal)),
            row(tr('Voided sales'), '${r.voidedCount} (${formatRupees(r.voidedTotal)})'),
            row(tr('Released carts'), '${r.releasedCount}'),
          ]),
          section(tr('Cash'), [
            row(tr('Cash sales'), formatRupees(r.cash['cash_sales'] ?? '0')),
            row(tr('Cash refunds'), '−${formatRupees(r.cash['cash_refunds'] ?? '0')}'),
            row(tr('Float in'), formatRupees(r.cash['float_in'] ?? '0')),
            row(tr('Drops'), '−${formatRupees(r.cash['drops'] ?? '0')}'),
            row(tr('Paid in'), formatRupees(r.cash['paid_in'] ?? '0')),
            row(tr('Paid out'), '−${formatRupees(r.cash['paid_out'] ?? '0')}'),
            row(tr('Net cash'), formatRupees(r.cash['net_cash'] ?? '0'), strong: true),
            row(tr('Counted (closed shifts)'), formatRupees(r.cash['counted'] ?? '0')),
            row(tr('Variance'), formatRupees(r.cash['variance'] ?? '0'), strong: true),
          ]),
          section(tr('Shifts'), [
            for (final s in r.shifts)
              row('${s['terminal_name']} #${s['shift_number']}${s['status'] == 'open' ? ' (open)' : ''}',
                  '${formatRupees('${s['net_sales']}')}${s['variance'] != null ? ' · ${formatRupees('${s['variance']}')}' : ''}'),
            if (r.shifts.isEmpty) Text(tr('No till shifts this day')),
          ]),
          section(tr('Cashiers'), [
            for (final c in r.cashiers) row('${c['cashier']} (${c['orders']})', formatRupees('${c['net_sales']}')),
            if (r.cashiers.isEmpty) Text(tr('No sales')),
          ]),
        ],
      ]),
    );
  }
}
