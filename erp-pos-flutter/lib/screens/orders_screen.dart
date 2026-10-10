import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/api_client.dart';
import '../local/offline_store.dart';
import '../state/session.dart';
import '../ui/format.dart';
import 'receipt_screen.dart';
import 'return_screen.dart';

/// Sales history for this till or the whole branch (Phase 13, design UI-3):
/// a day summary, filters, the server's sales, and above them the sales
/// this device made offline that haven't synced yet ("queued"). Opening a
/// sale shows its receipt, where it can be reprinted or returned.
class OrdersScreen extends StatefulWidget {
  const OrdersScreen({super.key});

  @override
  State<OrdersScreen> createState() => _OrdersScreenState();
}

enum _Period { today, yesterday, week }

class _OrdersScreenState extends State<OrdersScreen> {
  bool _thisTill = true;
  _Period _period = _Period.today;
  String _status = '';
  final _search = TextEditingController();

  List<QueuedSale> _queued = [];
  List<SalesHistoryOrder> _orders = [];
  int _total = 0;
  int _page = 1;
  SalesSummaryData? _summary;
  bool _loading = true;
  bool _loadingMore = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _search.dispose();
    super.dispose();
  }

  static String _ymd(DateTime d) =>
      '${d.year.toString().padLeft(4, '0')}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';

  (String, String) get _range {
    final now = DateTime.now();
    final today = DateTime(now.year, now.month, now.day);
    return switch (_period) {
      _Period.today => (_ymd(today), _ymd(today)),
      _Period.yesterday => (_ymd(today.subtract(const Duration(days: 1))), _ymd(today.subtract(const Duration(days: 1)))),
      _Period.week => (_ymd(today.subtract(const Duration(days: 6))), _ymd(today)),
    };
  }

  Future<void> _load() async {
    final session = context.read<AppSession>();
    setState(() {
      _loading = true;
      _error = null;
    });
    final queued = await session.queuedSales();
    if (!mounted) return;
    setState(() => _queued = queued);
    if (session.offlineMode) {
      setState(() {
        _orders = [];
        _summary = null;
        _total = 0;
        _loading = false;
      });
      return;
    }
    final (from, to) = _range;
    try {
      final results = await Future.wait([
        session.salesHistory(thisTerminalOnly: _thisTill, status: _status, query: _search.text.trim(), from: from, to: to),
        session.salesSummary(thisTerminalOnly: _thisTill, from: from, to: to),
      ]);
      if (!mounted) return;
      final page = results[0] as SalesHistoryPage;
      setState(() {
        _orders = page.orders;
        _total = page.total;
        _page = 1;
        _summary = results[1] as SalesSummaryData;
        _loading = false;
      });
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.message;
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _error = 'Can\'t reach the server. Only this device\'s queued sales are shown.';
        _loading = false;
      });
    }
  }

  Future<void> _loadMore() async {
    final session = context.read<AppSession>();
    final (from, to) = _range;
    setState(() => _loadingMore = true);
    try {
      final page = await session.salesHistory(
          thisTerminalOnly: _thisTill, status: _status, query: _search.text.trim(), from: from, to: to, page: _page + 1);
      if (!mounted) return;
      setState(() {
        _orders = [..._orders, ...page.orders];
        _total = page.total;
        _page += 1;
      });
    } catch (_) {
      _snack('Could not load more sales');
    } finally {
      if (mounted) setState(() => _loadingMore = false);
    }
  }

  void _snack(String m) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(m)));
  }

  Widget _scoped(Widget child) => ChangeNotifierProvider<AppSession>.value(value: context.read<AppSession>(), child: child);

  Future<void> _open(SalesHistoryOrder o) async {
    final session = context.read<AppSession>();
    final ReceiptData receipt;
    try {
      receipt = await session.receiptFor(o.orderId);
    } catch (_) {
      _snack('Could not load this sale');
      return;
    }
    if (!mounted) return;
    final canReturn = o.status == 'finalized' || o.status == 'partially_refunded';
    var changed = false;
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (routeContext) => _scoped(ReceiptScreen(
        receipt: receipt,
        orderId: o.orderId,
        onReturn: canReturn
            ? () async {
                final done = await Navigator.of(routeContext).push<bool>(
                  MaterialPageRoute(builder: (_) => _scoped(ReturnScreen(orderId: o.orderId, orderNumber: o.orderNumber))),
                );
                if (done == true && routeContext.mounted) {
                  changed = true;
                  Navigator.of(routeContext).pop();
                }
              }
            : null,
      )),
    ));
    if (changed) _load();
  }

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(
        title: const Text('Sales history'),
        actions: [IconButton(onPressed: _load, icon: const Icon(Icons.refresh), tooltip: 'Refresh')],
      ),
      body: RefreshIndicator(
        onRefresh: _load,
        child: ListView(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
          children: [
            _filters(theme),
            const SizedBox(height: 12),
            if (session.offlineMode)
              _banner(theme, Icons.cloud_off, 'Offline. Only sales on this device are shown; history and returns need a connection.')
            else if (_error != null)
              _banner(theme, Icons.error_outline, _error!),
            if (_summary != null) _summaryTiles(theme, _summary!),
            if (_queued.isNotEmpty) ...[
              const SizedBox(height: 16),
              Text('Waiting to sync (${_queued.length})', style: theme.textTheme.titleSmall),
              const SizedBox(height: 4),
              for (final q in _queued) _queuedTile(theme, q),
            ],
            const SizedBox(height: 16),
            if (_loading)
              const Padding(padding: EdgeInsets.all(32), child: Center(child: CircularProgressIndicator()))
            else if (!session.offlineMode && _error == null) ...[
              Text(_total == 0 ? 'No sales match' : '$_total sale${_total == 1 ? '' : 's'}', style: theme.textTheme.titleSmall),
              const SizedBox(height: 4),
              for (final o in _orders) _orderTile(theme, o),
              if (_orders.length < _total)
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Center(
                    child: OutlinedButton(
                      onPressed: _loadingMore ? null : _loadMore,
                      child: Text(_loadingMore ? 'Loading…' : 'Load more'),
                    ),
                  ),
                ),
            ],
          ],
        ),
      ),
    );
  }

  Widget _filters(ThemeData theme) {
    return Wrap(
      spacing: 8,
      runSpacing: 8,
      crossAxisAlignment: WrapCrossAlignment.center,
      children: [
        SegmentedButton<bool>(
          segments: const [
            ButtonSegment(value: true, label: Text('This till')),
            ButtonSegment(value: false, label: Text('Whole branch')),
          ],
          selected: {_thisTill},
          onSelectionChanged: (v) {
            setState(() => _thisTill = v.first);
            _load();
          },
        ),
        for (final (p, label) in const [(_Period.today, 'Today'), (_Period.yesterday, 'Yesterday'), (_Period.week, 'Last 7 days')])
          ChoiceChip(
            label: Text(label),
            selected: _period == p,
            onSelected: (_) {
              setState(() => _period = p);
              _load();
            },
          ),
        DropdownButton<String>(
          value: _status,
          underline: const SizedBox.shrink(),
          items: const [
            DropdownMenuItem(value: '', child: Text('All statuses')),
            DropdownMenuItem(value: 'finalized', child: Text('Completed')),
            DropdownMenuItem(value: 'partially_refunded', child: Text('Partly refunded')),
            DropdownMenuItem(value: 'refunded', child: Text('Refunded')),
            DropdownMenuItem(value: 'voided', child: Text('Voided')),
          ],
          onChanged: (v) {
            setState(() => _status = v ?? '');
            _load();
          },
        ),
        SizedBox(
          width: 280,
          child: TextField(
            key: const Key('history-search'),
            controller: _search,
            textInputAction: TextInputAction.search,
            onSubmitted: (_) => _load(),
            decoration: InputDecoration(
              isDense: true,
              prefixIcon: const Icon(Icons.search),
              hintText: 'Order no., customer or phone',
              border: const OutlineInputBorder(),
              suffixIcon: _search.text.isEmpty
                  ? null
                  : IconButton(
                      tooltip: 'Clear search',
                      icon: const Icon(Icons.clear),
                      onPressed: () {
                        _search.clear();
                        _load();
                      },
                    ),
            ),
          ),
        ),
      ],
    );
  }

  Widget _banner(ThemeData theme, IconData icon, String text) => Padding(
        padding: const EdgeInsets.only(bottom: 8),
        child: Material(
          color: theme.colorScheme.secondaryContainer,
          borderRadius: BorderRadius.circular(8),
          child: Padding(
            padding: const EdgeInsets.all(12),
            child: Row(children: [
              Icon(icon, color: theme.colorScheme.onSecondaryContainer),
              const SizedBox(width: 12),
              Expanded(child: Text(text, style: TextStyle(color: theme.colorScheme.onSecondaryContainer))),
            ]),
          ),
        ),
      );

  Widget _summaryTiles(ThemeData theme, SalesSummaryData s) {
    Widget tile(String label, String value, {Key? key}) => Container(
          key: key,
          width: 160,
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            border: Border.all(color: theme.colorScheme.outlineVariant),
            borderRadius: BorderRadius.circular(10),
          ),
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Text(label, style: theme.textTheme.labelMedium?.copyWith(color: theme.colorScheme.onSurfaceVariant)),
            const SizedBox(height: 4),
            Text(value, style: theme.textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w600)),
          ]),
        );
    return Wrap(spacing: 8, runSpacing: 8, children: [
      tile('Sales', '${s.orderCount}', key: const Key('summary-count')),
      tile('Net sales', formatRupees(s.netSales), key: const Key('summary-net')),
      for (final m in const ['cash', 'card', 'upi', 'credit'])
        if (s.byMethod.containsKey(m)) tile(m == 'upi' ? 'UPI' : '${m[0].toUpperCase()}${m.substring(1)}', formatRupees(s.byMethod[m]!)),
      if (s.refundedCount > 0 || s.voidedCount > 0) tile('Refunded · voided', '${s.refundedCount} · ${s.voidedCount}'),
    ]);
  }

  Widget _queuedTile(ThemeData theme, QueuedSale q) {
    final failed = q.syncError != null;
    return Card(
      margin: const EdgeInsets.symmetric(vertical: 3),
      child: ListTile(
        leading: Icon(failed ? Icons.sync_problem : Icons.cloud_upload_outlined, color: failed ? theme.colorScheme.error : null),
        title: Text(q.orderNumber),
        subtitle: Text(failed
            ? 'Sync failed: ${q.syncError}'
            : 'Queued · ${formatQty(q.itemCount.toString())} item(s) · ${_time(q.createdAt)}'),
        trailing: Text(formatRupeesNum(q.grandTotal), style: theme.textTheme.titleSmall),
        onTap: () => _snack('This sale reaches the server when the till is back online. It can be reprinted or returned after that.'),
      ),
    );
  }

  Widget _orderTile(ThemeData theme, SalesHistoryOrder o) {
    final scheme = theme.colorScheme;
    final (bg, fg) = switch (o.status) {
      'voided' => (scheme.errorContainer, scheme.onErrorContainer),
      'refunded' || 'partially_refunded' => (scheme.tertiaryContainer, scheme.onTertiaryContainer),
      _ => (scheme.secondaryContainer, scheme.onSecondaryContainer),
    };
    return Card(
      margin: const EdgeInsets.symmetric(vertical: 3),
      child: ListTile(
        key: Key('order-${o.orderNumber}'),
        title: Row(children: [
          Flexible(child: Text(o.orderNumber, overflow: TextOverflow.ellipsis)),
          const SizedBox(width: 8),
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
            decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
            child: Text(statusLabel(o.status), style: theme.textTheme.labelSmall?.copyWith(color: fg)),
          ),
        ]),
        subtitle: Text([
          _time(o.finalizedAt),
          '${formatQty(o.itemCount)} item(s)',
          if (o.paymentMethods.isNotEmpty) o.paymentMethods.map((m) => m.toUpperCase()).join(' + '),
          if (o.customerName != null) o.customerName!,
          if (!_thisTill) o.terminalName,
          o.cashierName,
        ].join(' · ')),
        trailing: Text(
          formatRupees(o.grandTotal),
          style: theme.textTheme.titleSmall?.copyWith(
            decoration: o.status == 'voided' ? TextDecoration.lineThrough : null,
          ),
        ),
        onTap: () => _open(o),
      ),
    );
  }

  static String _time(String? iso) {
    if (iso == null) return '';
    final t = DateTime.tryParse(iso)?.toLocal();
    if (t == null) return '';
    final now = DateTime.now();
    final hm = '${t.hour.toString().padLeft(2, '0')}:${t.minute.toString().padLeft(2, '0')}';
    if (t.year == now.year && t.month == now.month && t.day == now.day) return hm;
    return '${t.day}/${t.month} $hm';
  }
}
