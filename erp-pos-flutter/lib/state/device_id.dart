import 'dart:math';
import 'package:shared_preferences/shared_preferences.dart';

/// A per-device fingerprint for PIN-login device binding
/// (POST /auth/pin-login — see erp-core-go's internal/authn/pin_handlers.go).
/// Generated once and persisted in SharedPreferences so it survives app
/// restarts; a fingerprint that changed on every launch would fail the
/// server's device-binding check on the very next login attempt after the
/// one that bound it.
class DeviceId {
  static const _prefsKey = 'device_fingerprint';

  static Future<String> get() async {
    final prefs = await SharedPreferences.getInstance();
    final existing = prefs.getString(_prefsKey);
    if (existing != null && existing.isNotEmpty) return existing;

    final generated = _generate();
    await prefs.setString(_prefsKey, generated);
    return generated;
  }

  static String _generate() {
    final rand = Random.secure();
    final bytes = List<int>.generate(16, (_) => rand.nextInt(256));
    return bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join();
  }
}
