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
    final label = shift == null ? tr('No shift') : tr('Shift {0}', [shift.shiftNumber]);
    final fg = warn ? theme.pos.warn : theme.colorScheme.onSurfaceVariant;
    return Semantics(
      button: true,
      label: tr('{0}. Open shift and cash drawer.', [label]),
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

/// The cashier's check-in state at a glance; tapping opens the attendance
/// screen to check in or out. Shown as a warning until they check in.
class _AttendanceChip extends StatelessWidget {
  final VoidCallback onTap;
  final bool compact;

  const _AttendanceChip({required this.onTap, required this.compact});

  @override
  Widget build(BuildContext context) {
    final session = context.watch<AppSession>();
    final theme = Theme.of(context);
    final me = session.attendance;
    final offline = session.offlineMode;
    final checkedIn = me?.checkedIn ?? false;
    final warn = me != null && !checkedIn && me.allowPos;
    final since = checkedIn ? (me!.openSince ?? me.open!.clockIn) : null;
    final label = me == null
        ? tr('Check in')
        : checkedIn
            ? (since == null ? tr('Checked in') : tr('In · {0}', [TimeOfDay.fromDateTime(since).format(context)]))
            : tr('Check in');
    final fg = offline
        ? theme.disabledColor
        : checkedIn
            ? theme.pos.pay
            : warn
                ? theme.pos.warn
                : theme.colorScheme.onSurfaceVariant;
    return Semantics(
      button: true,
      label: checkedIn ? '$label. Open attendance to check out.' : tr('Not checked in. Open attendance to check in.'),
      child: InkWell(
        key: const Key('attendance-chip'),
        borderRadius: BorderRadius.circular(999),
        onTap: offline ? null : onTap,
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
          decoration: BoxDecoration(
            color: offline ? null : (checkedIn ? theme.pos.paySoft : (warn ? theme.pos.warnSoft : null)),
            border: Border.all(color: offline ? theme.colorScheme.outline : (checkedIn ? theme.pos.pay : (warn ? theme.pos.warn : theme.colorScheme.outline))),
            borderRadius: BorderRadius.circular(999),
          ),
          child: Row(mainAxisSize: MainAxisSize.min, children: [
            Icon(checkedIn ? Icons.how_to_reg : Icons.badge_outlined, size: 16, color: fg),
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

enum _MenuAction { heldOrders, salesHistory, tillReports, productSearch, productLookup, syncNow, syncQueue, printer, signOut }

class _StatusBar extends StatelessWidget implements PreferredSizeWidget {
  final bool busy;
  final VoidCallback onSync;
  final VoidCallback onShift;
  final VoidCallback onAttendance;
  final ValueChanged<_MenuAction> onMenu;

  const _StatusBar({required this.busy, required this.onSync, required this.onShift, required this.onAttendance, required this.onMenu});

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
        ? (pos.warn, pos.warnSoft, Icons.cloud_off, pending > 0 ? tr('Offline · {0} queued', [pending]) : tr('Offline'))
        : pending > 0
            ? (pos.warn, pos.warnSoft, Icons.sync, tr('{0} to sync', [pending]))
            : (pos.pay, pos.paySoft, Icons.cloud_done_outlined, compact ? tr('Online') : tr('Online · synced'));

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
              // On a phone the badge gives its room to the terminal name.
              if (!compact) ...[
                Container(
                  width: 32,
                  height: 32,
                  alignment: Alignment.center,
                  decoration: BoxDecoration(color: theme.colorScheme.primary, borderRadius: BorderRadius.circular(8)),
                  child: Text('ADR', style: TextStyle(color: theme.colorScheme.onPrimary, fontSize: 11, fontWeight: FontWeight.w700)),
                ),
                const SizedBox(width: 10),
              ],
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
                label: tr('Sync status: {0}. Tap to sync now.', [label]),
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
              _AttendanceChip(onTap: onAttendance, compact: compact),
              const SizedBox(width: 8),
              _ShiftChip(onTap: onShift, compact: compact),
              const SizedBox(width: 8),
              if (!compact)
                Padding(
                  padding: const EdgeInsets.only(right: 4),
                  child: Text(session.roles.isNotEmpty ? session.roles.first : tr('Signed in'), style: theme.textTheme.bodySmall),
                ),
              PopupMenuButton<_MenuAction>(
                tooltip: tr('More'),
                icon: const Icon(Icons.more_vert),
                onSelected: onMenu,
                itemBuilder: (_) => [
                  PopupMenuItem(
                    value: _MenuAction.heldOrders,
                    enabled: !offline,
                    child: ListTile(leading: const Icon(Icons.pause_circle_outline), title: Text(tr('Held orders')), trailing: const _KeyHint('F9')),
                  ),
                  PopupMenuItem(
                    value: _MenuAction.salesHistory,
                    child: ListTile(leading: const Icon(Icons.receipt_long_outlined), title: Text(tr('Sales history & returns')), trailing: const _KeyHint('F6')),
                  ),
                  PopupMenuItem(
                    value: _MenuAction.tillReports,
                    enabled: !offline,
                    child: ListTile(leading: const Icon(Icons.summarize_outlined), title: Text(tr('X / Z reports'))),
                  ),
                  PopupMenuItem(
                    value: _MenuAction.productSearch,
                    enabled: !offline,
                    child: ListTile(leading: const Icon(Icons.manage_search), title: Text(tr('Full product search'))),
                  ),
                  PopupMenuItem(
                    value: _MenuAction.productLookup,
                    enabled: !offline,
                    child: ListTile(leading: const Icon(Icons.sell_outlined), title: Text(tr('Product lookup · price, stock, labels'))),
                  ),
                  PopupMenuItem(value: _MenuAction.syncNow, child: ListTile(leading: const Icon(Icons.sync), title: Text(tr('Sync now')))),
                  PopupMenuItem(
                    value: _MenuAction.syncQueue,
                    child: ListTile(
                      leading: const Icon(Icons.pending_actions_outlined),
                      title: Text(tr('Sync queue')),
                      trailing: pending > 0 ? Text('$pending') : null,
                    ),
                  ),
                  PopupMenuItem(value: _MenuAction.printer, child: ListTile(leading: const Icon(Icons.print_outlined), title: Text(tr('Printer settings')))),
                  const PopupMenuDivider(),
                  PopupMenuItem(value: _MenuAction.signOut, child: ListTile(leading: const Icon(Icons.logout), title: Text(tr('Sign out')))),
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
