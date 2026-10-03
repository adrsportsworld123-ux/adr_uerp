import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as p;
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import 'package:erp_pos_app/api/api_client.dart';
import 'package:erp_pos_app/local/offline_store.dart';

/// Exercises the actual sqflite logic offline sync depends on — real SQL
/// via sqflite_common_ffi (no Android/iOS device or emulator available in
/// this environment to click through the UI live), not just a compile
/// check. This is the part of the offline feature most likely to have a
/// real bug: arithmetic, status transitions, and the shape handed to
/// POST /sync/push.
void main() {
  setUpAll(() {
    sqfliteFfiInit();
    databaseFactory = databaseFactoryFfi;
  });

  test('offline cart: add lines, checkout, and produce a syncable order', () async {
    final store = OfflineStore();
    final dbPath = p.join(await databaseFactory.getDatabasesPath(), 'erp_pos_offline.db');
    await databaseFactory.deleteDatabase(dbPath);

    const branchId = 'branch-1';
    const terminalId = 'terminal-1';

    await store.cacheCatalog([
      {
        'barcode': '1111111111111',
        'product_id': 'prod-1',
        'product_name': 'Test Bat',
        'hsn_code': '9506',
        'variant_id': 'variant-1',
        'sku': 'SKU-1',
        'selling_price': '100.00',
        'mrp': '120.00',
        'cgst_rate': 9.0,
        'sgst_rate': 9.0,
        'igst_rate': 0.0,
        'cess_rate': 0.0,
      }
    ]);

    final cached = await store.lookupBarcode('1111111111111');
    expect(cached, isNotNull);
    expect(cached!.lookup.variantId, 'variant-1');

    final orderId = await store.createPendingOrder(branchId, terminalId);

    // 2 units @ 100 = 200 subtotal, 18% tax = 36, line_total = 236.
    var summary = await store.addLine(orderId, cached, 2);
    expect(summary.subtotal, '200.00');
    expect(summary.taxTotal, '36.00');
    expect(summary.grandTotal, '236.00');
    expect(summary.lines, hasLength(1));
    expect(summary.status, 'cart');

    // Second line: 1 more unit of the same variant — a separate line, not
    // merged, matching the server's AddLine behavior.
    summary = await store.addLine(orderId, cached, 1);
    expect(summary.subtotal, '300.00');
    expect(summary.lines, hasLength(2));

    // Remove the second line — totals should fall back to the first line's.
    final secondLineId = summary.lines[1].lineId;
    summary = await store.removeLine(orderId, secondLineId);
    expect(summary.subtotal, '200.00');
    expect(summary.lines, hasLength(1));

    // Checkout: status flips to 'finalized' for the UI, and the order
    // becomes visible to listUnsynced with its payment attached.
    summary = await store.checkout(orderId, [PaymentInput(method: 'cash', amount: 236.0)]);
    expect(summary.status, 'finalized');

    final unsynced = await store.listUnsynced();
    expect(unsynced, hasLength(1));
    expect(unsynced.first.id, orderId);
    expect(unsynced.first.lines, hasLength(1));
    expect(unsynced.first.payments, hasLength(1));
    expect(unsynced.first.payments.first['amount'], 236.0);

    // Simulate a successful sync — it must disappear from the pending queue.
    await store.markSynced(orderId, 'server-order-id-123');
    expect(await store.listUnsynced(), isEmpty);
    expect(await store.countPendingSync(), 0);
  });

  // erp-core-go migrations/033: a GST-inclusive product sold offline must
  // bill exactly what the server would (internal/taxcalc.ComputeLine) —
  // the customer pays the shelf price, tax is extracted from it, and the
  // whole line is split at once so 3 x 1000 is 3000.00, not 3000.01.
  test('offline cart: tax-inclusive product bills exactly the shelf price', () async {
    final store = OfflineStore();
    await store.cacheCatalog([
      {
        'barcode': '2222222222222',
        'product_id': 'prod-incl',
        'product_name': 'MRP Shuttlecock',
        'hsn_code': '9506',
        'variant_id': 'variant-incl',
        'sku': 'SKU-INCL',
        'selling_price': '1000.00',
        'mrp': '1000.00',
        'cgst_rate': 9.0,
        'sgst_rate': 9.0,
        'igst_rate': 0.0,
        'cess_rate': 0.0,
        'price_includes_tax': true,
      },
      {
        'barcode': '3333333333333',
        'product_id': 'prod-excl',
        'product_name': 'Grip Tape',
        'hsn_code': '9506',
        'variant_id': 'variant-excl',
        'sku': 'SKU-EXCL',
        'selling_price': '100.00',
        'mrp': '118.00',
        'cgst_rate': 9.0,
        'sgst_rate': 9.0,
        'igst_rate': 0.0,
        'cess_rate': 0.0,
        // older server: no price_includes_tax key at all -> exclusive
      },
    ]);

    final incl = await store.lookupBarcode('2222222222222');
    final excl = await store.lookupBarcode('3333333333333');
    expect(incl!.priceIncludesTax, isTrue);
    expect(excl!.priceIncludesTax, isFalse);

    final orderId = await store.createPendingOrder('branch-1', 'terminal-1');
    var summary = await store.addLine(orderId, incl, 3);
    expect(summary.lines.single.lineTotal, '3000.00');
    expect(summary.lines.single.taxAmount, '457.63');
    expect(summary.subtotal, '2542.37'); // pre-tax, same definition as the server's recalcOrderTotals
    expect(summary.grandTotal, '3000.00');

    // Mixed cart: the exclusive line still adds tax on top.
    summary = await store.addLine(orderId, excl, 1);
    expect(summary.subtotal, '2642.37');
    expect(summary.taxTotal, '475.63');
    expect(summary.grandTotal, '3118.00');

    await store.checkout(orderId, [PaymentInput(method: 'cash', amount: 3118.0)]);
    final pending = (await store.listUnsynced()).singleWhere((o) => o.id == orderId);
    final pushedIncl = pending.lines.singleWhere((l) => l['variant_id'] == 'variant-incl');
    expect(pushedIncl['price_includes_tax'], 1);
    expect(pushedIncl['line_total'], 3000.0);
    await store.markSynced(orderId, 'server-order-incl');
  });
}
