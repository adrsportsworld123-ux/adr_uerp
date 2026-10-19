import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/api_client.dart';
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
  final t = DateTime.tryParse(iso)?.toLocal();
  if (t == null) return '';
  return '${t.day.toString().padLeft(2, '0')}/${t.month.toString().padLeft(2, '0')} ${t.hour.toString().padLeft(2, '0')}:${t.minute.toString().padLeft(2, '0')}';
}

String _method(String m) => switch (m) {
      'upi' => 'UPI',
      'cash' => 'Cash',
      'card' => 'Card',
      'credit' => 'On credit',
      'bank_transfer' => 'Bank transfer',
      'cheque' => 'Cheque',
      'pos' => 'Till',
      'backoffice' => 'Back office',
      'online' => 'Online',
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
          title: const Text('X / Z reports'),
          bottom: const TabBar(tabs: [
            Tab(key: Key('tab-x'), text: 'X · shifts'),
            Tab(key: Key('tab-z'), text: 'Z · day'),
          ]),
        ),
        body: session.offlineMode
            ? const Center(child: Padding(padding: EdgeInsets.all(24), child: Text('Offline. Reports need a connection.', textAlign: TextAlign.center)))
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
  DateTimeRange _range = DateTimeRange(start: DateTime.now().subtract(const Duration(days: 6)), end: DateTime.now());
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
      if (mounted) setState(() => _error = 'Can\'t reach the server.');
    }
  }

  Future<void> _pickDates() async {
    final r = await showDateRangePicker(context: context, firstDate: DateTime(2020), lastDate: DateTime.now(), initialDateRange: _range);
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
            label: const Text('This till only'),
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
        if (shifts != null && shifts.isEmpty) const Padding(padding: EdgeInsets.all(24), child: Text('No shifts opened in these dates.', textAlign: TextAlign.center)),
        for (final s in shifts ?? const <ShiftSummary>[])
          Card(
            child: ListTile(
              key: Key('shift-${s.shiftId}'),
              leading: Icon(s.status == 'open' ? Icons.lock_open : Icons.lock_outline, color: s.status == 'open' ? theme.colorScheme.primary : null),
              title: Text('${s.terminalName} · shift ${s.shiftNumber}'),
              subtitle: Text([
                'Opened ${_when(s.openedAt)} by ${s.openedBy}',
                if (s.closedAt != null) 'closed ${_when(s.closedAt!)}' else 'open',
              ].join(' · ')),
              trailing: Column(mainAxisAlignment: MainAxisAlignment.center, crossAxisAlignment: CrossAxisAlignment.end, children: [
                Text(s.netSales != null ? formatRupees(s.netSales!) : 'Live', style: theme.textTheme.titleSmall),
                if (s.variance != null && double.tryParse(s.variance!) != 0)
                  Text('Variance ${formatRupees(s.variance!)}', style: theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.error)),
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
      if (mounted) setState(() => _error = e is ApiException ? e.message : 'Can\'t reach the server.');
    }
  }

  @override
  Widget build(BuildContext context) {
    final s = _shift;
    return Scaffold(
      appBar: AppBar(
        title: Text(s == null ? 'X report' : 'X report · ${s.terminalName} shift ${s.shiftNumber}'),
        actions: [
          if (s != null && s.isOpen) IconButton(onPressed: _load, icon: const Icon(Icons.refresh), tooltip: 'Refresh'),
          if (s != null) IconButton(key: const Key('print-x-report'), onPressed: () => printShiftReport(context, s.shiftId), icon: const Icon(Icons.print), tooltip: 'Print X report'),
        ],
      ),
      body: _error != null
          ? Center(child: Text(_error!))
          : s == null
              ? const Center(child: CircularProgressIndicator())
              : ListView(padding: const EdgeInsets.all(16), children: [
                  Text('Opened ${_when(s.openedAt)} by ${s.openedBy}${s.closedAt != null ? ' · closed ${_when(s.closedAt!)}' : ''}'),
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
  DateTime _date = DateTime.now();
  DayReport? _rep;
  String? _error;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  bool get _ended => _ymd(_date).compareTo(_ymd(DateTime.now())) < 0;

  Future<void> _load() async {
    setState(() => _error = null);
    try {
      final r = await context.read<AppSession>().loadDayReport(_ymd(_date));
      if (mounted) setState(() => _rep = r);
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } catch (_) {
      if (mounted) setState(() => _error = 'Can\'t reach the server.');
    }
  }

  Future<void> _pickDate() async {
    final d = await showDatePicker(context: context, firstDate: DateTime(2020), lastDate: DateTime.now(), initialDate: _date);
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
    messenger.showSnackBar(SnackBar(content: Text(ok ? 'Z report sent to the printer' : (session.lastError ?? 'Could not print'))));
  }

  Future<void> _closeDay() async {
    final rep = _rep!;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('Close ${rep.date}?'),
        content: Text('The day\'s Z report is frozen with the branch\'s next Z number: ${rep.orderCount} bills, ${formatRupees(rep.netSales)} net sales. '
            'Sales that sync for this day afterwards show as a difference.'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('Cancel')),
          FilledButton(key: const Key('confirm-close-day'), onPressed: () => Navigator.pop(ctx, true), child: const Text('Close day')),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    setState(() => _busy = true);
    final messenger = ScaffoldMessenger.of(context);
    try {
      final r = await context.read<AppSession>().closeDay(rep.date);
      if (mounted) setState(() => _rep = r);
      messenger.showSnackBar(SnackBar(content: Text('Day closed: Z no. ${r.zNumber}')));
    } on ApiException catch (e) {
      messenger.showSnackBar(SnackBar(
        content: Text(switch (e.code) {
          'FORBIDDEN' => 'Closing the day needs a manager (reports.day_close).',
          _ => e.message,
        }),
      ));
    } catch (_) {
      messenger.showSnackBar(const SnackBar(content: Text('Can\'t reach the server.')));
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
          OutlinedButton.icon(key: const Key('print-z-report'), onPressed: r == null ? null : _print, icon: const Icon(Icons.print), label: const Text('Print Z report')),
          if (r != null && !r.isClosed && session.can('reports.day_close'))
            FilledButton.icon(
              key: const Key('close-day'),
              onPressed: _busy || !_ended ? null : _closeDay,
              icon: const Icon(Icons.lock_outline),
              label: Text(_ended ? 'Close day' : 'Close day (after today)'),
            ),
        ]),
        const SizedBox(height: 8),
        if (_error != null) Text(_error!, style: TextStyle(color: theme.colorScheme.error)),
        if (r == null && _error == null) const Padding(padding: EdgeInsets.all(24), child: Center(child: CircularProgressIndicator())),
        if (r != null) ...[
          Text('${r.branchName} · ${r.date}', style: theme.textTheme.titleMedium),
          Text(
            r.isClosed ? 'Z no. ${r.zNumber} · closed by ${r.closedBy}' : 'Day not closed: figures so far',
            key: const Key('z-status'),
            style: theme.textTheme.bodySmall?.copyWith(color: r.isClosed ? null : theme.colorScheme.tertiary),
          ),
          if (r.changedSinceClose != null)
            Padding(
              padding: const EdgeInsets.only(top: 4),
              child: Text(
                'Changed since close: ${r.changedSinceClose!['order_count']} bills, ${formatRupees('${r.changedSinceClose!['net_sales']}')}',
                style: TextStyle(color: theme.colorScheme.error),
              ),
            ),
          if (r.openShifts > 0)
            Padding(
              padding: const EdgeInsets.only(top: 4),
              child: Text('${r.openShifts} shift(s) still open: close them before closing the day.', style: TextStyle(color: theme.colorScheme.error)),
            ),
          const SizedBox(height: 12),
          section('Sales', [
            row('Bills', '${r.orderCount}'),
            row('Subtotal', formatRupees(r.subtotal)),
            row('Discounts', '−${formatRupees(r.discountTotal)}'),
            row('Tax', formatRupees(r.taxTotal)),
            row('Net sales', formatRupees(r.netSales), strong: true, key: const Key('z-net-sales')),
            row('Average bill', formatRupees(r.averageBill)),
            if (r.firstOrder != null) Text('Bills ${r.firstOrder} to ${r.lastOrder}', style: theme.textTheme.bodySmall),
          ]),
          section('Tenders', [
            for (final e in r.byMethod.entries) row(_method(e.key), formatRupees(e.value)),
            if (r.byMethod.isEmpty) const Text('No payments'),
            const Divider(),
            for (final e in r.byChannel.entries) row(_method(e.key), formatRupees(e.value)),
          ]),
          section('Tax by rate', [
            for (final t in r.taxRates) row('GST ${t['rate']}% on ${formatRupees('${t['taxable_value']}')}', formatRupees('${t['tax']}')),
            if (r.taxRates.isEmpty) const Text('No taxable sales'),
          ]),
          section('Returns & voids', [
            row('Returns', '${r.returnCount}'),
            row('Refunded', formatRupees(r.refundTotal)),
            row('Voided sales', '${r.voidedCount} (${formatRupees(r.voidedTotal)})'),
            row('Released carts', '${r.releasedCount}'),
          ]),
          section('Cash', [
            row('Cash sales', formatRupees(r.cash['cash_sales'] ?? '0')),
            row('Cash refunds', '−${formatRupees(r.cash['cash_refunds'] ?? '0')}'),
            row('Float in', formatRupees(r.cash['float_in'] ?? '0')),
            row('Drops', '−${formatRupees(r.cash['drops'] ?? '0')}'),
            row('Paid in', formatRupees(r.cash['paid_in'] ?? '0')),
            row('Paid out', '−${formatRupees(r.cash['paid_out'] ?? '0')}'),
            row('Net cash', formatRupees(r.cash['net_cash'] ?? '0'), strong: true),
            row('Counted (closed shifts)', formatRupees(r.cash['counted'] ?? '0')),
            row('Variance', formatRupees(r.cash['variance'] ?? '0'), strong: true),
          ]),
          section('Shifts', [
            for (final s in r.shifts)
              row('${s['terminal_name']} #${s['shift_number']}${s['status'] == 'open' ? ' (open)' : ''}',
                  '${formatRupees('${s['net_sales']}')}${s['variance'] != null ? ' · ${formatRupees('${s['variance']}')}' : ''}'),
            if (r.shifts.isEmpty) const Text('No till shifts this day'),
          ]),
          section('Cashiers', [
            for (final c in r.cashiers) row('${c['cashier']} (${c['orders']})', formatRupees('${c['net_sales']}')),
            if (r.cashiers.isEmpty) const Text('No sales'),
          ]),
        ],
      ]),
    );
  }
}
