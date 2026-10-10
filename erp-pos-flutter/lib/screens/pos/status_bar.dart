part of '../pos_screen.dart';

// ---------------------------------------------------------------------
// Status bar (design system §06, pin 2): replaces the old app bar whose
// six icon actions crowded out the sync state. Terminal and branch on the
// left, sync state as a pill that is always visible, cashier and the
// secondary actions in a menu on the right.
// ---------------------------------------------------------------------

/// Phase 13: the till's shift at a glance; tapping opens the shift screen.
/// Shown as a warning when the merchant requires shifts and none is open.
class _ShiftChip extends StatelessWidget {
  final VoidCallback onTap;
  final bool compact;

  const _ShiftChip({required this.onTap, required this.compact});

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    final theme = Theme.of(context);
    final shift = session.currentShift;
    final warn = shift == null && session.shiftsRequired;
    final label = shift == null ? 'No shift' : 'Shift ${shift.shiftNumber}';
    final fg = warn ? theme.pos.warn : theme.colorScheme.onSurfaceVariant;
    return Semantics(
      button: true,
      label: '$label. Open shift and cash drawer.',
      child: InkWell(
        key: const Key('shift-chip'),
        borderRadius: BorderRadius.circular(999),
        onTap: onTap,
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
          decoration: BoxDecoration(
            color: warn ? theme.pos.warnSoft : null,
            border: Border.all(color: warn ? theme.pos.warn : theme.colorScheme.outline),
            borderRadius: BorderRadius.circular(999),
          ),
          child: Row(mainAxisSize: MainAxisSize.min, children: [
            Icon(shift == null ? Icons.lock_outline : Icons.point_of_sale, size: 16, color: fg),
            if (!compact) ...[
              const SizedBox(width: 6),
              Text(label, style: TextStyle(color: fg, fontWeight: FontWeight.w600, fontSize: 13)),
            ],
          ]),
        ),
      ),
    );
  }
}

enum _MenuAction { heldOrders, salesHistory, productSearch, productLookup, syncNow, syncQueue, printer, signOut }

class _StatusBar extends StatelessWidget implements PreferredSizeWidget {
  final bool busy;
  final VoidCallback onSync;
  final VoidCallback onShift;
  final ValueChanged<_MenuAction> onMenu;

  const _StatusBar({required this.busy, required this.onSync, required this.onShift, required this.onMenu});

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
              _ShiftChip(onTap: onShift, compact: compact),
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
                  PopupMenuItem(
                    value: _MenuAction.productLookup,
                    enabled: !offline,
                    child: const ListTile(leading: Icon(Icons.sell_outlined), title: Text('Product lookup · price, stock, labels')),
                  ),
                  const PopupMenuItem(value: _MenuAction.syncNow, child: ListTile(leading: Icon(Icons.sync), title: Text('Sync now'))),
                  PopupMenuItem(
                    value: _MenuAction.syncQueue,
                    child: ListTile(
                      leading: const Icon(Icons.pending_actions_outlined),
                      title: const Text('Sync queue'),
                      trailing: pending > 0 ? Text('$pending') : null,
                    ),
                  ),
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
