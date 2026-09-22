import 'dart:async';
import 'dart:io';
import 'dart:math';

import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;

import '../api/api_client.dart';
import '../local/offline_store.dart';
import '../printing/printer_service.dart';
import 'device_id.dart';

/// Holds everything a single POS terminal session needs: the authenticated
/// API client, the offline-sync local store, and the cart currently being
/// built. A ChangeNotifier so screens can listen with `context.watch`.
///
/// Offline design: the cart for a single sale is either entirely
/// server-backed (the normal path — CreateOrder/AddLine/Checkout hit the
/// live API as each item is scanned) or entirely local (built in
/// OfflineStore's sqflite tables and pushed as one completed unit via
/// POST /sync/push once connectivity returns). It never switches mid-sale
/// — a network failure partway through the live path would leave the sale
/// in an ambiguous state (some lines confirmed server-side, others not),
/// so the *next* action after any network failure restarts building
/// locally rather than trying to reconcile the two. `offlineMode` flips to
/// true the first time any live call hits a network error, and back to
/// false the next time a sync attempt actually reaches the server.
class AppSession extends ChangeNotifier {
  final ApiClient api;

  // TODO(phase 2): these belong to a terminal-registration/device-binding
  // flow (FRD §18 device binding), not a hardcoded constant. Wired to the
  // seed data in migrations/002_seed.sql for this walking skeleton.
  final String branchId;
  final String posTerminalId;

  final OfflineStore _offline = OfflineStore();

  AppSession({required this.api, required this.branchId, required this.posTerminalId});

  String? userId;
  List<String> roles = [];
  bool get isLoggedIn => userId != null;

  OrderSummary? currentOrder;
  bool _currentOrderIsLocal = false;
  bool get isCurrentOrderLocal => _currentOrderIsLocal;

  bool offlineMode = false;
  int pendingSyncCount = 0;

  String? _lastError;
  String? get lastError => _lastError;

  // Customer/loyalty/discount/promotions/void are all live-API-only
  // features (Phase 3/4) — they have no equivalent in OfflineStore's
  // local schema, which only ever models the walking-skeleton cart shape
  // (lines + payments). Attempting any of them while offlineMode or on a
  // locally-built order is refused with a clear message rather than
  // silently doing nothing or half-applying — see each method below.
  String? attachedCustomerId;
  String? attachedCustomerName;
  int? loyaltyAvailablePoints;

  // Populated by scanBarcode whenever it falls back to the local catalog
  // cache — addToCart's offline path needs the tax rates a plain
  // ProductLookup doesn't carry, and the barcode scan always happens
  // immediately before addToCart in the POS screen's flow.
  final Map<String, CachedProduct> _offlineCatalogByVariant = {};

  Future<bool> login(String merchantCode, String email, String password) async {
    _lastError = null;
    try {
      final result = await api.login(merchantCode: merchantCode, email: email, password: password);
      userId = result.userId;
      roles = result.roles;
      offlineMode = false;
      notifyListeners();
      unawaited(_refreshCatalogCache());
      unawaited(refreshPendingSyncCount());
      return true;
    } on ApiException catch (e) {
      _lastError = e.message;
      notifyListeners();
      return false;
    }
  }

  Future<bool> loginWithPin(String employeeCode, String pin) async {
    _lastError = null;
    try {
      final fingerprint = await DeviceId.get();
      final result = await api.pinLogin(
        posTerminalId: posTerminalId,
        employeeCode: employeeCode,
        pin: pin,
        deviceFingerprint: fingerprint,
      );
      userId = result.userId;
      roles = result.roles;
      offlineMode = false;
      notifyListeners();
      unawaited(_refreshCatalogCache());
      unawaited(refreshPendingSyncCount());
      return true;
    } on ApiException catch (e) {
      _lastError = e.code == 'DEVICE_MISMATCH'
          ? 'This terminal is registered to a different device'
          : e.message;
      notifyListeners();
      return false;
    }
  }

  /// Pulls catalog/stock deltas since the last successful pull. Best-effort
  /// — failures here just mean the offline cache stays as fresh as it last
  /// was, not a user-facing error. Called after login and after every
  /// successful sync, which is the natural rhythm for an offline-first POS
  /// (refresh whenever connectivity is confirmed present).
  Future<void> _refreshCatalogCache() async {
    try {
      final since = await _offline.getLastPullAt();
      final data = await api.syncPull(branchId: branchId, since: since);
      await _offline.cacheCatalog(data['catalog'] as List<dynamic>);
      await _offline.cacheStock(data['stock'] as List<dynamic>);
      await _offline.setLastPullAt(data['server_time'] as String);
      if (offlineMode) {
        offlineMode = false;
        notifyListeners();
      }
    } catch (_) {
      // Offline or server unreachable — leave the existing cache as-is.
    }
  }

  Future<void> refreshPendingSyncCount() async {
    pendingSyncCount = await _offline.countPendingSync();
    notifyListeners();
  }

  /// Pushes every locally-completed sale still waiting to sync. Safe to
  /// call opportunistically (app resume, a manual "Sync now" tap, after
  /// every checkout) — each push is idempotent server-side, and a device
  /// with nothing pending just does a no-op pull-and-return.
  Future<void> syncNow() async {
    final pending = await _offline.listUnsynced();
    if (pending.isEmpty) {
      await _refreshCatalogCache();
      await refreshPendingSyncCount();
      return;
    }

    try {
      final orders = pending
          .map((o) => {
                'idempotency_key': o.idempotencyKey,
                'branch_id': o.branchId,
                'pos_terminal_id': o.posTerminalId,
                'device_created_at': o.deviceCreatedAt,
                'lines': o.lines
                    .map((l) => {
                          'variant_id': l['variant_id'],
                          'quantity': l['quantity'],
                          'unit_price': l['unit_price'],
                          'discount_amount': l['discount_amount'],
                          'tax_amount': l['tax_amount'],
                          'line_total': l['line_total'],
                        })
                    .toList(),
                'payments': o.payments
                    .map((p) => {'method': p['method'], 'amount': p['amount']})
                    .toList(),
              })
          .toList();

      final resp = await api.syncPush(orders);
      final results = resp['results'] as List<dynamic>;
      for (var i = 0; i < results.length; i++) {
        final r = results[i] as Map<String, dynamic>;
        final local = pending[i];
        if (r['status'] == 'ok' || r['status'] == 'duplicate') {
          await _offline.markSynced(local.id, r['order_id'] as String);
        } else {
          await _offline.markSyncFailed(local.id, r['error'] as String? ?? 'unknown error');
        }
      }
      offlineMode = false;
      await _refreshCatalogCache();
    } catch (e) {
      if (!_isNetworkError(e)) rethrow;
      offlineMode = true;
    }
    await refreshPendingSyncCount();
  }

  Future<ProductLookup?> scanBarcode(String code) async {
    _lastError = null;
    if (!offlineMode) {
      try {
        return await api.lookupBarcode(code);
      } on ApiException catch (e) {
        _lastError = e.code == 'PRODUCT_NOT_FOUND' ? 'No product matches that barcode' : e.message;
        notifyListeners();
        return null;
      } catch (e) {
        if (!_isNetworkError(e)) rethrow;
        offlineMode = true;
        notifyListeners();
      }
    }

    final cached = await _offline.lookupBarcode(code);
    if (cached == null) {
      _lastError = 'No product matches that barcode (offline — catalog cache may be out of date)';
      notifyListeners();
      return null;
    }
    _offlineCatalogByVariant[cached.lookup.variantId] = cached;
    return cached.lookup;
  }

  Future<bool> addToCart(ProductLookup product, double quantity) async {
    _lastError = null;
    if (offlineMode) {
      return _addToCartOffline(product, quantity);
    }
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
      _currentOrderIsLocal = false;
      notifyListeners();
      return true;
    } on ApiException catch (e) {
      _lastError = e.code == 'STOCK_UNAVAILABLE'
          ? 'Not enough stock available for that quantity'
          : e.message;
      notifyListeners();
      return false;
    } catch (e) {
      if (!_isNetworkError(e)) rethrow;
      offlineMode = true;
      // The order we thought we'd started online may or may not have
      // reached the server — start a fresh local order rather than risk
      // adding this line to an order the server never actually saw.
      currentOrder = null;
      return _addToCartOffline(product, quantity);
    }
  }

  Future<bool> _addToCartOffline(ProductLookup product, double quantity) async {
    final cached = _offlineCatalogByVariant[product.variantId];
    if (cached == null) {
      _lastError = 'This item was not in the offline catalog cache — cannot add it while offline';
      notifyListeners();
      return false;
    }
    if (!_currentOrderIsLocal || currentOrder == null) {
      final localId = await _offline.createPendingOrder(branchId, posTerminalId);
      currentOrder = OrderSummary(
        orderId: localId,
        orderNumber: 'OFFLINE-${localId.substring(0, 8)}',
        status: 'cart',
        subtotal: '0.00',
        discountTotal: '0.00',
        taxTotal: '0.00',
        grandTotal: '0.00',
      );
      _currentOrderIsLocal = true;
    }
    currentOrder = await _offline.addLine(currentOrder!.orderId, cached, quantity);
    notifyListeners();
    return true;
  }

  Future<bool> removeLine(String lineId) async {
    if (currentOrder == null) return false;
    _lastError = null;
    try {
      if (_currentOrderIsLocal) {
        currentOrder = await _offline.removeLine(currentOrder!.orderId, lineId);
      } else {
        currentOrder = await api.deleteLine(orderId: currentOrder!.orderId, lineId: lineId);
      }
      notifyListeners();
      return true;
    } on ApiException catch (e) {
      _lastError = e.message;
      notifyListeners();
      return false;
    } catch (e) {
      if (!_isNetworkError(e)) rethrow;
      _lastError = 'Cannot remove this line while offline — it was added before connectivity was lost';
      notifyListeners();
      return false;
    }
  }

  Future<bool> checkout(List<PaymentInput> payments) async {
    if (currentOrder == null) return false;
    _lastError = null;
    if (_currentOrderIsLocal) {
      currentOrder = await _offline.checkout(currentOrder!.orderId, payments);
      await refreshPendingSyncCount();
      notifyListeners();
      unawaited(syncNow()); // opportunistic — fine if this silently fails
      return true;
    }
    try {
      final finalized = await api.checkout(orderId: currentOrder!.orderId, payments: payments);
      currentOrder = finalized;
      notifyListeners();
      return true;
    } on ApiException catch (e) {
      _lastError = switch (e.code) {
        'PAYMENT_MISMATCH' => 'Payment total does not cover the order total',
        'CREDIT_HOLD' => 'This customer is on credit hold — cannot accept a credit payment',
        'CREDIT_LIMIT_EXCEEDED' => 'This sale would put the customer over their credit limit',
        _ => e.message,
      };
      notifyListeners();
      return false;
    } catch (e) {
      if (!_isNetworkError(e)) rethrow;
      // The order was created online but we lost connectivity before
      // checkout confirmed — safest is to surface this rather than
      // silently re-run it as a brand-new local sale (that would risk a
      // double sale if the server actually did process the checkout right
      // before the connection dropped).
      offlineMode = true;
      _lastError = 'Lost connection during checkout — check this sale on another device before retrying.';
      notifyListeners();
      return false;
    }
  }

  /// True for everything below (customer/discount/promotions/coupon/
  /// loyalty/void/quantity-update) — none of these have an offline
  /// equivalent (see the field doc comment on attachedCustomerId above),
  /// so every one of them checks this first and refuses cleanly rather
  /// than silently no-op-ing against a local order the server has never
  /// seen.
  bool get _canUseLiveOnlyFeatures => currentOrder != null && !_currentOrderIsLocal && !offlineMode;

  Future<bool> updateLineQuantity(String lineId, double quantity) async {
    if (!_canUseLiveOnlyFeatures) {
      _lastError = 'Not available offline';
      notifyListeners();
      return false;
    }
    _lastError = null;
    try {
      currentOrder = await api.updateLineQuantity(orderId: currentOrder!.orderId, lineId: lineId, quantity: quantity);
      notifyListeners();
      return true;
    } on ApiException catch (e) {
      _lastError = e.code == 'STOCK_UNAVAILABLE' ? 'Not enough stock available for that quantity' : e.message;
      notifyListeners();
      return false;
    }
  }

  Future<List<Customer>> searchCustomers(String query) => api.searchCustomers(query);

  /// Attaches an existing customer (by id) or creates-and-attaches a
  /// walk-in from name/phone/email. Required before a `credit` payment
  /// method at checkout, and before loyalty points can be redeemed —
  /// see internal/sales/customer.go.
  Future<bool> attachCustomer({String? customerId, String? name, String? phone, String? email}) async {
    if (!_canUseLiveOnlyFeatures) {
      _lastError = 'Not available offline';
      notifyListeners();
      return false;
    }
    _lastError = null;
    try {
      final id = await api.attachCustomer(
        orderId: currentOrder!.orderId,
        customerId: customerId,
        name: name,
        phone: phone,
        email: email,
      );
      attachedCustomerId = id;
      attachedCustomerName = customerId != null ? name : (name ?? phone);
      notifyListeners();
      unawaited(refreshLoyaltyBalance());
      return true;
    } on ApiException catch (e) {
      _lastError = e.message;
      notifyListeners();
      return false;
    }
  }

  Future<void> refreshLoyaltyBalance() async {
    if (attachedCustomerId == null) return;
    try {
      final balance = await api.getCustomerLoyalty(attachedCustomerId!);
      loyaltyAvailablePoints = balance.availablePoints;
      notifyListeners();
    } catch (_) {
      // Best-effort display only — a stale/missing balance doesn't block
      // the sale, redeem() itself is still the source of truth.
    }
  }

  Future<bool> applyManualDiscount({
    required double valuePercent,
    String authorizedBy = '',
    String authorizedPin = '',
    required String reason,
  }) async {
    if (!_canUseLiveOnlyFeatures) {
      _lastError = 'Not available offline';
      notifyListeners();
      return false;
    }
    _lastError = null;
    try {
      currentOrder = await api.applyDiscount(
        orderId: currentOrder!.orderId,
        valuePercent: valuePercent,
        authorizedBy: authorizedBy,
        authorizedPin: authorizedPin,
        reason: reason,
      );
      notifyListeners();
      return true;
    } on ApiException catch (e) {
      _lastError = e.code == 'DISCOUNT_NOT_AUTHORIZED'
          ? 'This discount needs a higher-tier manager/admin PIN'
          : e.message;
      notifyListeners();
      return false;
    }
  }

  Future<bool> applyPromotions() async {
    if (!_canUseLiveOnlyFeatures) {
      _lastError = 'Not available offline';
      notifyListeners();
      return false;
    }
    _lastError = null;
    try {
      currentOrder = await api.applyPromotions(currentOrder!.orderId);
      notifyListeners();
      return true;
    } on ApiException catch (e) {
      _lastError = e.message;
      notifyListeners();
      return false;
    }
  }

  Future<bool> applyCoupon(String code) async {
    if (!_canUseLiveOnlyFeatures) {
      _lastError = 'Not available offline';
      notifyListeners();
      return false;
    }
    _lastError = null;
    try {
      currentOrder = await api.applyCoupon(orderId: currentOrder!.orderId, code: code);
      notifyListeners();
      return true;
    } on ApiException catch (e) {
      _lastError = switch (e.code) {
        'COUPON_NOT_FOUND' => 'No active coupon with that code',
        'COUPON_NOT_VALID' => 'That coupon is not valid right now (window/branch/channel)',
        'COUPON_MIN_PURCHASE_NOT_MET' => 'Cart total is below this coupon\'s minimum purchase',
        'COUPON_LIMIT_REACHED' => 'This coupon has reached its usage limit',
        'COUPON_ALREADY_APPLIED' => 'A coupon is already applied to this order',
        _ => e.message,
      };
      notifyListeners();
      return false;
    }
  }

  Future<bool> redeemLoyaltyPoints(int points) async {
    if (!_canUseLiveOnlyFeatures) {
      _lastError = 'Not available offline';
      notifyListeners();
      return false;
    }
    if (attachedCustomerId == null) {
      _lastError = 'Attach a customer before redeeming loyalty points';
      notifyListeners();
      return false;
    }
    _lastError = null;
    try {
      currentOrder = await api.redeemLoyalty(orderId: currentOrder!.orderId, points: points);
      notifyListeners();
      unawaited(refreshLoyaltyBalance());
      return true;
    } on ApiException catch (e) {
      _lastError = e.code == 'INSUFFICIENT_LOYALTY_POINTS'
          ? 'Customer does not have that many points available'
          : e.message;
      notifyListeners();
      return false;
    }
  }

  /// Voids a *finalized* sale — see internal/sales/void.go. Enforced
  /// server-side by the `sales.void` permission; this client doesn't
  /// gate the button itself on `roles` (a POS User attempting it just
  /// gets a clear 403 back), matching how every other permission-gated
  /// action in this codebase surfaces denial.
  Future<bool> voidCurrentOrder(String reason) async {
    if (currentOrder == null || _currentOrderIsLocal || offlineMode) {
      _lastError = 'Not available offline';
      notifyListeners();
      return false;
    }
    _lastError = null;
    try {
      currentOrder = await api.voidOrder(orderId: currentOrder!.orderId, reason: reason);
      notifyListeners();
      return true;
    } on ApiException catch (e) {
      _lastError = e.message;
      notifyListeners();
      return false;
    }
  }

  Future<ReceiptData?> fetchReceipt() async {
    if (currentOrder == null || _currentOrderIsLocal) return null;
    try {
      return await api.getReceipt(currentOrder!.orderId);
    } catch (_) {
      return null;
    }
  }

  /// Fetches the print-ready ESC/POS bytes and sends them to whatever
  /// printer PrinterService has configured. Returns false (with
  /// `lastError` set) both when nothing's configured and when the send
  /// itself fails — callers should treat both the same way: the sale is
  /// still finalized either way, only the print attempt failed.
  Future<bool> printReceipt(PrinterService printer) async {
    if (currentOrder == null || _currentOrderIsLocal) {
      _lastError = 'Not available offline';
      notifyListeners();
      return false;
    }
    _lastError = null;
    try {
      final bytes = await api.printReceiptBytes(currentOrder!.orderId);
      await printer.printBytes(bytes);
      return true;
    } catch (e) {
      _lastError = e is StateError ? e.message : 'Could not print receipt: $e';
      notifyListeners();
      return false;
    }
  }

  /// Starts a fresh cart, discarding any in-progress (un-checked-out) one.
  /// Called after a successful checkout, or if the cashier voids the sale
  /// locally before it ever reached the server.
  void startNewSale() {
    currentOrder = null;
    _currentOrderIsLocal = false;
    attachedCustomerId = null;
    attachedCustomerName = null;
    loyaltyAvailablePoints = null;
    notifyListeners();
  }

  bool _isNetworkError(Object e) =>
      e is SocketException || e is TimeoutException || e is http.ClientException;

  String _newIdempotencyKey() {
    final rand = Random().nextInt(1 << 31).toRadixString(16);
    return '${DateTime.now().microsecondsSinceEpoch}-$rand';
  }
}
