import 'dart:convert';
import 'package:http/http.dart' as http;

/// Thrown for any non-2xx response. Carries the error `code` from the
/// backend's standard `{ "error": { "code", "message" } }` shape (see
/// phase0_1_design.md §3) so the UI can react to specific cases — e.g.
/// STOCK_UNAVAILABLE vs a generic failure — without string-matching
/// messages.
class ApiException implements Exception {
  final int statusCode;
  final String code;
  final String message;

  ApiException(this.statusCode, this.code, this.message);

  @override
  String toString() => 'ApiException($statusCode, $code): $message';
}

/// Why a session ended — drives the message LoginScreen shows.
enum SessionEndReason { idle, revoked }

enum RefreshOutcome { ok, rejected, unreachable }

String sessionEndedMessage(SessionEndReason reason) => reason == SessionEndReason.idle
    ? 'Your session expired due to inactivity. Please sign in again.'
    : 'Your session has ended. Please sign in again.';

class LoginResult {
  final String accessToken;
  final String? refreshToken; // absent from older server responses; nullable for that reason
  final int? expiresIn; // seconds
  final String userId;
  final List<String> roles;

  LoginResult({
    required this.accessToken,
    this.refreshToken,
    this.expiresIn,
    required this.userId,
    required this.roles,
  });

  factory LoginResult.fromJson(Map<String, dynamic> json) => LoginResult(
        accessToken: json['access_token'] as String,
        refreshToken: json['refresh_token'] as String?,
        expiresIn: json['expires_in'] as int?,
        userId: json['user_id'] as String,
        roles: (json['roles'] as List<dynamic>? ?? const []).cast<String>(),
      );
}

class ProductLookup {
  final String productId;
  final String productName;
  final String variantId;
  final String sku;
  final String sellingPrice; // kept as the backend's string form — see money-as-string note below
  final String mrp;

  ProductLookup({
    required this.productId,
    required this.productName,
    required this.variantId,
    required this.sku,
    required this.sellingPrice,
    required this.mrp,
  });

  factory ProductLookup.fromJson(Map<String, dynamic> json) => ProductLookup(
        productId: json['product_id'] as String,
        productName: json['product_name'] as String,
        variantId: json['variant_id'] as String,
        sku: json['sku'] as String,
        sellingPrice: json['selling_price'] as String,
        mrp: json['mrp'] as String,
      );
}

/// Mirrors internal/catalog/taxonomy.go's categoryResponse — a flat list
/// with a self-referencing parent_id, the same materialized-hierarchy
/// shape the backend already exposes. `null` parentId means a top-level
/// category; anything else is a subcategory of that id. No separate
/// "subcategory" type exists on the backend — this app derives the
/// distinction purely from parentId, same as erp-web-admin would if it
/// ever needed a category tree.
class CatalogCategory {
  final String categoryId;
  final String? parentId;
  final String name;
  final String path;

  CatalogCategory({required this.categoryId, this.parentId, required this.name, required this.path});

  factory CatalogCategory.fromJson(Map<String, dynamic> json) => CatalogCategory(
        categoryId: json['category_id'] as String,
        parentId: json['parent_id'] as String?,
        name: json['name'] as String,
        path: json['path'] as String? ?? '',
      );
}

/// One OpenSearch hit from GET /products/search — mirrors
/// internal/search.Document exactly. Unlike every other money field in
/// this file, selling_price/mrp arrive as JSON numbers here, not strings:
/// they come straight from OpenSearch's own document, never through a
/// Postgres NUMERIC::text cast, so there's no "string all the way down"
/// convention to preserve on this one path. toProductLookup() re-stringifies
/// them (fixed to 2dp) purely to reuse AppSession.addToCart's existing
/// ProductLookup-shaped call site — not a claim that a double ever holds
/// this value for arithmetic.
class ProductSearchResult {
  final String variantId;
  final String productId;
  final String name;
  final String sku;
  final String? categoryId;
  final String? categoryName;
  final double sellingPrice;
  final double mrp;

  ProductSearchResult({
    required this.variantId,
    required this.productId,
    required this.name,
    required this.sku,
    this.categoryId,
    this.categoryName,
    required this.sellingPrice,
    required this.mrp,
  });

  factory ProductSearchResult.fromJson(Map<String, dynamic> json) => ProductSearchResult(
        variantId: json['variant_id'] as String,
        productId: json['product_id'] as String,
        name: json['name'] as String,
        sku: json['sku'] as String,
        categoryId: json['category_id'] as String?,
        categoryName: json['category_name'] as String?,
        sellingPrice: (json['selling_price'] as num).toDouble(),
        mrp: (json['mrp'] as num).toDouble(),
      );

  ProductLookup toProductLookup() => ProductLookup(
        productId: productId,
        productName: name,
        variantId: variantId,
        sku: sku,
        sellingPrice: sellingPrice.toStringAsFixed(2),
        mrp: mrp.toStringAsFixed(2),
      );
}

/// One sales_order_lines row, joined server-side with product_variants for
/// display fields (name/sku) — see internal/sales/handlers.go's
/// loadOrderLines. Money/quantity fields are strings for the same reason as
/// OrderSummary's totals below.
class OrderLine {
  final String lineId;
  final String variantId;
  final String sku;
  final String productName;
  final String quantity;
  final String unitPrice;
  final String discountAmount;
  final String taxAmount;
  final String lineTotal;

  OrderLine({
    required this.lineId,
    required this.variantId,
    required this.sku,
    required this.productName,
    required this.quantity,
    required this.unitPrice,
    required this.discountAmount,
    required this.taxAmount,
    required this.lineTotal,
  });

  factory OrderLine.fromJson(Map<String, dynamic> json) => OrderLine(
        lineId: json['line_id'] as String,
        variantId: json['variant_id'] as String,
        sku: json['sku'] as String,
        productName: json['product_name'] as String,
        quantity: json['quantity'] as String,
        unitPrice: json['unit_price'] as String,
        discountAmount: json['discount_amount'] as String,
        taxAmount: json['tax_amount'] as String,
        lineTotal: json['line_total'] as String,
      );
}

/// Mirrors the backend's orderResponse shape exactly (internal/sales/handlers.go).
/// Money fields are deliberately kept as Dart Strings, never parsed to
/// double, all the way out to the receipt screen — the backend already
/// settled these to NUMERIC(14,2)-correct values server-side (see the
/// float64-for-money note in erp-core-go), and re-parsing to a Dart double
/// here would just reintroduce the exact binary-float risk that was
/// deliberately contained on the server. Display them as-is; if this
/// screen ever needs to do arithmetic on them (e.g. a running total before
/// the server confirms it), use a fixed-point/decimal package, not double.
///
/// `lines` used to be absent from this response (see the FLAGGED GAP note
/// that was in state/session.dart) — the backend now joins them in, so the
/// app no longer needs to track cart lines locally per-device.
class OrderSummary {
  final String orderId;
  final String orderNumber;
  final String status;
  final String subtotal;
  final String discountTotal;
  final String taxTotal;
  final String grandTotal;
  final List<OrderLine> lines;

  OrderSummary({
    required this.orderId,
    required this.orderNumber,
    required this.status,
    required this.subtotal,
    required this.discountTotal,
    required this.taxTotal,
    required this.grandTotal,
    this.lines = const [],
  });

  factory OrderSummary.fromJson(Map<String, dynamic> json) => OrderSummary(
        orderId: json['order_id'] as String,
        orderNumber: json['order_number'] as String,
        status: json['status'] as String,
        subtotal: json['subtotal'] as String,
        discountTotal: json['discount_total'] as String,
        taxTotal: json['tax_total'] as String,
        grandTotal: json['grand_total'] as String,
        lines: (json['lines'] as List<dynamic>? ?? const [])
            .map((e) => OrderLine.fromJson(e as Map<String, dynamic>))
            .toList(),
      );
}

/// A line Recall couldn't get stock back for — see internal/sales/hold.go's
/// doc comment. Never silently dropped: the cashier decides what to do
/// with it (remove/adjust before checkout).
class UnavailableLine {
  final String lineId;
  final String variantId;
  final String reason;

  UnavailableLine({required this.lineId, required this.variantId, required this.reason});

  factory UnavailableLine.fromJson(Map<String, dynamic> json) => UnavailableLine(
        lineId: json['line_id'] as String,
        variantId: json['variant_id'] as String,
        reason: json['reason'] as String,
      );
}

/// POST /sales/orders/{id}/recall's response — a normal orderResponse plus
/// an optional list of lines whose stock hold didn't survive.
class RecallResult {
  final OrderSummary order;
  final List<UnavailableLine> unavailableLines;

  RecallResult({required this.order, this.unavailableLines = const []});
}

/// One row from GET /sales/orders/held — the recall screen's list.
class HeldOrderSummary {
  final String orderId;
  final String orderNumber;
  final String heldAt;
  final String holdNote;
  final int lineCount;
  final String grandTotal;

  HeldOrderSummary({
    required this.orderId,
    required this.orderNumber,
    required this.heldAt,
    required this.holdNote,
    required this.lineCount,
    required this.grandTotal,
  });

  factory HeldOrderSummary.fromJson(Map<String, dynamic> json) => HeldOrderSummary(
        orderId: json['order_id'] as String,
        orderNumber: json['order_number'] as String,
        heldAt: json['held_at'] as String,
        holdNote: json['hold_note'] as String? ?? '',
        lineCount: json['line_count'] as int? ?? 0,
        grandTotal: json['grand_total'] as String,
      );
}

class PaymentInput {
  final String method; // 'cash' | 'card' | 'upi' | 'credit'
  final double amount;
  final String? reference; // card/UPI gateway transaction id (UTR), if the terminal surfaces one — see erp-core-go's Payment Gateway Reconciliation (phase0_1_design.md §3.17)

  PaymentInput({required this.method, required this.amount, this.reference});

  Map<String, dynamic> toJson() => {
        'method': method,
        'amount': amount,
        if (reference != null && reference!.isNotEmpty) 'reference': reference,
      };
}

/// Mirrors internal/customers.customerResponse's fields this app actually
/// needs — a customer picker doesn't need the full profile (address,
/// GSTIN, purchase history), just enough to search, display, and know
/// whether attaching them for a credit sale is even possible.
class Customer {
  final String customerId;
  final String name;
  final String phone;
  final String email;
  final bool creditHold;

  Customer({
    required this.customerId,
    required this.name,
    required this.phone,
    required this.email,
    required this.creditHold,
  });

  factory Customer.fromJson(Map<String, dynamic> json) => Customer(
        customerId: json['customer_id'] as String,
        name: json['name'] as String? ?? '',
        phone: json['phone'] as String? ?? '',
        email: json['email'] as String? ?? '',
        creditHold: json['credit_hold'] as bool? ?? false,
      );
}

/// Mirrors internal/loyalty's balanceResponse (available_points only — the
/// full ledger is a back-office/admin concern, erp-web-admin's job, not
/// this terminal's).
class LoyaltyBalance {
  final String customerId;
  final int availablePoints;

  LoyaltyBalance({required this.customerId, required this.availablePoints});

  factory LoyaltyBalance.fromJson(Map<String, dynamic> json) => LoyaltyBalance(
        customerId: json['customer_id'] as String,
        availablePoints: json['available_points'] as int? ?? 0,
      );
}

class ReceiptLineData {
  final String productName;
  final String sku;
  final String quantity;
  final String unitPrice;
  final String lineTotal;

  ReceiptLineData({
    required this.productName,
    required this.sku,
    required this.quantity,
    required this.unitPrice,
    required this.lineTotal,
  });

  factory ReceiptLineData.fromJson(Map<String, dynamic> json) => ReceiptLineData(
        productName: json['product_name'] as String,
        sku: json['sku'] as String,
        quantity: json['quantity'] as String,
        unitPrice: json['unit_price'] as String,
        lineTotal: json['line_total'] as String,
      );
}

class ReceiptPaymentData {
  final String method;
  final String amount;

  ReceiptPaymentData({required this.method, required this.amount});

  factory ReceiptPaymentData.fromJson(Map<String, dynamic> json) => ReceiptPaymentData(
        method: json['method'] as String,
        amount: json['amount'] as String,
      );
}

/// Mirrors internal/sales/receipt.go's receiptResponse — the print-ready
/// payload GET /sales/orders/{id}/receipt returns. This app renders it as
/// a real on-screen receipt (ReceiptScreen) rather than only the plain
/// "sale complete" dialog the walking-skeleton version showed, and
/// GET .../receipt/print's raw ESC/POS bytes (see ApiClient.printReceiptBytes)
/// feed an actual network-printer send when one's configured — see
/// PrinterService's own doc comment for exactly what "verified" means here.
class ReceiptData {
  final String orderNumber;
  final String status;
  final String? finalizedAt;
  final String merchantName;
  final String branchName;
  final String cashierName;
  final String? customerName;
  final List<ReceiptLineData> lines;
  final String subtotal;
  final String discountTotal;
  final String taxTotal;
  final String grandTotal;
  final List<ReceiptPaymentData> payments;

  ReceiptData({
    required this.orderNumber,
    required this.status,
    required this.finalizedAt,
    required this.merchantName,
    required this.branchName,
    required this.cashierName,
    required this.customerName,
    required this.lines,
    required this.subtotal,
    required this.discountTotal,
    required this.taxTotal,
    required this.grandTotal,
    required this.payments,
  });

  factory ReceiptData.fromJson(Map<String, dynamic> json) => ReceiptData(
        orderNumber: json['order_number'] as String,
        status: json['status'] as String,
        finalizedAt: json['finalized_at'] as String?,
        merchantName: json['merchant_name'] as String,
        branchName: json['branch_name'] as String,
        cashierName: json['cashier_name'] as String,
        customerName: json['customer_name'] as String?,
        lines: (json['lines'] as List<dynamic>? ?? const [])
            .map((e) => ReceiptLineData.fromJson(e as Map<String, dynamic>))
            .toList(),
        subtotal: json['subtotal'] as String,
        discountTotal: json['discount_total'] as String,
        taxTotal: json['tax_total'] as String,
        grandTotal: json['grand_total'] as String,
        payments: (json['payments'] as List<dynamic>? ?? const [])
            .map((e) => ReceiptPaymentData.fromJson(e as Map<String, dynamic>))
            .toList(),
      );
}

/// Talks to erp-core-go's /api/v1 surface, exactly as specified in
/// phase0_1_design.md §3 and implemented (and verified against live
/// Postgres) in internal/authn, internal/catalog, internal/sales.
///
/// `flutter analyze` and `flutter test` both run clean against this file as
/// of the Phase 1 hardening pass — the original sandbox that wrote the
/// first version of this file had no Flutter/Dart SDK available (same
/// organizational egress policy that blocked the Go module proxy), so this
/// is the first real verification it's had. Field names/JSON shapes are
/// checked against a running server manually, not by an automated
/// integration test — there still isn't one.
class ApiClient {
  final String baseUrl;
  // Raw transport — only used directly for POST /auth/refresh, so a
  // refresh can never recurse through the retrying client below.
  final http.Client _inner;
  // Every other call goes through this: it injects the current token and,
  // on a rejected token, refreshes once and replays the request.
  late final http.Client _http = _AuthRetryingClient(_inner, this);

  String? _accessToken;
  String? _refreshToken;
  DateTime? _accessExpiresAt;

  /// The role tier's session window (POS User 15 min, Branch Manager 30,
  /// Merchant Admin 60 — erp-core-go's internal/authn/session_tiers.go).
  /// It's both the access token's lifetime and the inactivity timeout.
  Duration? sessionWindow;

  /// Set by AppSession: false once the user has been idle for a full
  /// session window, so a rejected token ends the session instead of being
  /// silently refreshed (refreshing an idle session would turn the FRD's
  /// 15-minute inactivity timeout into the refresh token's 30 days).
  bool Function() canRefresh = () => true;

  /// Called once when the session can't continue — see AppSession.
  void Function(SessionEndReason reason)? onSessionExpired;

  Future<RefreshOutcome>? _refreshInFlight;

  ApiClient({required this.baseUrl, http.Client? httpClient}) : _inner = httpClient ?? http.Client();

  void setAccessToken(String token) => _accessToken = token;

  bool get hasSession => _accessToken != null;

  bool get accessTokenExpiringSoon {
    final exp = _accessExpiresAt;
    return exp == null || exp.difference(DateTime.now()) < const Duration(seconds: 60);
  }

  void _storeTokens(LoginResult r) {
    _accessToken = r.accessToken;
    if (r.refreshToken != null) _refreshToken = r.refreshToken;
    final window = Duration(seconds: r.expiresIn ?? 15 * 60);
    sessionWindow = window;
    _accessExpiresAt = DateTime.now().add(window);
  }

  void clearSession() {
    _accessToken = null;
    _refreshToken = null;
    _accessExpiresAt = null;
    sessionWindow = null;
  }

  /// Ends the session (idempotent) and tells AppSession why.
  void expireSession(SessionEndReason reason) {
    if (!hasSession) return;
    clearSession();
    onSessionExpired?.call(reason);
  }

  /// POST /auth/refresh — single-flight, since the backend rotates refresh
  /// tokens (the old one is revoked on use) and two concurrent refreshes
  /// with the same token would make the second look revoked.
  /// A network failure is `unreachable`, never `rejected`: this is an
  /// offline-first POS, and losing connectivity must not log a cashier out.
  Future<RefreshOutcome> refresh() {
    return _refreshInFlight ??= _doRefresh().whenComplete(() => _refreshInFlight = null);
  }

  Future<RefreshOutcome> _doRefresh() async {
    final token = _refreshToken;
    if (token == null) return RefreshOutcome.rejected;
    try {
      final resp = await _inner
          .post(
            Uri.parse('$baseUrl/api/v1/auth/refresh'),
            headers: {'Content-Type': 'application/json'},
            body: jsonEncode({'refresh_token': token}),
          )
          .timeout(const Duration(seconds: 15));
      if (resp.statusCode == 401 || resp.statusCode == 403) return RefreshOutcome.rejected;
      if (resp.statusCode < 200 || resp.statusCode >= 300) return RefreshOutcome.unreachable;
      _storeTokens(LoginResult.fromJson(jsonDecode(resp.body) as Map<String, dynamic>));
      return RefreshOutcome.ok;
    } catch (_) {
      return RefreshOutcome.unreachable;
    }
  }

  Map<String, String> get _authHeaders => {
        'Content-Type': 'application/json',
        if (_accessToken != null) 'Authorization': 'Bearer $_accessToken',
      };

  Future<LoginResult> login({
    required String merchantCode,
    required String email,
    required String password,
  }) async {
    final resp = await _http.post(
      Uri.parse('$baseUrl/api/v1/auth/login'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({'merchant_code': merchantCode, 'email': email, 'password': password}),
    );
    final result = LoginResult.fromJson(_decode(resp));
    _storeTokens(result);
    return result;
  }

  Future<LoginResult> pinLogin({
    required String posTerminalId,
    required String employeeCode,
    required String pin,
    required String deviceFingerprint,
  }) async {
    final resp = await _http.post(
      Uri.parse('$baseUrl/api/v1/auth/pin-login'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({
        'pos_terminal_id': posTerminalId,
        'employee_code': employeeCode,
        'pin': pin,
        'device_fingerprint': deviceFingerprint,
      }),
    );
    final result = LoginResult.fromJson(_decode(resp));
    _storeTokens(result);
    return result;
  }

  Future<ProductLookup> lookupBarcode(String code) async {
    final resp = await _http.get(
      Uri.parse('$baseUrl/api/v1/products/barcode/$code'),
      headers: _authHeaders,
    );
    return ProductLookup.fromJson(_decode(resp));
  }

  Future<List<CatalogCategory>> listCategories() async {
    final resp = await _http.get(Uri.parse('$baseUrl/api/v1/categories'), headers: _authHeaders);
    final body = _decode(resp);
    return (body['categories'] as List<dynamic>)
        .map((e) => CatalogCategory.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Name/category/subcategory browse for the cart's "look it up, don't
  /// just scan it" path (erp-core-go's internal/search — OpenSearch-backed,
  /// fuzzy/typo-tolerant on name/sku/category/brand). An empty `query`
  /// with a `categoryId` set is a valid, common call shape — it means
  /// "browse this whole category," not "search for nothing" (the backend
  /// falls back to match-all when query is blank).
  Future<List<ProductSearchResult>> searchProducts({String query = '', String? categoryId, int limit = 50}) async {
    final params = <String, String>{'limit': '$limit'};
    if (query.isNotEmpty) params['q'] = query;
    if (categoryId != null && categoryId.isNotEmpty) params['category_id'] = categoryId;
    final uri = Uri.parse('$baseUrl/api/v1/products/search').replace(queryParameters: params);
    final resp = await _http.get(uri, headers: _authHeaders);
    final body = _decode(resp);
    return (body['results'] as List<dynamic>)
        .map((e) => ProductSearchResult.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<OrderSummary> createOrder({
    required String branchId,
    required String posTerminalId,
    required String idempotencyKey,
  }) async {
    final resp = await _http.post(
      Uri.parse('$baseUrl/api/v1/sales/orders'),
      headers: _authHeaders,
      body: jsonEncode({
        'branch_id': branchId,
        'pos_terminal_id': posTerminalId,
        'idempotency_key': idempotencyKey,
      }),
    );
    return OrderSummary.fromJson(_decode(resp));
  }

  Future<OrderSummary> addLine({
    required String orderId,
    required String variantId,
    required double quantity,
  }) async {
    final resp = await _http.post(
      Uri.parse('$baseUrl/api/v1/sales/orders/$orderId/lines'),
      headers: _authHeaders,
      body: jsonEncode({'variant_id': variantId, 'quantity': quantity}),
    );
    return OrderSummary.fromJson(_decode(resp));
  }

  Future<OrderSummary> deleteLine({
    required String orderId,
    required String lineId,
  }) async {
    final resp = await _http.delete(
      Uri.parse('$baseUrl/api/v1/sales/orders/$orderId/lines/$lineId'),
      headers: _authHeaders,
    );
    return OrderSummary.fromJson(_decode(resp));
  }

  Future<OrderSummary> checkout({
    required String orderId,
    required List<PaymentInput> payments,
  }) async {
    final resp = await _http.post(
      Uri.parse('$baseUrl/api/v1/sales/orders/$orderId/checkout'),
      headers: _authHeaders,
      body: jsonEncode({'payments': payments.map((p) => p.toJson()).toList()}),
    );
    return OrderSummary.fromJson(_decode(resp));
  }

  Future<OrderSummary> getOrder(String orderId) async {
    final resp = await _http.get(
      Uri.parse('$baseUrl/api/v1/sales/orders/$orderId'),
      headers: _authHeaders,
    );
    return OrderSummary.fromJson(_decode(resp));
  }

  Future<OrderSummary> updateLineQuantity({
    required String orderId,
    required String lineId,
    required double quantity,
  }) async {
    final resp = await _http.patch(
      Uri.parse('$baseUrl/api/v1/sales/orders/$orderId/lines/$lineId'),
      headers: _authHeaders,
      body: jsonEncode({'quantity': quantity}),
    );
    return OrderSummary.fromJson(_decode(resp));
  }

  /// {customer_id} to attach an existing customer, or {name, phone, email}
  /// to create-and-attach a walk-in — see internal/sales/customer.go.
  /// Required before a `credit` payment method at checkout (the backend
  /// rejects a credit payment with no customer attached), and before
  /// loyalty points can be looked up/redeemed for this sale.
  Future<String> attachCustomer({
    required String orderId,
    String? customerId,
    String? name,
    String? phone,
    String? email,
  }) async {
    final resp = await _http.post(
      Uri.parse('$baseUrl/api/v1/sales/orders/$orderId/customer'),
      headers: _authHeaders,
      body: jsonEncode({
        if (customerId != null) 'customer_id': customerId,
        if (name != null) 'name': name,
        if (phone != null) 'phone': phone,
        if (email != null) 'email': email,
      }),
    );
    return _decode(resp)['customer_id'] as String;
  }

  Future<List<Customer>> searchCustomers(String query) async {
    final uri = Uri.parse('$baseUrl/api/v1/customers').replace(queryParameters: {'q': query, 'limit': '20'});
    final resp = await _http.get(uri, headers: _authHeaders);
    final body = _decode(resp);
    return (body['customers'] as List<dynamic>).map((e) => Customer.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<LoyaltyBalance> getCustomerLoyalty(String customerId) async {
    final resp = await _http.get(
      Uri.parse('$baseUrl/api/v1/customers/$customerId/loyalty'),
      headers: _authHeaders,
    );
    return LoyaltyBalance.fromJson(_decode(resp));
  }

  /// The discount hierarchy's manual layer (pos_frd_complete.md §5, item
  /// 6) — see internal/sales/discounts.go. `authorizedBy`/`authorizedPin`
  /// only matter above the 5% no-approval tier; the server is the one
  /// true authority on the exact tier boundaries (0-5/5-15/15-25%), this
  /// client just mirrors them so the approval fields can be shown/hidden
  /// without a round trip.
  Future<OrderSummary> applyDiscount({
    required String orderId,
    required double valuePercent,
    String authorizedBy = '',
    String authorizedPin = '',
    required String reason,
  }) async {
    final resp = await _http.post(
      Uri.parse('$baseUrl/api/v1/sales/orders/$orderId/discounts'),
      headers: _authHeaders,
      body: jsonEncode({
        'type': 'manual',
        'value': valuePercent,
        'authorized_by': authorizedBy,
        'authorized_pin': authorizedPin,
        'reason': reason,
      }),
    );
    return OrderSummary.fromJson(_decode(resp));
  }

  /// Evaluates and auto-applies every eligible promotion against the cart
  /// as it stands right now — see internal/promotions/apply.go. Safe to
  /// call more than once (it's a re-evaluation, not a toggle); the FRD
  /// names no "undo" for an auto-applied promotion.
  /// Unlike every other cart-mutating endpoint, this one's response isn't
  /// a bare orderResponse — it's `{applied: [...], order: <orderResponse
  /// | null>}` (internal/promotions/apply.go), `order` staying `null`
  /// when nothing matched (an empty cart, or no active promotion is
  /// currently eligible). Falls back to a fresh GET when that happens, so
  /// callers always get the order back either way — found live: the
  /// naive `OrderSummary.fromJson` directly on the response body threw
  /// a null-cast error the very first time this ran against a real cart
  /// with no matching promotions.
  Future<OrderSummary> applyPromotions(String orderId) async {
    final resp = await _http.post(
      Uri.parse('$baseUrl/api/v1/sales/orders/$orderId/promotions/apply'),
      headers: _authHeaders,
      body: jsonEncode({}),
    );
    final body = _decode(resp);
    final order = body['order'] as Map<String, dynamic>?;
    if (order != null) return OrderSummary.fromJson(order);
    return getOrder(orderId);
  }

  /// Wrapped the same way applyPromotions is — `{discount_amount, order}`
  /// (internal/promotions/coupons.go), not a bare orderResponse. Unlike
  /// promotions, `order` is never null here (a successful coupon apply
  /// always resolves a real discount amount first), but unwrapping the
  /// same way keeps both call sites consistent rather than assuming.
  Future<OrderSummary> applyCoupon({required String orderId, required String code}) async {
    final resp = await _http.post(
      Uri.parse('$baseUrl/api/v1/sales/orders/$orderId/coupons'),
      headers: _authHeaders,
      body: jsonEncode({'code': code}),
    );
    final body = _decode(resp);
    final order = body['order'] as Map<String, dynamic>?;
    if (order != null) return OrderSummary.fromJson(order);
    return getOrder(orderId);
  }

  /// Wrapped like applyCoupon — `{discount_amount, order}`
  /// (internal/loyalty/handlers.go's Redeem).
  Future<OrderSummary> redeemLoyalty({required String orderId, required int points}) async {
    final resp = await _http.post(
      Uri.parse('$baseUrl/api/v1/sales/orders/$orderId/loyalty/redeem'),
      headers: _authHeaders,
      body: jsonEncode({'points': points}),
    );
    final body = _decode(resp);
    final order = body['order'] as Map<String, dynamic>?;
    if (order != null) return OrderSummary.fromJson(order);
    return getOrder(orderId);
  }

  Future<OrderSummary> voidOrder({required String orderId, required String reason}) async {
    final resp = await _http.post(
      Uri.parse('$baseUrl/api/v1/sales/orders/$orderId/void'),
      headers: _authHeaders,
      body: jsonEncode({'reason': reason}),
    );
    return OrderSummary.fromJson(_decode(resp));
  }

  /// Parks the current cart — see internal/sales/hold.go's Hold. Extends
  /// its reservations well past the ordinary 15-minute cart window
  /// server-side; this client doesn't need to know the exact duration.
  Future<OrderSummary> holdOrder({required String orderId, String? note}) async {
    final resp = await _http.post(
      Uri.parse('$baseUrl/api/v1/sales/orders/$orderId/hold'),
      headers: _authHeaders,
      body: jsonEncode({if (note != null && note.isNotEmpty) 'note': note}),
    );
    return OrderSummary.fromJson(_decode(resp));
  }

  /// Brings a held cart back into an editable one — see hold.go's Recall.
  /// `unavailable_lines` (never silently dropped) surfaces any line whose
  /// stock hold didn't survive.
  Future<RecallResult> recallOrder(String orderId) async {
    final resp = await _http.post(
      Uri.parse('$baseUrl/api/v1/sales/orders/$orderId/recall'),
      headers: _authHeaders,
    );
    final body = _decode(resp);
    return RecallResult(
      order: OrderSummary.fromJson(body),
      unavailableLines: (body['unavailable_lines'] as List<dynamic>? ?? const [])
          .map((e) => UnavailableLine.fromJson(e as Map<String, dynamic>))
          .toList(),
    );
  }

  Future<List<HeldOrderSummary>> listHeldOrders(String branchId) async {
    final uri = Uri.parse('$baseUrl/api/v1/sales/orders/held').replace(queryParameters: {'branch_id': branchId});
    final resp = await _http.get(uri, headers: _authHeaders);
    final body = _decode(resp);
    return (body['held_orders'] as List<dynamic>)
        .map((e) => HeldOrderSummary.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<ReceiptData> getReceipt(String orderId) async {
    final resp = await _http.get(
      Uri.parse('$baseUrl/api/v1/sales/orders/$orderId/receipt'),
      headers: _authHeaders,
    );
    return ReceiptData.fromJson(_decode(resp));
  }

  /// Raw ESC/POS bytes (internal/printing.BuildReceipt) — not JSON, so
  /// this bypasses `_decode` and reads the response body directly.
  Future<List<int>> printReceiptBytes(String orderId) async {
    final resp = await _http.get(
      Uri.parse('$baseUrl/api/v1/sales/orders/$orderId/receipt/print'),
      headers: _authHeaders,
    );
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw ApiException(resp.statusCode, 'PRINT_FAILED', 'could not fetch printable receipt');
    }
    return resp.bodyBytes;
  }

  Future<Map<String, dynamic>> syncPull({required String branchId, String? since}) async {
    final uri = Uri.parse('$baseUrl/api/v1/sync/pull').replace(queryParameters: {
      'branch_id': branchId,
      if (since != null) 'since': since,
    });
    final resp = await _http.get(uri, headers: _authHeaders);
    return _decode(resp);
  }

  Future<Map<String, dynamic>> syncPush(List<Map<String, dynamic>> orders) async {
    final resp = await _http.post(
      Uri.parse('$baseUrl/api/v1/sync/push'),
      headers: _authHeaders,
      body: jsonEncode({'orders': orders}),
    );
    return _decode(resp);
  }

  Map<String, dynamic> _decode(http.Response resp) {
    final body = resp.body.isEmpty ? <String, dynamic>{} : jsonDecode(resp.body) as Map<String, dynamic>;
    if (resp.statusCode >= 200 && resp.statusCode < 300) {
      return body;
    }
    final err = body['error'] as Map<String, dynamic>? ?? const {};
    throw ApiException(
      resp.statusCode,
      err['code'] as String? ?? 'UNKNOWN_ERROR',
      err['message'] as String? ?? 'request failed with status ${resp.statusCode}',
    );
  }
}

/// Wraps every authenticated ApiClient call. Injects the CURRENT access
/// token at send time (so a request queued behind a refresh uses the new
/// one), and when the auth middleware rejects the token (401 with
/// INVALID_TOKEN/MISSING_TOKEN — never a business-level error):
///   - user idle for a full session window -> end the session
///   - otherwise -> refresh once and replay the request
///   - refresh rejected -> end the session
///   - refresh unreachable (offline) -> surface the original 401, keep the
///     session; the offline-first paths in AppSession take over
/// A session that ends here is reported as a synthetic 401
/// SESSION_EXPIRED, so callers' existing ApiException handling just works.
class _AuthRetryingClient extends http.BaseClient {
  final http.Client _inner;
  final ApiClient _api;

  _AuthRetryingClient(this._inner, this._api);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    if (request.url.path.contains('/api/v1/auth/') || !_api.hasSession) return _inner.send(request);

    // Buffered so the request can be replayed after a refresh.
    final bodyBytes = await request.finalize().toBytes();
    Future<http.StreamedResponse> attempt() {
      final copy = http.Request(request.method, request.url)
        ..headers.addAll(request.headers)
        ..bodyBytes = bodyBytes;
      if (_api._accessToken != null) copy.headers['Authorization'] = 'Bearer ${_api._accessToken}';
      return _inner.send(copy);
    }

    final resp = await attempt();
    if (resp.statusCode != 401) return resp;

    final body = await resp.stream.toBytes();
    String? code;
    try {
      code = ((jsonDecode(utf8.decode(body)) as Map<String, dynamic>)['error'] as Map<String, dynamic>?)?['code'] as String?;
    } catch (_) {}
    final original = http.StreamedResponse(http.ByteStream.fromBytes(body), 401, headers: resp.headers, request: resp.request);
    if (code != 'INVALID_TOKEN' && code != 'MISSING_TOKEN') return original;

    if (!_api.canRefresh()) return _expired(SessionEndReason.idle);
    switch (await _api.refresh()) {
      case RefreshOutcome.ok:
        final retry = await attempt();
        return retry.statusCode == 401 ? _expired(SessionEndReason.revoked) : retry;
      case RefreshOutcome.rejected:
        return _expired(SessionEndReason.revoked);
      case RefreshOutcome.unreachable:
        return original;
    }
  }

  http.StreamedResponse _expired(SessionEndReason reason) {
    _api.expireSession(reason);
    final body = utf8.encode(jsonEncode({
      'error': {'code': 'SESSION_EXPIRED', 'message': sessionEndedMessage(reason)}
    }));
    return http.StreamedResponse(http.ByteStream.fromBytes(body), 401, headers: {'content-type': 'application/json'});
  }

  @override
  void close() => _inner.close();
}
