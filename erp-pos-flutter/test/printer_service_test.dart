import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:erp_pos_app/printing/printer_service.dart';

/// Exercises the real TCP send path against an actual `ServerSocket` on
/// localhost — no Bluetooth/network thermal printer is available in this
/// environment, but the connect/write/close protocol itself (raw-TCP
/// port 9100 ESC/POS printing) is fully real here, not mocked. What this
/// can't prove is that a genuine printer renders the bytes correctly —
/// that's the same on-device caveat already on file for offline sync and
/// the backend's own printer driver.
void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  test('printBytes throws when no printer is configured', () async {
    final service = PrinterService();
    expect(() => service.printBytes(utf8.encode('hello')), throwsA(isA<StateError>()));
  });

  test('printBytes sends the exact bytes to the configured host/port', () async {
    final server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
    final received = <int>[];
    final receivedAll = Completer<void>();
    server.listen((client) {
      client.listen(
        (chunk) => received.addAll(chunk),
        onDone: () => receivedAll.complete(),
      );
    });

    final service = PrinterService();
    await service.setPrinter(host: server.address.address, port: server.port);

    final payload = List<int>.generate(300, (i) => i % 256); // longer than one TCP segment, to catch a truncation bug
    await service.printBytes(payload);
    await receivedAll.future.timeout(const Duration(seconds: 5));

    expect(received, equals(payload));
    await server.close();
  });

  test('getConfiguredHost/Port round-trip what setPrinter stored', () async {
    final service = PrinterService();
    expect(await service.getConfiguredHost(), isNull);
    expect(await service.getConfiguredPort(), PrinterService.defaultPort);

    await service.setPrinter(host: '192.168.1.50', port: 9100);
    expect(await service.getConfiguredHost(), '192.168.1.50');
    expect(await service.getConfiguredPort(), 9100);

    await service.clearPrinter();
    expect(await service.getConfiguredHost(), isNull);
  });
}
