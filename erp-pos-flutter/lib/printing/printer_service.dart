import 'dart:async';
import 'dart:io';

import 'package:shared_preferences/shared_preferences.dart';

/// Sends raw ESC/POS bytes (from ApiClient.printReceiptBytes, itself from
/// erp-core-go's internal/printing.BuildReceipt) to a thermal printer over
/// its standard raw-TCP print port (9100) — the same protocol Epson TM
/// series and most generic ESC/POS network printers speak, needing no
/// vendor SDK or Bluetooth pairing flow. Chosen over a Bluetooth printer
/// package specifically to keep this app's dependency-light posture
/// (pubspec.yaml has taken on exactly one new package since Phase 0,
/// sqflite, for offline sync) — `dart:io Socket` is stdlib, not a new
/// dependency.
///
/// **Verification status, matching this project's own established
/// pattern for hardware this sandbox doesn't have** (see
/// docs/phased_roadmap.md's Phase 1 status: offline sync and the backend
/// printer driver both carry the identical caveat): the bytes this sends
/// are the same bytes `internal/printing`'s own unit tests already proved
/// byte-correct against a real ESC/POS command reference, and the TCP
/// send path itself is exercised by `PrinterServiceTest`'s fake server
/// socket (a real `ServerSocket` on localhost, not a mock) — but nothing
/// here has been confirmed against a real network thermal printer. Do
/// that before trusting this in front of a real cashier, same as the
/// offline-sync and receipt-driver caveats already on file.
class PrinterService {
  static const _prefsHostKey = 'printer_host';
  static const _prefsPortKey = 'printer_port';
  static const defaultPort = 9100;

  final Duration timeout;

  PrinterService({this.timeout = const Duration(seconds: 5)});

  Future<String?> getConfiguredHost() async {
    final prefs = await SharedPreferences.getInstance();
    return prefs.getString(_prefsHostKey);
  }

  Future<int> getConfiguredPort() async {
    final prefs = await SharedPreferences.getInstance();
    return prefs.getInt(_prefsPortKey) ?? defaultPort;
  }

  Future<void> setPrinter({required String host, int port = defaultPort}) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_prefsHostKey, host);
    await prefs.setInt(_prefsPortKey, port);
  }

  Future<void> clearPrinter() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_prefsHostKey);
    await prefs.remove(_prefsPortKey);
  }

  /// Opens a TCP connection to the configured printer and writes the raw
  /// bytes, then closes it — an ESC/POS network printer treats the
  /// connection itself as the print job boundary, no handshake needed.
  /// Throws if no printer is configured; callers should check
  /// `getConfiguredHost()` first if they want to skip printing silently
  /// (e.g. show the on-screen receipt only) rather than surface an error.
  Future<void> printBytes(List<int> bytes) async {
    final host = await getConfiguredHost();
    if (host == null || host.isEmpty) {
      throw StateError('No printer configured — set one in Printer Settings first');
    }
    final port = await getConfiguredPort();
    final socket = await Socket.connect(host, port, timeout: timeout);
    try {
      socket.add(bytes);
      await socket.flush();
    } finally {
      await socket.close();
    }
  }
}
