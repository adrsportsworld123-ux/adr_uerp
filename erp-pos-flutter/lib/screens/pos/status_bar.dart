part of '../pos_screen.dart';

// ---------------------------------------------------------------------
// Status bar (design system §06, pin 2): replaces the old app bar whose
// six icon actions crowded out the sync state. Terminal and branch on the
// left, sync state as a pill that is always visible, cashier and the
// secondary actions in a menu on the right.
// ---------------------------------------------------------------------

enum _MenuAction { heldOrders, salesHistory, productSearch, syncNow, printer, signOut }

class _StatusBar extends StatelessWidget implements PreferredSizeWidget {
  final bool busy;
  final VoidCallback onSync;
  final ValueChanged<_MenuAction> onMenu;

  const _StatusBar({required this.busy, required this.onSync, required this.onMenu});

  @override
  Size get preferredSize => const Size.fromHeight(56);

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    final theme = Theme.of(context);
    final pos = theme.pos;
    final compact = MediaQuery.sizeOf(context).width < 600;

    final offline = session.offlineMode;
    final pending = session.pendingSyncCount;
    final (Color fg, Color bg, IconData icon, String label) = offline
        ? (pos.warn, pos.warnSoft, Icons.cloud_off, pending > 0 ? 'Offline · $pending queued' : 'Offline')
        : pending > 0
            ? (pos.warn, pos.warnSoft, Icons.sync, '$pending to sync')
            : (pos.pay, pos.paySoft, Icons.cloud_done_outlined, compact ? 'Online' : 'Online · synced');

    return Material(
      color: theme.colorScheme.surface,
      elevation: 0,
      child: SafeArea(
        bottom: false,
        child: Container(
          height: 56,
          padding: const EdgeInsets.symmetric(horizontal: 12),
          decoration: BoxDecoration(border: Border(bottom: BorderSide(color: theme.colorScheme.outline))),
          child: Row(
            children: [
              Container(
                width: 32,
                height: 32,
                alignment: Alignment.center,
                decoration: BoxDecoration(color: theme.colorScheme.primary, borderRadius: BorderRadius.circular(8)),
                child: Text('ADR', style: TextStyle(color: theme.colorScheme.onPrimary, fontSize: 11, fontWeight: FontWeight.w700)),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  mainAxisAlignment: MainAxisAlignment.center,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(session.terminalName, maxLines: 1, overflow: TextOverflow.ellipsis, style: theme.textTheme.titleSmall),
                    if (session.branchName.isNotEmpty && !compact)
                      Text(session.branchName,
                          maxLines: 1, overflow: TextOverflow.ellipsis, style: theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.onSurfaceVariant)),
                  ],
                ),
              ),
              Semantics(
                button: true,
                label: 'Sync status: $label. Tap to sync now.',
                child: InkWell(
                  key: const Key('sync-pill'),
                  borderRadius: BorderRadius.circular(999),
                  onTap: busy ? null : onSync,
                  child: Container(
                    padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
                    decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
                    child: Row(mainAxisSize: MainAxisSize.min, children: [
                      Icon(icon, size: 16, color: fg),
                      const SizedBox(width: 6),
                      Text(label, style: TextStyle(color: fg, fontWeight: FontWeight.w600, fontSize: 13)),
                    ]),
                  ),
                ),
              ),
              const SizedBox(width: 8),
              if (!compact)
                Padding(
                  padding: const EdgeInsets.only(right: 4),
                  child: Text(session.roles.isNotEmpty ? session.roles.first : 'Signed in', style: theme.textTheme.bodySmall),
                ),
              PopupMenuButton<_MenuAction>(
                tooltip: 'More',
                icon: const Icon(Icons.more_vert),
                onSelected: onMenu,
                itemBuilder: (_) => [
                  PopupMenuItem(
                    value: _MenuAction.heldOrders,
                    enabled: !offline,
                    child: const ListTile(leading: Icon(Icons.pause_circle_outline), title: Text('Held orders'), trailing: _KeyHint('F9')),
                  ),
                  const PopupMenuItem(
                    value: _MenuAction.salesHistory,
                    child: ListTile(leading: Icon(Icons.receipt_long_outlined), title: Text('Sales history & returns'), trailing: _KeyHint('F6')),
                  ),
                  PopupMenuItem(
                    value: _MenuAction.productSearch,
                    enabled: !offline,
                    child: const ListTile(leading: Icon(Icons.manage_search), title: Text('Full product search')),
                  ),
                  const PopupMenuItem(value: _MenuAction.syncNow, child: ListTile(leading: Icon(Icons.sync), title: Text('Sync now'))),
                  const PopupMenuItem(value: _MenuAction.printer, child: ListTile(leading: Icon(Icons.print_outlined), title: Text('Printer settings'))),
                  const PopupMenuDivider(),
                  const PopupMenuItem(value: _MenuAction.signOut, child: ListTile(leading: Icon(Icons.logout), title: Text('Sign out'))),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// A small keycap label, e.g. F12, shown next to the action it triggers.
class _KeyHint extends StatelessWidget {
  final String label;
  final Color? color;

  const _KeyHint(this.label, {this.color});

  @override
  Widget build(BuildContext context) {
    final c = color ?? Theme.of(context).colorScheme.onSurfaceVariant;
    return ExcludeSemantics(
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
        decoration: BoxDecoration(border: Border.all(color: c.withValues(alpha: 0.6)), borderRadius: BorderRadius.circular(4)),
        child: Text(label, style: kMonoStyle.copyWith(fontSize: 11, color: c)),
      ),
    );
  }
}
