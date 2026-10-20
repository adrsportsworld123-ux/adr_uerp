import 'package:flutter/material.dart';
import 'package:geolocator/geolocator.dart';
import 'package:provider/provider.dart';

import '../api/api_client.dart';
import '../l10n/l10n.dart';
import '../state/session.dart';

/// Where the device is, for an attendance punch — or why it can't say.
class DeviceFix {
  final double? latitude;
  final double? longitude;
  final double? accuracyM;
  final String? problem;

  const DeviceFix.found(double this.latitude, double this.longitude, double this.accuracyM) : problem = null;
  const DeviceFix.missing(String this.problem)
      : latitude = null,
        longitude = null,
        accuracyM = null;

  bool get ok => latitude != null;
}

/// Asks the platform for one location fix. Never throws: a till without
/// GPS (or a platform geolocator doesn't support, such as Linux) answers
/// with the reason, and the paired terminal stands in as the evidence.
Future<DeviceFix> currentFix() async {
  try {
    if (!await Geolocator.isLocationServiceEnabled()) {
      return DeviceFix.missing(tr('Location is turned off on this device.'));
    }
    var perm = await Geolocator.checkPermission();
    if (perm == LocationPermission.denied) perm = await Geolocator.requestPermission();
    if (perm == LocationPermission.denied || perm == LocationPermission.deniedForever) {
      return DeviceFix.missing(tr('This app is not allowed to use the location.'));
    }
    final p = await Geolocator.getCurrentPosition(
      locationSettings: const LocationSettings(accuracy: LocationAccuracy.high, timeLimit: Duration(seconds: 15)),
    );
    return DeviceFix.found(p.latitude, p.longitude, p.accuracy);
  } catch (_) {
    return DeviceFix.missing(tr('This terminal has no location service.'));
  }
}

/// Check-in / check-out for the signed-in employee (menu → Attendance).
class AttendanceScreen extends StatefulWidget {
  /// Injectable for tests; defaults to the device's location.
  final Future<DeviceFix> Function() locate;

  const AttendanceScreen({super.key, this.locate = currentFix});

  @override
  State<AttendanceScreen> createState() => _AttendanceScreenState();
}

class _AttendanceScreenState extends State<AttendanceScreen> {
  MyAttendance? _me;
  DeviceFix? _fix;
  bool _locating = false;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final api = context.read<AppSession>().api;
    setState(() => _error = null);
    try {
      final me = await api.myAttendance();
      if (!mounted) return;
      setState(() => _me = me);
      context.read<AppSession>().setAttendance(me);
      _locate();
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } catch (_) {
      if (mounted) setState(() => _error = tr('Attendance needs a connection to the server.'));
    }
  }

  Future<void> _locate() async {
    setState(() => _locating = true);
    final f = await widget.locate();
    if (mounted) {
      setState(() {
        _fix = f;
        _locating = false;
      });
    }
  }

  String _time(DateTime? t) => t == null ? '—' : TimeOfDay.fromDateTime(t).format(context);

  Future<void> _punch() async {
    final me = _me!;
    final checkIn = !me.checkedIn;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final rec = await context.read<AppSession>().api.punch(
            checkIn: checkIn,
            latitude: _fix?.latitude,
            longitude: _fix?.longitude,
            accuracyM: _fix?.accuracyM,
          );
      if (!mounted) return;
      final fence = checkIn ? rec.clockInGeofence : rec.clockOutGeofence;
      final where = fence == 'outside' ? tr(' — outside the branch area, flagged for your manager') : '';
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(checkIn ? tr('Checked in at {0}{1}', [_time(rec.clockIn), where]) : tr('Checked out at {0}{1}', [_time(rec.clockOut), where]))),
      );
      Navigator.of(context).pop();
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } catch (_) {
      if (mounted) setState(() => _error = tr('Could not reach the server. Try again when back online.'));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final me = _me;
    return Scaffold(
      appBar: AppBar(title: Text(tr('Attendance'))),
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 480),
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: me == null
                ? (_error != null
                    ? Column(mainAxisSize: MainAxisSize.min, children: [
                        Text(_error!, style: TextStyle(color: theme.colorScheme.error)),
                        const SizedBox(height: 12),
                        OutlinedButton(onPressed: _load, child: Text(tr('Try again'))),
                      ])
                    : const CircularProgressIndicator())
                : Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Text(
                        me.checkedIn
                            ? tr('Checked in since {0}', [_time(me.openSince ?? me.open!.clockIn)])
                            : me.sessionsToday > 0
                                ? tr('Checked out')
                                : tr('Not checked in yet'),
                        key: const Key('attendance-status'),
                        style: theme.textTheme.headlineSmall,
                        textAlign: TextAlign.center,
                      ),
                      if (me.sessionsToday > 0) ...[
                        const SizedBox(height: 12),
                        Text(tr('Today · {0} h worked', [me.today!.hoursWorked ?? '0.00']), textAlign: TextAlign.center, style: theme.textTheme.titleSmall),
                        const SizedBox(height: 4),
                        for (final (i, s) in me.today!.sessions.indexed)
                          Text(
                            '#${i + 1}  ${_time(s.clockIn)} – ${s.clockOut == null ? 'now' : _time(s.clockOut)}'
                            '${s.minutes == null ? '' : '  (${s.minutes! ~/ 60}h ${s.minutes! % 60}m)'}',
                            key: Key('attendance-session-$i'),
                            textAlign: TextAlign.center,
                          ),
                      ],
                      const SizedBox(height: 24),
                      ListTile(
                        leading: _locating
                            ? const SizedBox(width: 24, height: 24, child: CircularProgressIndicator(strokeWidth: 2))
                            : Icon(_fix?.ok == true ? Icons.location_on : Icons.location_off_outlined),
                        title: Text(_locating
                            ? tr('Getting the location…')
                            : _fix?.ok == true
                                ? tr('Location found (±{0} m)', [_fix!.accuracyM!.round()])
                                : (_fix?.problem ?? '')),
                        subtitle: Text(me.geofenceBranch != null
                            ? tr('Checked against {0}\'s {1} m area. This terminal and its network address are recorded too.', [me.geofenceBranch, me.geofenceRadiusM])
                            : tr('This terminal and its network address are recorded with the time.')),
                        trailing: !_locating && _fix?.ok != true ? TextButton(onPressed: _locate, child: Text(tr('Retry'))) : null,
                      ),
                      const SizedBox(height: 16),
                      FilledButton.icon(
                        key: const Key('attendance-punch'),
                        onPressed: _busy || _locating || !me.allowPos ? null : _punch,
                        icon: _busy
                            ? const SizedBox(width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2))
                            : Icon(me.checkedIn ? Icons.logout : Icons.login),
                        label: Text(me.checkedIn ? tr('Check out') : (me.sessionsToday > 0 ? tr('Check in again') : tr('Check in'))),
                        style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(56)),
                      ),
                      if (!me.allowPos)
                        Padding(
                          padding: const EdgeInsets.only(top: 8),
                          child: Text(tr('Your company has turned off checking in from the POS.'), textAlign: TextAlign.center),
                        ),
                      if (_error != null)
                        Padding(
                          padding: const EdgeInsets.only(top: 12),
                          child: Text(_error!, style: TextStyle(color: theme.colorScheme.error), textAlign: TextAlign.center),
                        ),
                    ],
                  ),
          ),
        ),
      ),
    );
  }
}
