import 'dart:convert';

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
const _baseUrl = 'http://localhost:8080';

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
}
