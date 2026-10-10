import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:erp_pos_app/api/api_client.dart';

/// Exercises every ApiClient method added in this pass against a REAL,
/// running erp-core-go backend (docker-compose, localhost:8080) — not
/// mocked. `flutter analyze`/`flutter test`'s normal run only proves this
/// code compiles and the pure-Dart logic (offline store, printer socket)
/// works; it cannot catch a wrong JSON field name or URL path against the
/// actual server, which is exactly the class of bug worth checking for
/// real here rather than trusting the mirrored Go structs by inspection
/// alone.
///
/// Skipped automatically (not failed) if nothing is listening on
/// localhost:8080, so this doesn't break `flutter test` in an
/// environment without the docker-compose stack running.
// ERP_API_URL points the suite at another instance (e.g. a throwaway API
// built from a branch, on :8081) without touching the developer's own.
final _baseUrl = Platform.environment['ERP_API_URL'] ?? 'http://localhost:8080';

void main() {
  late ApiClient api;
  bool serverUp = false;

  setUpAll(() async {
    // Merchant Admin, not the POS User seed account — this suite exercises
    // void (gated by the sales.void permission), which a POS User is
    // correctly denied.
    api = ApiClient(baseUrl: _baseUrl);
    try {
      await api.login(merchantCode: 'acme-sports', email: 'arjun@acme-sports.test', password: 'Passw0rd!');
      serverUp = true;
    } catch (_) {
      serverUp = false;
    }
  });

  test('the full POS feature set works end to end against the real backend', () async {
    if (!serverUp) {
      // ignore: avoid_print
      print('SKIPPED: no live erp-core-go backend at $_baseUrl');
      return;
    }

    const branchId = '22222222-2222-2222-2222-222222222222';
    const terminalId = '33333333-3333-3333-3333-333333333333';
    const variantId = '99999999-9999-9999-9999-999999999999';
    final idempotencyKey = 'flutter-live-test-${DateTime.now().microsecondsSinceEpoch}';

    var order = await api.createOrder(branchId: branchId, posTerminalId: terminalId, idempotencyKey: idempotencyKey);
    expect(order.status, 'cart');

    order = await api.addLine(orderId: order.orderId, variantId: variantId, quantity: 4);
    expect(order.lines, hasLength(1));

    order = await api.updateLineQuantity(orderId: order.orderId, lineId: order.lines.first.lineId, quantity: 2);
    expect(order.lines.first.quantity, '2.000');

    final walkInName = 'Flutter Live Test ${DateTime.now().microsecondsSinceEpoch}';
    final customerId = await api.attachCustomer(orderId: order.orderId, name: walkInName, phone: '9999900000');
    expect(customerId, isNotEmpty);

    final loyaltyBefore = await api.getCustomerLoyalty(customerId);
    expect(loyaltyBefore.availablePoints, 0); // brand-new customer

    order = await api.applyDiscount(orderId: order.orderId, valuePercent: 5, reason: 'live test discount');
    expect(double.parse(order.discountTotal), greaterThan(0));

    // A brand-new customer has no active promotions/coupons targeting
    // them in the seed data — applying promotions should still succeed
    // (it's a no-op evaluation, not an error) even if nothing matches.
    order = await api.applyPromotions(order.orderId);
    expect(order.status, 'cart');

    order = await api.checkout(orderId: order.orderId, payments: [
      PaymentInput(method: 'cash', amount: double.parse(order.grandTotal)),
    ]);
    expect(order.status, 'finalized');

    final receipt = await api.getReceipt(order.orderId);
    expect(receipt.orderNumber, order.orderNumber);
    expect(receipt.lines, hasLength(1));
    expect(receipt.payments, hasLength(1));

    final printBytes = await api.printReceiptBytes(order.orderId);
    expect(printBytes, isNotEmpty);

    order = await api.voidOrder(orderId: order.orderId, reason: 'live test cleanup');
    expect(order.status, 'voided');
  });

  test('a credit payment is rejected with no customer attached, and coupon error codes surface correctly', () async {
    if (!serverUp) return;

    const branchId = '22222222-2222-2222-2222-222222222222';
    const terminalId = '33333333-3333-3333-3333-333333333333';
    const variantId = '99999999-9999-9999-9999-999999999999';
    final idempotencyKey = 'flutter-live-test-credit-${DateTime.now().microsecondsSinceEpoch}';

    var order = await api.createOrder(branchId: branchId, posTerminalId: terminalId, idempotencyKey: idempotencyKey);
    order = await api.addLine(orderId: order.orderId, variantId: variantId, quantity: 1);

    await expectLater(
      api.checkout(orderId: order.orderId, payments: [PaymentInput(method: 'credit', amount: double.parse(order.grandTotal))]),
      throwsA(isA<ApiException>().having((e) => e.code, 'code', 'INVALID_REQUEST')),
    );

    await expectLater(
      api.applyCoupon(orderId: order.orderId, code: 'NO-SUCH-COUPON-CODE'),
      throwsA(isA<ApiException>().having((e) => e.code, 'code', 'COUPON_NOT_FOUND')),
    );

    // Clean up: check out for real so the reservation isn't left dangling.
    await api.checkout(orderId: order.orderId, payments: [PaymentInput(method: 'cash', amount: double.parse(order.grandTotal))]);
  });

  test('a credit payment succeeds once a customer is attached, and books to the right customer', () async {
    if (!serverUp) return;

    const branchId = '22222222-2222-2222-2222-222222222222';
    const terminalId = '33333333-3333-3333-3333-333333333333';
    const variantId = '99999999-9999-9999-9999-999999999999';
    final idempotencyKey = 'flutter-live-test-credit-ok-${DateTime.now().microsecondsSinceEpoch}';

    var order = await api.createOrder(branchId: branchId, posTerminalId: terminalId, idempotencyKey: idempotencyKey);
    order = await api.addLine(orderId: order.orderId, variantId: variantId, quantity: 1);

    final walkInName = 'Flutter Credit Test ${DateTime.now().microsecondsSinceEpoch}';
    final customerId = await api.attachCustomer(orderId: order.orderId, name: walkInName, phone: '9999911111');

    // Setting a credit limit is an admin/back-office action (erp-web-admin's
    // Credit section on the customer page, gated by credit.manage) — not
    // something this POS app itself ever does, so a raw call here, not a
    // new ApiClient method, just to seed a limit this brand-new walk-in
    // customer wouldn't otherwise have (defaults to 0, correctly rejecting
    // any credit amount — confirmed as its own real behavior, not a bug,
    // the first time this test ran without this step).
    final adminLoginResp = await http.post(
      Uri.parse('$_baseUrl/api/v1/auth/login'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({'merchant_code': 'acme-sports', 'email': 'arjun@acme-sports.test', 'password': 'Passw0rd!'}),
    );
    final adminToken = (jsonDecode(adminLoginResp.body) as Map<String, dynamic>)['access_token'] as String;
    final creditPatchResp = await http.patch(
      Uri.parse('$_baseUrl/api/v1/customers/$customerId/credit'),
      headers: {'Content-Type': 'application/json', 'Authorization': 'Bearer $adminToken'},
      body: jsonEncode({'credit_limit': 5000}),
    );
    expect(creditPatchResp.statusCode, 200);

    order = await api.checkout(orderId: order.orderId, payments: [
      PaymentInput(method: 'credit', amount: double.parse(order.grandTotal)),
    ]);
    expect(order.status, 'finalized');

    final receipt = await api.getReceipt(order.orderId);
    expect(receipt.payments.single.method, 'credit');
    expect(receipt.customerName, walkInName);

    // Clean up: void it so it doesn't sit as real outstanding receivable
    // for a synthetic test customer.
    await api.voidOrder(orderId: order.orderId, reason: 'live test cleanup');
    // customerId isn't otherwise used after checkout — referenced here
    // only to document that AttachCustomer's returned id is what
    // Checkout's credit-limit check keys off of.
    expect(customerId, isNotEmpty);
  });

  test('product search: name search, category browse, and add-to-cart via a search hit', () async {
    if (!serverUp) return;

    // Name search (fuzzy, via internal/search) should find the seed
    // product even with a deliberate typo — same tolerance
    // erp-web-admin's own search.spec.ts already proves server-side;
    // this just confirms this app's ApiClient decodes the response shape
    // correctly.
    final byName = await api.searchProducts(query: 'crikcet bat');
    expect(byName, isNotEmpty);
    final hit = byName.firstWhere((r) => r.sku == 'SG-BAT-SH');
    expect(hit.name, 'SG Cricket Bat');
    expect(hit.sellingPrice, greaterThan(0));

    // Category browse: an empty query with a real category_id should
    // still return results (the backend falls back to match-all), and
    // every result should actually belong to that category.
    final categories = await api.listCategories();
    expect(categories, isNotEmpty);
    if (hit.categoryId != null) {
      final byCategory = await api.searchProducts(categoryId: hit.categoryId);
      expect(byCategory, isNotEmpty);
      expect(byCategory.every((r) => r.categoryId == hit.categoryId), isTrue);
    }

    // A search hit's toProductLookup() must be a drop-in for the exact
    // cart-mutating call site scanBarcode's result already uses — proven
    // by actually adding it to a real cart, not just shape-checking the
    // conversion.
    const branchId = '22222222-2222-2222-2222-222222222222';
    const terminalId = '33333333-3333-3333-3333-333333333333';
    final idempotencyKey = 'flutter-live-test-search-${DateTime.now().microsecondsSinceEpoch}';
    var order = await api.createOrder(branchId: branchId, posTerminalId: terminalId, idempotencyKey: idempotencyKey);
    final lookup = hit.toProductLookup();
    order = await api.addLine(orderId: order.orderId, variantId: lookup.variantId, quantity: 1);
    expect(order.lines, hasLength(1));
    expect(order.lines.first.sku, 'SG-BAT-SH');

    // Clean up: this reservation would otherwise sit until the
    // reservation-expiry sweeper releases it 15 minutes later.
    await api.deleteLine(orderId: order.orderId, lineId: order.lines.first.lineId);
  });

  test('hold and recall: a held cart is excluded from held-orders once recalled, then checks out normally', () async {
    if (!serverUp) return;

    const branchId = '22222222-2222-2222-2222-222222222222';
    const terminalId = '33333333-3333-3333-3333-333333333333';
    const variantId = '99999999-9999-9999-9999-999999999999';
    final idempotencyKey = 'flutter-live-test-hold-${DateTime.now().microsecondsSinceEpoch}';

    var order = await api.createOrder(branchId: branchId, posTerminalId: terminalId, idempotencyKey: idempotencyKey);
    order = await api.addLine(orderId: order.orderId, variantId: variantId, quantity: 1);

    final held = await api.holdOrder(orderId: order.orderId, note: 'flutter live test hold');
    expect(held.status, 'held');

    final heldList = await api.listHeldOrders(branchId);
    expect(heldList.any((h) => h.orderId == order.orderId), isTrue);

    final recalled = await api.recallOrder(order.orderId);
    expect(recalled.order.status, 'cart');
    expect(recalled.unavailableLines, isEmpty); // stock was never lost within this short test

    final heldListAfter = await api.listHeldOrders(branchId);
    expect(heldListAfter.any((h) => h.orderId == order.orderId), isFalse);

    order = await api.checkout(orderId: order.orderId, payments: [
      PaymentInput(method: 'cash', amount: double.parse(recalled.order.grandTotal)),
    ]);
    expect(order.status, 'finalized');
  });

  test('Phase 13: history lists a new sale, and a partial return is approved, refunded and replay-safe', () async {
    if (!serverUp) return;

    const branchId = '22222222-2222-2222-2222-222222222222';
    const terminalId = '33333333-3333-3333-3333-333333333333';
    const variantId = '99999999-9999-9999-9999-999999999999';
    var order = await api.createOrder(
        branchId: branchId, posTerminalId: terminalId, idempotencyKey: 'flutter-live-p13-${DateTime.now().microsecondsSinceEpoch}');
    order = await api.addLine(orderId: order.orderId, variantId: variantId, quantity: 2);
    order = await api.checkout(orderId: order.orderId, payments: [PaymentInput(method: 'cash', amount: double.parse(order.grandTotal))]);

    final page = await api.listSalesOrders(terminalId: terminalId, limit: 5);
    expect(page.orders.first.orderId, order.orderId, reason: 'newest first');
    expect(page.orders.first.paymentMethods, ['cash']);
    final summary = await api.salesSummary(terminalId: terminalId);
    expect(summary.orderCount, greaterThanOrEqualTo(1));

    final returnable = await api.getReturnable(order.orderId);
    expect(returnable.returnable, isTrue);
    expect(returnable.lines.single.returnableQuantity, '2.000');

    final req = ReturnRequest(
      quantities: {returnable.lines.single.lineId: 1},
      reason: 'flutter live test',
      refundMethod: 'cash',
      approverCode: 'EMP003',
      approverPin: '1234',
      idempotencyKey: 'flutter-live-ret-${DateTime.now().microsecondsSinceEpoch}',
    );
    final r1 = await api.createReturn(orderId: order.orderId, posTerminalId: terminalId, req: req);
    expect(r1.orderStatus, 'partially_refunded');
    expect(double.parse(r1.refundAmount), closeTo(double.parse(order.grandTotal) / 2, 0.011));
    final r2 = await api.createReturn(orderId: order.orderId, posTerminalId: terminalId, req: req);
    expect(r2.returnNumber, r1.returnNumber, reason: 'same idempotency key -> same return');
    expect((await api.getReturnable(order.orderId)).lines.single.returnableQuantity, '1.000');
  });

  test('Phase 13: an open-price item needs a typed price within its range', () async {
    if (!serverUp) return;

    const branchId = '22222222-2222-2222-2222-222222222222';
    const terminalId = '33333333-3333-3333-3333-333333333333';
    final login = await http.post(
      Uri.parse('$_baseUrl/api/v1/auth/login'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({'merchant_code': 'acme-sports', 'email': 'arjun@acme-sports.test', 'password': 'Passw0rd!'}),
    );
    final token = (jsonDecode(login.body) as Map<String, dynamic>)['access_token'] as String;
    final authHeaders = {'Content-Type': 'application/json', 'Authorization': 'Bearer $token'};
    // Its own product (with a generated barcode and a little stock), so
    // the test never depends on data other suites change.
    final sku = 'FLT-OP-${DateTime.now().microsecondsSinceEpoch}';
    final created = jsonDecode((await http.post(Uri.parse('$_baseUrl/api/v1/products'),
            headers: authHeaders,
            body: jsonEncode({
              'name': 'Flutter open price $sku',
              'tax_slab_id': '66666666-6666-6666-6666-666666666666',
              'variants': [
                {'sku': sku, 'cost_price': 50, 'mrp': 500, 'selling_price': 120, 'generate_barcode': true}
              ],
            })))
        .body) as Map<String, dynamic>;
    final variant = (created['variants'] as List).first as Map<String, dynamic>;
    final variantId = variant['variant_id'] as String;
    final barcode = variant['generated_barcode'] as String;
    await http.post(Uri.parse('$_baseUrl/api/v1/inventory/adjustments'),
        headers: authHeaders, body: jsonEncode({'variant_id': variantId, 'branch_id': branchId, 'quantity_delta': 5, 'reason': 'flutter test stock'}));
    Future<int> setOpenPrice(Map<String, dynamic> op) async => (await http.patch(
          Uri.parse('$_baseUrl/api/v1/products/variants/$variantId'),
          headers: {'Content-Type': 'application/json', 'Authorization': 'Bearer $token'},
          body: jsonEncode({'open_price': op}),
        ))
            .statusCode;

    expect(await setOpenPrice({'enabled': true, 'min': 10, 'max': 500, 'approval_above': 200}), 200);
    try {
      final lookup = await api.lookupBarcode(barcode);
      expect(lookup.openPrice?.max, '500.00');

      var order = await api.createOrder(
          branchId: branchId, posTerminalId: terminalId, idempotencyKey: 'flutter-live-op-${DateTime.now().microsecondsSinceEpoch}');
      try {
        await api.addLine(orderId: order.orderId, variantId: variantId, quantity: 1);
        fail('expected OPEN_PRICE_REQUIRED');
      } on ApiException catch (e) {
        expect(e.code, 'OPEN_PRICE_REQUIRED');
        expect(OpenPriceRange.fromJson(e.details?['open_price'])?.approvalAbove, '200.00');
      }
      order = await api.addLine(orderId: order.orderId, variantId: variantId, quantity: 1, unitPrice: 150);
      expect(order.lines.single.unitPrice, '150.00');
      expect(order.lines.single.openPrice, isTrue);
      await api.deleteLine(orderId: order.orderId, lineId: order.lines.single.lineId);
    } finally {
      expect(await setOpenPrice({'enabled': false}), 200);
      await http.patch(Uri.parse('$_baseUrl/api/v1/products/variants/$variantId'), headers: authHeaders, body: jsonEncode({'status': 'discontinued'}));
    }
  });

  test('Phase 13: a shift opens, takes a float in, and closes on a matching count', () async {
    if (!serverUp) return;

    const branchId = '22222222-2222-2222-2222-222222222222';
    final login = await http.post(
      Uri.parse('$_baseUrl/api/v1/auth/login'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({'merchant_code': 'acme-sports', 'email': 'arjun@acme-sports.test', 'password': 'Passw0rd!'}),
    );
    final token = (jsonDecode(login.body) as Map<String, dynamic>)['access_token'] as String;
    final headers = {'Content-Type': 'application/json', 'Authorization': 'Bearer $token'};
    // A throwaway till, so nothing else's shift is touched.
    final created = await http.post(Uri.parse('$_baseUrl/api/v1/pos-terminals'),
        headers: headers, body: jsonEncode({'branch_id': branchId, 'name': 'Flutter shift ${DateTime.now().microsecondsSinceEpoch}'}));
    final terminalId = (jsonDecode(created.body) as Map<String, dynamic>)['terminal_id'] as String;
    try {
      expect(await api.currentShift(terminalId), isNull);
      var shift = await api.openShift(terminalId: terminalId, openingFloat: 1000, idempotencyKey: 'flutter-shift-${DateTime.now().microsecondsSinceEpoch}');
      expect(shift.isOpen, isTrue);
      expect(shift.report!.expectedCash, '1000.00');
      shift = await api.addCashMovement(
          shiftId: shift.shiftId, kind: 'float_in', amount: 250, reason: '', idempotencyKey: 'flutter-mv-${DateTime.now().microsecondsSinceEpoch}');
      expect(shift.report!.expectedCash, '1250.00');
      final closed = await api.closeShift(shiftId: shift.shiftId, denominations: {500: 2, 200: 1, 50: 1});
      expect(closed.status, 'closed');
      expect(closed.report!.kind, 'Z');
      expect(closed.report!.close!['variance'], '0.00');
      expect(await api.currentShift(terminalId), isNull);
    } finally {
      await http.patch(Uri.parse('$_baseUrl/api/v1/pos-terminals/$terminalId'), headers: headers, body: jsonEncode({'status': 'inactive'}));
    }
  });
}
