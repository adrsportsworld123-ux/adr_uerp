import 'dart:math';
import 'package:flutter/foundation.dart';

import '../api/api_client.dart';

/// Holds everything a single POS terminal session needs: the authenticated
/// API client and the cart currently being built. A ChangeNotifier so
/// screens can listen with `context.watch`.
///
/// Cart lines used to be tracked locally per-device (see git history for
/// the removed CartLineDisplay) because GET /sales/orders/{id} returned
/// order-level aggregates only — that gap is closed now (loadOrder joins
/// sales_order_lines), so `currentOrder.lines` from the server is the only
/// source of truth, which also fixes the original problem: a second device
/// reloading someone else's cart now sees real line detail, not nothing.
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

  Future<bool> removeLine(String lineId) async {
    if (currentOrder == null) return false;
    _lastError = null;
    try {
      currentOrder = await api.deleteLine(orderId: currentOrder!.orderId, lineId: lineId);
      notifyListeners();
      return true;
    } on ApiException catch (e) {
      _lastError = e.message;
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
    notifyListeners();
  }

  String _newIdempotencyKey() {
    final rand = Random().nextInt(1 << 31).toRadixString(16);
    return '${DateTime.now().microsecondsSinceEpoch}-$rand';
  }
}
