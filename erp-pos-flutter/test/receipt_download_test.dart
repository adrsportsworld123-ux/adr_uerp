// Receipt PDF download at the till: the API's PDF (GET .../receipt/pdf,
// erp-core-go internal/documents) is saved through AppSession.saveDocument,
// swapped here for an in-memory recorder, so these run without a real
// Downloads folder. The backend is scripted with MockClient.
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:provider/provider.dart';

import 'package:erp_pos_app/api/api_client.dart';
import 'package:erp_pos_app/screens/receipt_screen.dart';
import 'package:erp_pos_app/state/session.dart';
import 'package:erp_pos_app/ui/theme.dart';

const _orderId = 'order-1';
final _pdf = utf8.encode('%PDF-1.4 fake receipt');

class _Saved {
  final String filename;
  final List<int> bytes;
  _Saved(this.filename, this.bytes);
}

(AppSession, List<http.Request>, List<_Saved>) _setup({int status = 200}) {
  final requests = <http.Request>[];
  final saved = <_Saved>[];
  final client = MockClient((r) async {
    requests.add(r);
    if (r.url.path == '/api/v1/sales/orders/$_orderId/receipt/pdf') {
      if (status != 200) {
        return http.Response(jsonEncode({'error': {'code': 'NOT_FOUND', 'message': 'order not found'}}), status,
            headers: {'content-type': 'application/json'});
      }
      return http.Response.bytes(_pdf, 200, headers: {
        'content-type': 'application/pdf',
        'content-disposition': 'attachment; filename="receipt-MGR-1001.pdf"',
      });
    }
    return http.Response('{}', 404);
  });
  final s = AppSession(
    api: ApiClient(baseUrl: 'http://pos-test.invalid', httpClient: client),
    branchId: 'branch-test',
    posTerminalId: 'terminal-test',
  );
  s.saveDocument = (name, bytes) async {
    saved.add(_Saved(name, bytes));
    return 'C:/Downloads/$name';
  };
  return (s, requests, saved);
}

void main() {
  test('downloads the receipt PDF under the server\'s file name', () async {
    final (session, requests, saved) = _setup();
    final path = await session.downloadReceiptFor(_orderId);
    expect(path, 'C:/Downloads/receipt-MGR-1001.pdf');
    expect(saved.single.filename, 'receipt-MGR-1001.pdf');
    expect(saved.single.bytes, _pdf);
    expect(requests.single.url.queryParameters, isEmpty, reason: 'no format = the branch default paper');
  });

  test('asks for the paper the cashier picked', () async {
    final (session, requests, _) = _setup();
    await session.downloadReceiptFor(_orderId, format: 'thermal');
    expect(requests.single.url.queryParameters['format'], 'thermal');
  });

  test('surfaces the API error and saves nothing', () async {
    final (session, _, saved) = _setup(status: 404);
    expect(await session.downloadReceiptFor(_orderId), isNull);
    expect(session.lastError, 'order not found');
    expect(saved, isEmpty);
  });

  test('a sale with no current order has nothing to download', () async {
    final (session, requests, _) = _setup();
    expect(await session.downloadReceipt(), isNull);
    expect(session.lastError, contains('synced'));
    expect(requests, isEmpty);
  });

  testWidgets('Download PDF on a history receipt saves it and says where', (tester) async {
    final (session, _, saved) = _setup();
    final receipt = ReceiptData.fromJson({
      'order_number': 'MGR-1001',
      'status': 'finalized',
      'finalized_at': '2026-10-10T10:15:00Z',
      'merchant_name': 'Acme Sports',
      'branch_name': 'Koramangala',
      'cashier_name': 'Ravi',
      'customer_name': null,
      'lines': [
        {'product_name': 'Cricket Ball', 'sku': 'CRK-BALL', 'quantity': '1.000', 'unit_price': '100.00', 'line_total': '100.00'}
      ],
      'subtotal': '100.00',
      'discount_total': '0.00',
      'tax_total': '0.00',
      'grand_total': '100.00',
      'payments': [
        {'method': 'cash', 'amount': '100.00'}
      ],
    });
    await tester.pumpWidget(MaterialApp(
      theme: posTheme(Brightness.light),
      home: ChangeNotifierProvider<AppSession>.value(
        value: session,
        child: ReceiptScreen(receipt: receipt, orderId: _orderId),
      ),
    ));
    await tester.scrollUntilVisible(find.byKey(const Key('receipt-download')), 200);
    await tester.tap(find.byKey(const Key('receipt-download')));
    for (var i = 0; i < 5; i++) {
      await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 20)));
      await tester.pump(const Duration(milliseconds: 16));
    }
    expect(saved, hasLength(1));
    expect(find.textContaining('Saved to C:/Downloads/receipt-MGR-1001.pdf'), findsOneWidget);
  });
}
