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

class LoginResult {
  final String accessToken;
  final String userId;
  final List<String> roles;

  LoginResult({required this.accessToken, required this.userId, required this.roles});

  factory LoginResult.fromJson(Map<String, dynamic> json) => LoginResult(
        accessToken: json['access_token'] as String,
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

/// Mirrors the backend's orderResponse shape exactly (internal/sales/handlers.go).
/// Money fields are deliberately kept as Dart Strings, never parsed to
/// double, all the way out to the receipt screen — the backend already
/// settled these to NUMERIC(14,2)-correct values server-side (see the
/// float64-for-money note in erp-core-go), and re-parsing to a Dart double
/// here would just reintroduce the exact binary-float risk that was
/// deliberately contained on the server. Display them as-is; if this
/// screen ever needs to do arithmetic on them (e.g. a running total before
/// the server confirms it), use a fixed-point/decimal package, not double.
class OrderSummary {
  final String orderId;
  final String orderNumber;
  final String status;
  final String subtotal;
  final String discountTotal;
  final String taxTotal;
  final String grandTotal;

  OrderSummary({
    required this.orderId,
    required this.orderNumber,
    required this.status,
    required this.subtotal,
    required this.discountTotal,
    required this.taxTotal,
    required this.grandTotal,
  });

  factory OrderSummary.fromJson(Map<String, dynamic> json) => OrderSummary(
        orderId: json['order_id'] as String,
        orderNumber: json['order_number'] as String,
        status: json['status'] as String,
        subtotal: json['subtotal'] as String,
        discountTotal: json['discount_total'] as String,
        taxTotal: json['tax_total'] as String,
        grandTotal: json['grand_total'] as String,
      );
}

class PaymentInput {
  final String method; // 'cash' | 'card' | 'upi'
  final double amount;

  PaymentInput({required this.method, required this.amount});

  Map<String, dynamic> toJson() => {'method': method, 'amount': amount};
}

/// Talks to erp-core-go's /api/v1 surface, exactly as specified in
/// phase0_1_design.md §3 and implemented (and verified against live
/// Postgres) in internal/authn, internal/catalog, internal/sales.
///
/// NOT independently verified: this file could not be compiled, analyzed,
/// or run in the sandbox that wrote it — no Flutter/Dart SDK was available
/// and the network path to install one was blocked by the same
/// organizational egress policy that blocked the Go module proxy (see
/// erp-core-go's README for that precedent). Every field name and JSON
/// shape here was written to match the server code byte-for-byte, but
/// `flutter analyze` has not actually checked that. Run it before trusting
/// this beyond "carefully written."
class ApiClient {
  final String baseUrl;
  final http.Client _http;
  String? _accessToken;

  ApiClient({required this.baseUrl, http.Client? httpClient}) : _http = httpClient ?? http.Client();

  void setAccessToken(String token) => _accessToken = token;

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
    _accessToken = result.accessToken;
    return result;
  }

  Future<ProductLookup> lookupBarcode(String code) async {
    final resp = await _http.get(
      Uri.parse('$baseUrl/api/v1/products/barcode/$code'),
      headers: _authHeaders,
    );
    return ProductLookup.fromJson(_decode(resp));
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
