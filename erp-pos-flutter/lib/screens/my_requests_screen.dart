import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../api/api_client.dart';
import '../l10n/l10n.dart';
import '../printing/document_saver.dart';
import '../state/session.dart';

/// Leave, permission, attendance corrections and payslips for the
/// signed-in employee (erp-core-go §3.78; menu → Attendance → "Leave,
/// permission & payslips"). Everything needs the server: a manager
/// approves on the web, and payslips appear once the month is finalized.
class MyRequestsScreen extends StatefulWidget {
  const MyRequestsScreen({super.key});

  @override
  State<MyRequestsScreen> createState() => _MyRequestsScreenState();
}

String ymd(DateTime d) => '${d.year}-${d.month.toString().padLeft(2, '0')}-${d.day.toString().padLeft(2, '0')}';
String hm(TimeOfDay t) => '${t.hour.toString().padLeft(2, '0')}:${t.minute.toString().padLeft(2, '0')}';

const _months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

class _MyRequestsScreenState extends State<MyRequestsScreen> {
  LeaveBalancesInfo? _balances;
  List<HrRequestInfo>? _requests;
  List<PayslipSummary>? _payslips;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final session = context.read<AppSession>();
    setState(() => _error = null);
    try {
      final b = await session.api.myLeaveBalances();
      final r = await session.api.myRequests();
      final p = session.userId == null ? <PayslipSummary>[] : await session.api.myPayslips(session.userId!);
      if (!mounted) return;
      setState(() {
        _balances = b;
        _requests = r;
        _payslips = p;
      });
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } catch (_) {
      if (mounted) setState(() => _error = tr('Leave and payslips need a connection to the server.'));
    }
  }

  void _snack(String text) => ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(text)));

  Future<void> _ask(Widget dialog) async {
    final session = context.read<AppSession>();
    final number = await showDialog<String>(
      context: context,
      builder: (_) => ChangeNotifierProvider<AppSession>.value(value: session, child: dialog),
    );
    if (number != null && mounted) {
      _snack(tr('Sent for approval ({0})', [number]));
      _load();
    }
  }

  Future<void> _cancel(HrRequestInfo r) async {
    try {
      await context.read<AppSession>().api.cancelRequest(r.kind, r.id, reason: 'cancelled at the till');
      if (!mounted) return;
      _snack(tr('{0} cancelled', [r.number]));
      _load();
    } on ApiException catch (e) {
      _snack(e.message);
    }
  }

  Future<void> _download(PayslipSummary p) async {
    try {
      final file = await context.read<AppSession>().api.payslipPdf(p.id);
      final path = await saveDocument(file.filename, file.bytes);
      if (mounted) _snack(tr('Saved to {0}', [path]));
    } on ApiException catch (e) {
      _snack(e.message);
    } catch (_) {
      _snack(tr('Could not save the payslip.'));
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return DefaultTabController(
      length: 2,
      child: Scaffold(
        appBar: AppBar(
          title: Text(tr('Leave, permission & payslips')),
          bottom: TabBar(tabs: [Tab(text: tr('Requests')), Tab(text: tr('Payslips'))]),
        ),
        body: _error != null
            ? Center(
                child: Column(mainAxisSize: MainAxisSize.min, children: [
                  Text(_error!, style: TextStyle(color: theme.colorScheme.error)),
                  const SizedBox(height: 12),
                  OutlinedButton(onPressed: _load, child: Text(tr('Try again'))),
                ]),
              )
            : _balances == null
                ? const Center(child: CircularProgressIndicator())
                : TabBarView(children: [_requestsTab(theme), _payslipsTab(theme)]),
      ),
    );
  }

  Widget _requestsTab(ThemeData theme) {
    final b = _balances!;
    String n(num? v) => v == null ? tr('no limit') : (v % 1 == 0 ? v.toInt().toString() : v.toString());
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Wrap(spacing: 8, runSpacing: 8, children: [
          FilledButton.icon(
            key: const Key('ask-leave'),
            onPressed: () => _ask(const LeaveRequestDialog()),
            icon: const Icon(Icons.event_busy),
            label: Text(tr('Ask for leave')),
          ),
          OutlinedButton.icon(
            key: const Key('ask-permission'),
            onPressed: () => _ask(PermissionRequestDialog(maxMinutes: b.permissionMaxMinutes)),
            icon: const Icon(Icons.schedule),
            label: Text(tr('Permission')),
          ),
          OutlinedButton.icon(
            key: const Key('ask-correction'),
            onPressed: () => _ask(const CorrectionDialog()),
            icon: const Icon(Icons.edit_calendar),
            label: Text(tr('Correct attendance')),
          ),
        ]),
        const SizedBox(height: 16),
        Text(tr('My leave'), style: theme.textTheme.titleMedium),
        for (final x in b.balances)
          ListTile(
            dense: true,
            title: Text(x.name),
            subtitle: Text(x.paid ? tr('{0} taken · {1} waiting', [n(x.taken), n(x.pending)]) : tr('Unpaid · {0} taken', [n(x.taken)])),
            trailing: x.paid ? Text(tr('{0} left', [n(x.available)]), style: theme.textTheme.titleSmall) : null,
          ),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16),
          child: Text(
            tr('Permission this month: {0} of {1} free used ({2} of {3} min)',
                    [b.permissionUsedCount, b.permissionFreeCount, b.permissionUsedFreeMinutes, b.permissionFreeMinutes]) +
                (b.permissionUnpaidMinutes > 0 ? tr(', {0} min unpaid', [b.permissionUnpaidMinutes]) : ''),
            key: const Key('permission-usage'),
            style: theme.textTheme.bodySmall,
          ),
        ),
        const SizedBox(height: 16),
        Text(tr('My requests'), style: theme.textTheme.titleMedium),
        if (_requests!.isEmpty) Padding(padding: const EdgeInsets.all(16), child: Text(tr('You haven\'t asked for anything yet.'))),
        for (final r in _requests!)
          ListTile(
            key: Key('request-${r.number}'),
            title: Text('${tr(r.what)} · ${r.when}'),
            subtitle: Text([r.number, r.reason, if (r.outcome != null) r.outcome!, if (r.note != null) r.note!].join(' · ')),
            leading: _StatusDot(status: r.status),
            trailing: r.status == 'pending' ? TextButton(onPressed: () => _cancel(r), child: Text(tr('Cancel'))) : Text(_statusLabel(r.status)),
          ),
      ],
    );
  }

  Widget _payslipsTab(ThemeData theme) {
    final list = _payslips!;
    if (list.isEmpty) {
      return Center(
          child: Padding(padding: const EdgeInsets.all(24), child: Text(tr('No payslips yet. A month\'s payslip appears once its payroll is finalized.'))));
    }
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        for (final p in list)
          ListTile(
            key: Key('payslip-${p.year}-${p.month}'),
            leading: const Icon(Icons.receipt_long),
            title: Text('${tr(_months[p.month - 1])} ${p.year}'),
            subtitle:
                Text(tr('Days paid {0} of {1} · gross {2} · deductions {3}', [num.parse(p.daysPresent), num.parse(p.daysInPeriod), p.gross, p.deductions])),
            trailing: Row(mainAxisSize: MainAxisSize.min, children: [
              Text(p.netPay, style: theme.textTheme.titleSmall),
              IconButton(tooltip: tr('Save PDF'), icon: const Icon(Icons.download), onPressed: () => _download(p)),
            ]),
          ),
      ],
    );
  }
}

String _statusLabel(String s) => switch (s) {
      'approved' => tr('Approved'),
      'rejected' => tr('Rejected'),
      'cancelled' => tr('Cancelled'),
      _ => tr('Waiting'),
    };

class _StatusDot extends StatelessWidget {
  final String status;
  const _StatusDot({required this.status});
  @override
  Widget build(BuildContext context) {
    final c = switch (status) {
      'approved' => Colors.green,
      'rejected' => Colors.red,
      'cancelled' => Colors.grey,
      _ => Colors.orange,
    };
    return Icon(Icons.circle, size: 12, color: c, semanticLabel: _statusLabel(status));
  }
}

/// The common shape of the three ask dialogs: fields, an error line, Send.
class _AskDialog extends StatelessWidget {
  final String title;
  final List<Widget> children;
  final String? error;
  final bool busy;
  final VoidCallback? onSend;
  const _AskDialog({required this.title, required this.children, this.error, required this.busy, this.onSend});
  @override
  Widget build(BuildContext context) => AlertDialog(
        title: Text(title),
        content: SizedBox(
          width: 420,
          child: SingleChildScrollView(
            child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.stretch, children: [
              ...children,
              if (error != null)
                Padding(padding: const EdgeInsets.only(top: 8), child: Text(error!, style: TextStyle(color: Theme.of(context).colorScheme.error))),
            ]),
          ),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.of(context).pop(), child: Text(tr('Cancel'))),
          FilledButton(key: const Key('ask-send'), onPressed: busy ? null : onSend, child: Text(tr('Send'))),
        ],
      );
}

class _DateField extends StatelessWidget {
  final String label;
  final DateTime value;
  final DateTime first;
  final DateTime last;
  final ValueChanged<DateTime> onChanged;
  const _DateField({required this.label, required this.value, required this.first, required this.last, required this.onChanged});
  @override
  Widget build(BuildContext context) => ListTile(
        contentPadding: EdgeInsets.zero,
        title: Text(label),
        trailing: Text(ymd(value)),
        onTap: () async {
          final d = await showDatePicker(context: context, initialDate: value, firstDate: first, lastDate: last);
          if (d != null) onChanged(d);
        },
      );
}

class _TimeField extends StatelessWidget {
  final String label;
  final TimeOfDay? value;
  final ValueChanged<TimeOfDay> onChanged;
  final Key? fieldKey;
  const _TimeField({required this.label, required this.value, required this.onChanged, this.fieldKey});
  @override
  Widget build(BuildContext context) => ListTile(
        key: fieldKey,
        contentPadding: EdgeInsets.zero,
        title: Text(label),
        trailing: Text(value == null ? '—' : hm(value!)),
        onTap: () async {
          final t = await showTimePicker(context: context, initialTime: value ?? const TimeOfDay(hour: 9, minute: 0));
          if (t != null) onChanged(t);
        },
      );
}

/// Ask for leave: type, dates (or a half day), reason. Pops the request number.
class LeaveRequestDialog extends StatefulWidget {
  const LeaveRequestDialog({super.key});
  @override
  State<LeaveRequestDialog> createState() => _LeaveRequestDialogState();
}

class _LeaveRequestDialogState extends State<LeaveRequestDialog> {
  List<LeaveTypeInfo> _types = [];
  String? _typeId;
  DateTime _from = DateTime.now().add(const Duration(days: 1));
  DateTime _to = DateTime.now().add(const Duration(days: 1));
  bool _half = false;
  final _reason = TextEditingController();
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    context.read<AppSession>().api.leaveTypes().then((t) {
      if (mounted) setState(() => _types = t);
    }).catchError((Object _) {});
  }

  @override
  void dispose() {
    _reason.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    final type = _types.firstWhere((t) => t.id == _typeId);
    final half = _half && ymd(_from) == ymd(_to) && type.allowHalfDay;
    try {
      final n = await context
          .read<AppSession>()
          .api
          .askLeave(leaveTypeId: _typeId!, fromDate: ymd(_from), toDate: ymd(_to), halfDay: half, reason: _reason.text.trim());
      if (mounted) Navigator.of(context).pop(n);
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final now = DateTime.now();
    final oneDay = ymd(_from) == ymd(_to);
    return _AskDialog(
      title: tr('Ask for leave'),
      busy: _busy,
      error: _error,
      onSend: _typeId == null || _reason.text.trim().isEmpty ? null : _send,
      children: [
        DropdownButtonFormField<String>(
          key: const Key('leave-type'),
          initialValue: _typeId,
          decoration: InputDecoration(labelText: tr('Leave type')),
          items: [for (final t in _types) DropdownMenuItem(value: t.id, child: Text(t.paid ? t.name : '${t.name} (${tr('unpaid')})'))],
          onChanged: (v) => setState(() => _typeId = v),
        ),
        _DateField(
          label: tr('From'),
          value: _from,
          first: now.subtract(const Duration(days: 60)),
          last: now.add(const Duration(days: 365)),
          onChanged: (d) => setState(() {
            _from = d;
            if (_to.isBefore(d)) _to = d;
          }),
        ),
        _DateField(label: tr('To'), value: _to, first: _from, last: now.add(const Duration(days: 365)), onChanged: (d) => setState(() => _to = d)),
        if (oneDay) SwitchListTile(contentPadding: EdgeInsets.zero, title: Text(tr('Half day')), value: _half, onChanged: (v) => setState(() => _half = v)),
        TextField(key: const Key('ask-reason'), controller: _reason, decoration: InputDecoration(labelText: tr('Reason')), onChanged: (_) => setState(() {})),
      ],
    );
  }
}

/// Ask for a few hours off: date, from, to, reason.
class PermissionRequestDialog extends StatefulWidget {
  final int maxMinutes;
  const PermissionRequestDialog({super.key, required this.maxMinutes});
  @override
  State<PermissionRequestDialog> createState() => _PermissionRequestDialogState();
}

class _PermissionRequestDialogState extends State<PermissionRequestDialog> {
  DateTime _date = DateTime.now();
  TimeOfDay? _from;
  TimeOfDay? _to;
  final _reason = TextEditingController();
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _reason.dispose();
    super.dispose();
  }

  int get _minutes => _from == null || _to == null ? 0 : (_to!.hour * 60 + _to!.minute) - (_from!.hour * 60 + _from!.minute);

  Future<void> _send() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final n = await context.read<AppSession>().api.askPermission(workDate: ymd(_date), fromTime: hm(_from!), toTime: hm(_to!), reason: _reason.text.trim());
      if (mounted) Navigator.of(context).pop(n);
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final now = DateTime.now();
    return _AskDialog(
      title: tr('Ask for permission'),
      busy: _busy,
      error: _error,
      onSend: _minutes <= 0 || _reason.text.trim().isEmpty ? null : _send,
      children: [
        Text(tr('A few hours off, at most {0} min.', [widget.maxMinutes]), style: Theme.of(context).textTheme.bodySmall),
        _DateField(
            label: tr('Date'),
            value: _date,
            first: now.subtract(const Duration(days: 30)),
            last: now.add(const Duration(days: 60)),
            onChanged: (d) => setState(() => _date = d)),
        _TimeField(fieldKey: const Key('perm-from'), label: tr('From'), value: _from, onChanged: (t) => setState(() => _from = t)),
        _TimeField(fieldKey: const Key('perm-to'), label: tr('To'), value: _to, onChanged: (t) => setState(() => _to = t)),
        if (_minutes > 0) Text(tr('{0} min', [_minutes])),
        TextField(key: const Key('ask-reason'), controller: _reason, decoration: InputDecoration(labelText: tr('Reason')), onChanged: (_) => setState(() {})),
      ],
    );
  }
}

/// Correct a check-in/out of a past day, or add one that was missed.
class CorrectionDialog extends StatefulWidget {
  const CorrectionDialog({super.key});
  @override
  State<CorrectionDialog> createState() => _CorrectionDialogState();
}

class _CorrectionDialogState extends State<CorrectionDialog> {
  DateTime _date = DateTime.now().subtract(const Duration(days: 1));
  List<AttendanceSession> _sessions = [];
  String? _sessionId; // null = add a missed check-in/out
  TimeOfDay? _in;
  TimeOfDay? _out;
  final _reason = TextEditingController();
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _loadDay();
  }

  @override
  void dispose() {
    _reason.dispose();
    super.dispose();
  }

  Future<void> _loadDay() async {
    final month = ymd(_date).substring(0, 7);
    try {
      final days = await context.read<AppSession>().api.myAttendanceMonth(month);
      final day = days.where((d) => d.workDate == ymd(_date)).firstOrNull;
      if (!mounted) return;
      setState(() {
        _sessions = day?.sessions ?? [];
        _sessionId = _sessions.where((s) => s.clockOut == null).firstOrNull?.id;
      });
    } catch (_) {
      if (mounted) setState(() => _sessions = []);
    }
  }

  DateTime _at(TimeOfDay t, {bool nextDay = false}) => DateTime(_date.year, _date.month, _date.day + (nextDay ? 1 : 0), t.hour, t.minute);

  Future<void> _send() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    final start = _in ?? (_sessionId == null ? null : _sessions.firstWhere((s) => s.id == _sessionId).clockIn?.toLocal().let(TimeOfDay.fromDateTime));
    final overnight = _out != null && start != null && (_out!.hour * 60 + _out!.minute) < (start.hour * 60 + start.minute);
    try {
      final n = await context.read<AppSession>().api.askCorrection(
            workDate: ymd(_date),
            sessionId: _sessionId,
            clockIn: _in == null ? null : _at(_in!),
            clockOut: _out == null ? null : _at(_out!, nextDay: overnight),
            reason: _reason.text.trim(),
          );
      if (mounted) Navigator.of(context).pop(n);
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  String _clock(DateTime? t) => t == null ? '—' : hm(TimeOfDay.fromDateTime(t.toLocal()));

  @override
  Widget build(BuildContext context) {
    final now = DateTime.now();
    final ready = _sessionId != null ? (_in != null || _out != null) : (_in != null && _out != null);
    return _AskDialog(
      title: tr('Correct my attendance'),
      busy: _busy,
      error: _error,
      onSend: !ready || _reason.text.trim().isEmpty ? null : _send,
      children: [
        _DateField(
          label: tr('Day'),
          value: _date,
          first: now.subtract(const Duration(days: 30)),
          last: now,
          onChanged: (d) {
            setState(() {
              _date = d;
              _in = null;
              _out = null;
            });
            _loadDay();
          },
        ),
        RadioGroup<String?>(
          groupValue: _sessionId,
          onChanged: (v) => setState(() => _sessionId = v),
          child: Column(children: [
            for (final s in _sessions)
              RadioListTile<String?>(
                contentPadding: EdgeInsets.zero,
                value: s.id,
                title: Text(tr('In {0}, out {1}', [_clock(s.clockIn), s.clockOut == null ? tr('missing') : _clock(s.clockOut)])),
              ),
            RadioListTile<String?>(
              key: const Key('correction-add'),
              contentPadding: EdgeInsets.zero,
              value: null,
              title: Text(tr('Add a check-in and check-out I missed')),
            ),
          ]),
        ),
        _TimeField(
            fieldKey: const Key('correction-in'),
            label: _sessionId == null ? tr('Check-in') : tr('Correct check-in'),
            value: _in,
            onChanged: (t) => setState(() => _in = t)),
        _TimeField(
            fieldKey: const Key('correction-out'),
            label: _sessionId == null ? tr('Check-out') : tr('Correct check-out'),
            value: _out,
            onChanged: (t) => setState(() => _out = t)),
        TextField(key: const Key('ask-reason'), controller: _reason, decoration: InputDecoration(labelText: tr('Reason')), onChanged: (_) => setState(() {})),
      ],
    );
  }
}

extension _Let<T> on T {
  R let<R>(R Function(T) f) => f(this);
}
