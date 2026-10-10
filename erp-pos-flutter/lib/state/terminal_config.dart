import 'dart:convert';

import 'package:shared_preferences/shared_preferences.dart';

/// Which terminal this device is (Phase 12). Written once by the pairing
/// screen after POST /pos-terminals/pair succeeds, and read on every start
/// — replacing the seed terminal/branch ids that used to be hardcoded in
/// main.dart. Ids are what the API needs; names are only for display.
class TerminalConfig {
  final String terminalId;
  final String terminalName;
  final String branchId;
  final String branchName;
  final String merchantCode;
  final String merchantName;

  const TerminalConfig({
    required this.terminalId,
    required this.terminalName,
    required this.branchId,
    required this.branchName,
    required this.merchantCode,
    required this.merchantName,
  });

  static const _prefsKey = 'terminal_config';

  /// The seed data's terminal (migrations/002_seed.sql), for development
  /// only: `--dart-define=USE_SEED_TERMINAL=true` skips pairing against a
  /// fresh docker-compose backend.
  static const seed = TerminalConfig(
    terminalId: '33333333-3333-3333-3333-333333333333',
    terminalName: 'Till 1',
    branchId: '22222222-2222-2222-2222-222222222222',
    branchName: 'MG Road',
    merchantCode: 'acme-sports',
    merchantName: 'Acme Sports',
  );

  Map<String, dynamic> toJson() => {
        'terminal_id': terminalId,
        'terminal_name': terminalName,
        'branch_id': branchId,
        'branch_name': branchName,
        'merchant_code': merchantCode,
        'merchant_name': merchantName,
      };

  factory TerminalConfig.fromJson(Map<String, dynamic> j) => TerminalConfig(
        terminalId: j['terminal_id'] as String,
        terminalName: j['terminal_name'] as String,
        branchId: j['branch_id'] as String,
        branchName: j['branch_name'] as String,
        merchantCode: j['merchant_code'] as String,
        merchantName: j['merchant_name'] as String,
      );

  static Future<TerminalConfig?> load() async {
    final prefs = await SharedPreferences.getInstance();
    final raw = prefs.getString(_prefsKey);
    if (raw == null || raw.isEmpty) return null;
    try {
      return TerminalConfig.fromJson(jsonDecode(raw) as Map<String, dynamic>);
    } catch (_) {
      return null; // unreadable (e.g. an older format): pair again
    }
  }

  Future<void> save() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_prefsKey, jsonEncode(toJson()));
  }

  /// Forgets the pairing on this device only. The server still lists the
  /// device as bound until an admin resets it on the POS Terminals screen.
  static Future<void> clear() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_prefsKey);
  }
}
