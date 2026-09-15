import 'dart:math';
import 'package:flutter/foundation.dart';

import '../api/api_client.dart';

/// A locally-tracked cart line for display purposes only. The backend's
/// OrderSummary (see api_client.dart) is the authoritative source for
/// totals — this exists because the current erp-core-go GetOrder endpoint
/// returns order-level aggregates (subtotal/tax/grand_total) but not the
/// individual sales_order_lines rows.
///
/// FLAGGED GAP (found while building this screen, not worked around
/// silently): phase0_1_design.md §3.4 documents GET /sales/orders/{id} as
/// returning "Full order detail," but internal/sales/handlers.go's
/// loadOrder() only selects from sales_orders, not the joined lines. The
/// app compensates by tracking what it itself just added — reasonable for
/// a single-device cart, but it means a second device (or a page refresh
/// against a cart another device opened) has no way to see line detail
/// yet. Close this by extending loadOrder to also return sales_order_lines
/// joined with product_variants for the product name/sku, or adding a
/// dedicated GET /sales/orders/{id}/lines — either is a small addition to
/// the already-proven WithTenant pattern.
class CartLineDisplay {
  final String variantId;
  final String productName;
  final String sku;
  final double quantity;
  final String unitPrice;

  CartLineDisplay({
    required this.variantId,
    required this.productName,
    required this.sku,
    required this.quantity,
    required this.unitPrice,
  });
}

/// Holds everything a single POS terminal session needs: the authenticated
/// API client, the cart currently being built, and its locally-tracked
/// line items. A ChangeNotifier so screens can listen with `context.watch`.
class AppSession extends ChangeNotifier {
  final ApiClient api;

  // TODO(phase 2): these belong to a terminal-registration/device-binding
  // flow (FRD §18 device binding), not a hardcoded constant. Wired to the
  // seed data in migrations/002_seed.sql for this walking skeleton.
  final String branchId;
  final String posTerminalId;

  AppSession({required this.api, required this.branchId, required this.posTerminalId});

  String? userId;
  List<String> roles = [];
  bool get isLoggedIn => userId != null;

  OrderSummary? currentOrder;
  final List<CartLineDisplay> cartLines = [];

  String? _lastError;
  String? get lastError => _lastError;

  Future<bool> login(String merchantCode, String email, String password) async {
    _lastError = null;
    try {
      final result = await api.login(merchantCode: merchantCode, email: email, password: password);
      userId = result.userId;
      roles = result.roles;
      notifyListeners();
      return true;
    } on ApiException catch (e) {
      _lastError = e.message;
      notifyListeners();
      return false;
    }
  }

  Future<ProductLookup?> scanBarcode(String code) async {
    _lastError = null;
    try {
      return await api.lookupBarcode(code);
    } on ApiException catch (e) {
      _lastError = e.code == 'PRODUCT_NOT_FOUND' ? 'No product matches that barcode' : e.message;
      notifyListeners();
      return null;
    }
  }

  Future<bool> addToCart(ProductLookup product, double quantity) async {
    _lastError = null;
    try {
      currentOrder ??= await api.createOrder(
        branchId: branchId,
        posTerminalId: posTerminalId,
        idempotencyKey: _newIdempotencyKey(),
      );
      final updated = await api.addLine(
        orderId: currentOrder!.orderId,
        variantId: product.variantId,
        quantity: quantity,
      );
      currentOrder = updated;
      cartLines.add(CartLineDisplay(
        variantId: product.variantId,
        productName: product.productName,
        sku: product.sku,
        quantity: quantity,
        unitPrice: product.sellingPrice,
      ));
      notifyListeners();
      return true;
    } on ApiException catch (e) {
      _lastError = e.code == 'STOCK_UNAVAILABLE'
          ? 'Not enough stock available for that quantity'
          : e.message;
      notifyListeners();
      return false;
    }
  }

  Future<bool> checkout(List<PaymentInput> payments) async {
    if (currentOrder == null) return false;
    _lastError = null;
    try {
      final finalized = await api.checkout(orderId: currentOrder!.orderId, payments: payments);
      currentOrder = finalized;
      notifyListeners();
      return true;
    } on ApiException catch (e) {
      _lastError = e.code == 'PAYMENT_MISMATCH'
          ? 'Payment total does not cover the order total'
          : e.message;
      notifyListeners();
      return false;
    }
  }

  /// Starts a fresh cart, discarding any in-progress (un-checked-out) one.
  /// Called after a successful checkout, or if the cashier voids the sale
  /// locally before it ever reached the server.
  void startNewSale() {
    currentOrder = null;
    cartLines.clear();
    notifyListeners();
  }

  String _newIdempotencyKey() {
    final rand = Random().nextInt(1 << 31).toRadixString(16);
    return '${DateTime.now().microsecondsSinceEpoch}-$rand';
  }
}
