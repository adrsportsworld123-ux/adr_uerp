import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/api_client.dart';
import '../local/offline_store.dart';
import '../state/session.dart';
import '../ui/format.dart';

/// The sync queue (Phase 13): what this till sold offline and where each
/// sale stands. Waiting, failed (with the server's reason; it retries on
/// every sync), synced, and needs review: accepted, but it sold more than
/// the branch had in stock (NEGATIVE_STOCK, phase0_1_design.md §3.5). A
/// sale that already happened is never rejected; the back office resolves
/// it under Inventory → Stock Review.
class SyncQueueScreen extends StatefulWidget {
  const SyncQueueScreen({super.key});

  @override
  State<SyncQueueScreen> createState() => _SyncQueueScreenState();
}

class _SyncQueueScreenState extends State<SyncQueueScreen> {
  List<SyncQueueItem> _items = [];
  List<StockReviewItem> _review = [];
  bool _loading = true;
  bool _syncing = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final session = context.read<AppSession>();
    final items = await session.syncQueue();
    final review = await session.openReviewItems();
    if (!mounted) return;
    setState(() {
      _items = items;
      _review = review;
      _loading = false;
    });
  }

  Future<void> _syncNow() async {
    final session = context.read<AppSession>();
    setState(() => _syncing = true);
    try {
      await session.syncNow();
    } catch (_) {}
    await _load();
    if (!mounted) return;
    setState(() => _syncing = false);
    final waiting = _items.where((i) => !i.synced).length;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
      content: Text(session.offlineMode
          ? 'Still offline. Sales are safe on this device.'
          : waiting == 0
              ? 'Everything is synced'
              : '$waiting sale(s) still waiting'),
    ));
  }

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    final theme = Theme.of(context);
    final waiting = _items.where((i) => !i.synced).toList();
    final failed = waiting.where((i) => i.syncError != null).length;
    final synced = _items.where((i) => i.synced).toList();
    final flaggedHere = synced.where((i) => i.needsReview).length;

    Widget chip(String label, int n, Color fg, Color bg, Key key) => Container(
          key: key,
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
          decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
          child: Text('$label · $n', style: TextStyle(color: fg, fontWeight: FontWeight.w600)),
        );

    return Scaffold(
      appBar: AppBar(title: const Text('Sync queue')),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : RefreshIndicator(
              onRefresh: _load,
              child: ListView(padding: const EdgeInsets.all(16), children: [
                Wrap(spacing: 8, runSpacing: 8, crossAxisAlignment: WrapCrossAlignment.center, children: [
                  chip('Waiting', waiting.length - failed, theme.colorScheme.onSecondaryContainer, theme.colorScheme.secondaryContainer,
                      const Key('count-waiting')),
                  chip('Failed', failed, theme.colorScheme.onErrorContainer, theme.colorScheme.errorContainer, const Key('count-failed')),
                  chip('Needs review', session.offlineMode ? flaggedHere : _review.length, theme.colorScheme.onTertiaryContainer,
                      theme.colorScheme.tertiaryContainer, const Key('count-review')),
                  FilledButton.icon(
                    key: const Key('sync-now'),
                    onPressed: _syncing ? null : _syncNow,
                    icon: _syncing
                        ? const SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2))
                        : const Icon(Icons.sync),
                    label: Text(failed > 0 ? 'Retry now' : 'Sync now'),
                  ),
                ]),
                if (session.offlineMode)
                  Padding(
                    padding: const EdgeInsets.only(top: 12),
                    child: Text('Offline. Sales sync by themselves when the connection is back.', style: theme.textTheme.bodySmall),
                  ),
                const SizedBox(height: 16),
                _heading(theme, 'Waiting to sync', waiting.length),
                if (waiting.isEmpty) _empty(theme, 'Nothing waiting.'),
                for (final i in waiting) _saleTile(theme, i),
                if (_review.isNotEmpty) ...[
                  const SizedBox(height: 16),
                  _heading(theme, 'Needs review (sold more than was in stock)', _review.length),
                  Text('Nothing to do at the till. A manager checks the stock and resolves these under Inventory → Stock Review.',
                      style: theme.textTheme.bodySmall),
                  const SizedBox(height: 4),
                  for (final r in _review)
                    Card(
                      key: Key('review-${r.itemId}'),
                      margin: const EdgeInsets.symmetric(vertical: 3),
                      child: ListTile(
                        leading: Icon(Icons.inventory_2_outlined, color: theme.colorScheme.tertiary),
                        title: Text(r.productName),
                        subtitle: Text('${r.sku} · sold ${formatQty(r.quantity)} on ${r.orderNumber} · stock now ${formatQty(r.onHandNow)}'),
                      ),
                    ),
                ],
                const SizedBox(height: 16),
                _heading(theme, 'Synced recently', synced.length),
                if (synced.isEmpty) _empty(theme, 'No offline sales yet.'),
                for (final i in synced) _saleTile(theme, i),
              ]),
            ),
    );
  }

  Widget _heading(ThemeData theme, String text, int n) => Padding(
        padding: const EdgeInsets.only(bottom: 4),
        child: Text('$text ($n)', style: theme.textTheme.titleSmall),
      );

  Widget _empty(ThemeData theme, String text) => Padding(
        padding: const EdgeInsets.symmetric(vertical: 8),
        child: Text(text, style: theme.textTheme.bodySmall),
      );

  Widget _saleTile(ThemeData theme, SyncQueueItem i) {
    final (IconData icon, Color color, String status) = switch (i.state) {
      'failed' => (Icons.sync_problem, theme.colorScheme.error, 'Failed: ${i.syncError}. Retries on the next sync.'),
      'queued' => (Icons.cloud_upload_outlined, theme.colorScheme.onSurfaceVariant, 'Waiting to sync'),
      'needs_review' => (Icons.flag_outlined, theme.colorScheme.tertiary, 'Synced · needs review: sold more than was in stock'),
      _ => (Icons.cloud_done_outlined, theme.colorScheme.primary, 'Synced${i.syncedAt == null ? '' : ' at ${_hm(i.syncedAt!)}'}'),
    };
    return Card(
      key: Key('sync-${i.id}'),
      margin: const EdgeInsets.symmetric(vertical: 3),
      child: ListTile(
        leading: Icon(icon, color: color),
        title: Text(i.orderNumber),
        subtitle: Text('${formatQty(i.itemCount.toString())} item(s) · sold ${_hm(i.createdAt)}\n$status'),
        isThreeLine: true,
        trailing: Text(formatRupeesNum(i.grandTotal), style: theme.textTheme.titleSmall),
      ),
    );
  }

  static String _hm(String iso) {
    final t = DateTime.tryParse(iso)?.toLocal();
    if (t == null) return '';
    final now = DateTime.now();
    final hm = '${t.hour.toString().padLeft(2, '0')}:${t.minute.toString().padLeft(2, '0')}';
    return t.year == now.year && t.month == now.month && t.day == now.day ? hm : '${t.day}/${t.month} $hm';
  }
}
