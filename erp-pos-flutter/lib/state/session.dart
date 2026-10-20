import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';

import '../api/api_client.dart';
import '../l10n/l10n.dart';
import '../local/offline_store.dart';
import '../printing/document_saver.dart' as doc_saver;
import '../printing/printer_service.dart';
import '../ui/format.dart';
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

  // Phase 12: come from the device's pairing (TerminalConfig, see
  // main.dart), not hardcoded seed ids. Names are for display only.
  final String branchId;
  final String posTerminalId;
  final String terminalName;
  final String branchName;
  final String merchantCode;

  /// Set by the app shell: forgets this device's pairing (login screen
  /// only — never offered mid-sale). Null in tests that build a session
  /// directly.
  VoidCallback? onUnpairDevice;

  final OfflineStore _offline = OfflineStore();

  AppSession({
    required this.api,
    required this.branchId,
    required this.posTerminalId,
    this.terminalName = 'POS terminal',
    this.branchName = '',
    this.merchantCode = '',
  }) {
    api.canRefresh = () => !_isIdle;
    api.onSessionExpired = _endSession;
    api.terminalId = posTerminalId;
  }

  // ---------------------------------------------------------------------
  // Session lifecycle — same rules as erp-web-admin's lib/api-client.ts.
  // The access token's lifetime is the FRD's role-based inactivity
  // timeout (POS User 15 min), and refresh tokens last 30 days, so:
  //   - cashier active, token about to lapse     -> silent refresh
  //   - no input for a full session window       -> back to LoginScreen
  //   - refresh rejected (revoked/locked/expired) -> back to LoginScreen
  // Offline-first exception: while the server is unreachable the session
  // is never ended (re-login needs the server, so ending it would block
  // offline selling outright); the idle check resumes once back online.
  // ---------------------------------------------------------------------
  DateTime _lastActivity = DateTime.now();
  Timer? _sessionTimer;

  /// Why the last session ended, for LoginScreen to show once.
  String? sessionEndedNotice;

  bool get _isIdle {
    final window = api.sessionWindow;
    return window != null && DateTime.now().difference(_lastActivity) >= window;
  }

  /// Real cashier input (taps, scanner/keyboard) — wired in main.dart.
  void markActivity() => _lastActivity = DateTime.now();

  void _startSessionTimer() {
    _lastActivity = DateTime.now();
    _sessionTimer?.cancel();
    _sessionTimer = Timer.periodic(const Duration(seconds: 15), (_) => checkSession());
  }

  /// Proactive check, also called when the app returns to the foreground.
  Future<void> checkSession() async {
    if (!isLoggedIn || offlineMode) return;
    if (_isIdle) {
      api.expireSession(SessionEndReason.idle);
      return;
    }
    if (api.accessTokenExpiringSoon && await api.refresh() == RefreshOutcome.rejected) {
      api.expireSession(SessionEndReason.revoked);
    }
  }

  void _endSession(SessionEndReason reason) {
    _resetToLoggedOut();
    sessionEndedNotice = sessionEndedMessage(reason);
    notifyListeners();
  }

  /// Manual sign-out from the POS screen.
  void logout() {
    api.clearSession();
    _resetToLoggedOut();
    notifyListeners();
  }

  // A server-backed cart left open is reclaimed by the backend's 15-minute
  // reservation-expiry sweeper; an offline-built order stays queued in
  // OfflineStore and still syncs after the next login.
  void _resetToLoggedOut() {
    _sessionTimer?.cancel();
    _sessionTimer = null;
    userId = null;
    roles = [];
    currentOrder = null;
    _currentOrderIsLocal = false;
    attachedCustomerId = null;
    attachedCustomerName = null;
    loyaltyAvailablePoints = null;
    _lastError = null;
  }

  @override
  void dispose() {
    _sessionTimer?.cancel();
    super.dispose();
  }

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
      sessionEndedNotice = null;
      _startSessionTimer();
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
      sessionEndedNotice = null;
      _startSessionTimer();
      notifyListeners();
      unawaited(_refreshCatalogCache());
      unawaited(refreshPendingSyncCount());
      return true;
    } on ApiException catch (e) {
      _lastError = e.code == 'DEVICE_MISMATCH'
          ? tr('This terminal is registered to a different device')
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
                          'price_includes_tax': (l['price_includes_tax'] as int? ?? 0) == 1,
                          'open_price': (l['open_price'] as int? ?? 0) == 1,
                          if (l['description'] != null) 'description': l['description'],
                        })
                    .toList(),
                'payments': o.payments
                    .map((p) => {'method': p['method'], 'amount': p['amount']})
                    .toList(),
                if (o.roundOff != 0) 'round_off': o.roundOff,
              })
          .toList();

      final resp = await api.syncPush(orders);
      final results = resp['results'] as List<dynamic>;
      for (var i = 0; i < results.length; i++) {
        final r = results[i] as Map<String, dynamic>;
        final local = pending[i];
        if (r['status'] == 'ok' || r['status'] == 'duplicate') {
          await _offline.markSynced(local.id, r['order_id'] as String, needsReview: r['negative_stock'] == true);
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
        _lastError = e.code == 'PRODUCT_NOT_FOUND' ? tr('No product matches that barcode') : e.message;
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
      _lastError = tr('No product matches that barcode (offline — catalog cache may be out of date)');
      notifyListeners();
      return null;
    }
    _offlineCatalogByVariant[cached.lookup.variantId] = cached;
    return cached.lookup;
  }

  /// Category/subcategory tree for the product-search screen's filters.
  /// Live-only, like searchProducts below — categories aren't part of the
  /// offline catalog cache (see CachedProduct's fields), so there's
  /// nothing to fall back to while offline.
  Future<List<CatalogCategory>> loadCategories() => api.listCategories();

  /// Name/category/subcategory product lookup — the non-barcode path to
  /// find something and add it to the cart. Live-network only: the local
  /// SQLite catalog cache used for offline barcode scans has no category
  /// data and isn't full-text searchable, so unlike scanBarcode this has
  /// no offline fallback and refuses cleanly instead, matching
  /// searchCustomers/applyPromotions/etc.'s existing "Not available
  /// offline" convention rather than half-supporting it.
  Future<List<ProductSearchResult>> searchProducts({String query = '', String? categoryId}) async {
    _lastError = null;
    if (offlineMode) {
      _lastError = tr('Product search is not available offline');
      notifyListeners();
      return [];
    }
    try {
      return await api.searchProducts(query: query, categoryId: categoryId);
    } on ApiException catch (e) {
      _lastError = e.code == 'SEARCH_UNAVAILABLE' ? tr('Product search is temporarily unavailable') : e.message;
      notifyListeners();
      return [];
    } catch (e) {
      if (!_isNetworkError(e)) rethrow;
      offlineMode = true;
      _lastError = tr('Product search is not available offline');
      notifyListeners();
      return [];
    }
  }

  /// Phase 12 product grid source. Online: the server's search (fuzzy,
  /// category-filtered). Offline — or if the search service is down —
  /// the local catalog cache by name/SKU, with cached stock, so a cashier
  /// can still find and sell an item without its barcode. Offline items are
  /// registered for addToCart's offline path, the same way scanBarcode's
  /// cache fallback does.
  Future<List<GridProduct>> browseProducts({String query = '', String? categoryId}) async {
    _lastError = null;
    if (!offlineMode) {
      try {
        final results = await api.searchProducts(query: query, categoryId: categoryId, branchId: branchId);
        return results
            .map((r) => GridProduct(lookup: r.toProductLookup(), categoryName: r.categoryName, available: null, openPrice: r.openPrice))
            .toList();
      } on ApiException catch (e) {
        if (e.code != 'SEARCH_UNAVAILABLE') {
          _lastError = e.message;
          notifyListeners();
          return [];
        }
        // Search service down: fall through to the local cache.
      } catch (e) {
        if (!_isNetworkError(e)) rethrow;
        offlineMode = true;
        notifyListeners();
      }
    }
    final items = await _offline.searchCatalog(query, openPriceFirst: openPriceFirst);
    for (final i in items) {
      _offlineCatalogByVariant[i.product.lookup.variantId] = i.product;
    }
    return items
        .map((i) => GridProduct(lookup: i.product.lookup, categoryName: null, available: i.available, openPrice: i.product.lookup.openPrice != null))
        .toList();
  }

  /// Set when an item needs its price typed (Phase 13 open price): the
  /// screen shows the price keypad and calls [addToCart] again with an
  /// [OpenPriceEntry]. Cleared on every [addToCart].
  OpenPricePrompt? openPricePrompt;

  Future<bool> addToCart(ProductLookup product, double quantity, {OpenPriceEntry? openPrice}) async {
    _lastError = null;
    openPricePrompt = null;
    // Known open-price item and no price yet: ask before any round trip.
    if (product.openPrice != null && openPrice == null) {
      openPricePrompt = OpenPricePrompt(product: product, range: product.openPrice!, suggestedPrice: product.sellingPrice);
      notifyListeners();
      return false;
    }
    if (offlineMode) {
      return _addToCartOffline(product, quantity, openPrice);
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
        unitPrice: openPrice?.price,
        approverCode: openPrice?.approverCode,
        approverPin: openPrice?.approverPin,
        description: openPrice?.description,
      );
      currentOrder = updated;
      _currentOrderIsLocal = false;
      notifyListeners();
      return true;
    } on ApiException catch (e) {
      // The server knows an item is open-price even when this lookup
      // didn't say so (a search hit carries no range): it answers with
      // the range, and the keypad opens with it.
      final range = OpenPriceRange.fromJson(e.details?['open_price']);
      if (e.code == 'OPEN_PRICE_REQUIRED' && range != null) {
        openPricePrompt = OpenPricePrompt(
          product: product,
          range: range,
          suggestedPrice: e.details?['suggested_price'] as String? ?? product.sellingPrice,
        );
        notifyListeners();
        return false;
      }
      _lastError = switch (e.code) {
        'STOCK_UNAVAILABLE' => tr('Not enough stock available for that quantity'),
        'OPEN_PRICE_NOT_APPROVED' => tr('Approval failed. Check the manager\'s employee code and PIN.'),
        _ => e.message,
      };
      notifyListeners();
      return false;
    } catch (e) {
      if (!_isNetworkError(e)) rethrow;
      offlineMode = true;
      // The order we thought we'd started online may or may not have
      // reached the server — start a fresh local order rather than risk
      // adding this line to an order the server never actually saw.
      currentOrder = null;
      return _addToCartOffline(product, quantity, openPrice);
    }
  }

  Future<bool> _addToCartOffline(ProductLookup product, double quantity, OpenPriceEntry? openPrice) async {
    final cached = _offlineCatalogByVariant[product.variantId];
    if (cached == null) {
      _lastError = tr('This item was not in the offline catalog cache — cannot add it while offline');
      notifyListeners();
      return false;
    }
    // Offline, the device enforces the range itself; a price that needs a
    // manager's approval can't be checked without the server.
    final range = cached.lookup.openPrice;
    if (range != null) {
      if (openPrice == null) {
        openPricePrompt = OpenPricePrompt(product: product, range: range, suggestedPrice: cached.lookup.sellingPrice);
        notifyListeners();
        return false;
      }
      if (!range.allows(openPrice.price)) {
        _lastError = tr('The price must be between {0} and {1}', [formatRupees(range.min), formatRupees(range.max)]);
        notifyListeners();
        return false;
      }
      if (range.needsApproval(openPrice.price)) {
        _lastError = tr('Prices above {0} need a manager\'s approval, which needs a connection', [formatRupees(range.approvalAbove ?? '')]);
        notifyListeners();
        return false;
      }
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
    currentOrder = await _offline.addLine(currentOrder!.orderId, cached, quantity,
        unitPrice: range == null ? null : openPrice?.price, description: range == null ? null : openPrice?.description);
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
      _lastError = tr('Cannot remove this line while offline — it was added before connectivity was lost');
      notifyListeners();
      return false;
    }
  }

  /// [roundOff]: the amount waived, signed (-0.75), within
  /// [roundOffMax]; the server checks it again (online) or records it with
  /// the synced sale (offline).
  Future<bool> checkout(List<PaymentInput> payments, {double roundOff = 0}) async {
    if (currentOrder == null) return false;
    _lastError = null;
    if (_currentOrderIsLocal) {
      currentOrder = await _offline.checkout(currentOrder!.orderId, payments, roundOff: roundOff);
      await refreshPendingSyncCount();
      notifyListeners();
      unawaited(syncNow()); // opportunistic — fine if this silently fails
      return true;
    }
    try {
      final finalized = await api.checkout(orderId: currentOrder!.orderId, payments: payments, roundOff: roundOff);
      currentOrder = finalized;
      notifyListeners();
      return true;
    } on ApiException catch (e) {
      _lastError = switch (e.code) {
        'PAYMENT_MISMATCH' => tr('Payment total does not cover the order total'),
        'CREDIT_HOLD' => tr('This customer is on credit hold — cannot accept a credit payment'),
        'CREDIT_LIMIT_EXCEEDED' => tr('This sale would put the customer over their credit limit'),
        'SHIFT_NOT_OPEN' => tr('Open a shift on this till before taking payment (tap the shift chip at the top)'),
        'ROUND_OFF_NOT_ALLOWED' => tr('That round-off isn’t allowed (it’s off, or above the limit in Sales Policy)'),
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
      _lastError = tr('Lost connection during checkout — check this sale on another device before retrying.');
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
      _lastError = tr('Not available offline');
      notifyListeners();
      return false;
    }
    _lastError = null;
    try {
      currentOrder = await api.updateLineQuantity(orderId: currentOrder!.orderId, lineId: lineId, quantity: quantity);
      notifyListeners();
      return true;
    } on ApiException catch (e) {
      _lastError = e.code == 'STOCK_UNAVAILABLE' ? tr('Not enough stock available for that quantity') : e.message;
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
      _lastError = tr('Not available offline');
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

  /// A percent off ([valuePercent]) or, with [amount], a fixed rupee
  /// amount off the sale.
  Future<bool> applyManualDiscount({
    String authorizedByCode = '',
    double valuePercent = 0,
    double? amount,
    String authorizedBy = '',
    String authorizedPin = '',
    required String reason,
  }) async {
    if (!_canUseLiveOnlyFeatures) {
      _lastError = tr('Not available offline');
      notifyListeners();
      return false;
    }
    _lastError = null;
    try {
      currentOrder = await api.applyDiscount(
        orderId: currentOrder!.orderId,
        valuePercent: valuePercent,
        amount: amount,
        authorizedBy: authorizedBy,
        authorizedByCode: authorizedByCode,
        authorizedPin: authorizedPin,
        reason: reason,
      );
      notifyListeners();
      return true;
    } on ApiException catch (e) {
      _lastError = e.code == 'DISCOUNT_NOT_AUTHORIZED'
          ? tr('This discount needs a higher-tier manager/admin PIN')
          : e.message;
      notifyListeners();
      return false;
    }
  }

  Future<bool> applyPromotions() async {
    if (!_canUseLiveOnlyFeatures) {
      _lastError = tr('Not available offline');
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
      _lastError = tr('Not available offline');
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
        'COUPON_NOT_FOUND' => tr('No active coupon with that code'),
        'COUPON_NOT_VALID' => tr('That coupon is not valid right now (window/branch/channel)'),
        'COUPON_MIN_PURCHASE_NOT_MET' => tr('Cart total is below this coupon\'s minimum purchase'),
        'COUPON_LIMIT_REACHED' => tr('This coupon has reached its usage limit'),
        'COUPON_ALREADY_APPLIED' => tr('A coupon is already applied to this order'),
        _ => e.message,
      };
      notifyListeners();
      return false;
    }
  }

  Future<bool> redeemLoyaltyPoints(int points) async {
    if (!_canUseLiveOnlyFeatures) {
      _lastError = tr('Not available offline');
      notifyListeners();
      return false;
    }
    if (attachedCustomerId == null) {
      _lastError = tr('Attach a customer before redeeming loyalty points');
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
          ? tr('Customer does not have that many points available')
          : e.message;
      notifyListeners();
      return false;
    }
  }

  /// Parks the current cart so the cashier can serve someone else, then
  /// clears it from the active screen (startNewSale) — the cart itself
  /// stays alive server-side as 'held', recallable later. No offline
  /// equivalent, same as every other live-only feature above.
  Future<bool> holdCurrentOrder({String? note}) async {
    if (!_canUseLiveOnlyFeatures) {
      _lastError = tr('Not available offline');
      notifyListeners();
      return false;
    }
    _lastError = null;
    try {
      await api.holdOrder(orderId: currentOrder!.orderId, note: note);
      startNewSale();
      return true;
    } on ApiException catch (e) {
      _lastError = e.code == 'INVALID_REQUEST' ? tr('Cannot hold an empty cart') : e.message;
      notifyListeners();
      return false;
    }
  }

  Future<List<HeldOrderSummary>> listHeldOrders() async {
    try {
      return await api.listHeldOrders(branchId);
    } catch (_) {
      return [];
    }
  }

  /// Voids a held cart (online only: it releases server-side stock holds).
  /// False with [lastError] set when it can't.
  Future<bool> voidHeldOrder(String orderId, String reason) async {
    if (offlineMode) {
      _lastError = tr('Not available offline');
      notifyListeners();
      return false;
    }
    _lastError = null;
    try {
      await api.voidHeldOrder(orderId: orderId, reason: reason);
      return true;
    } on ApiException catch (e) {
      _lastError = e.message;
      notifyListeners();
      return false;
    } catch (_) {
      _lastError = tr('Could not reach the server');
      notifyListeners();
      return false;
    }
  }

  /// Recalls a held cart back into the active one. Refuses if there's
  /// already an unsaved live cart in progress — recalling would otherwise
  /// silently orphan it — the cashier holds or finishes it first.
  Future<RecallResult?> recallOrder(String orderId) async {
    if (offlineMode) {
      _lastError = tr('Not available offline');
      notifyListeners();
      return null;
    }
    if (currentOrder != null && !_currentOrderIsLocal && currentOrder!.status == 'cart' && currentOrder!.lines.isNotEmpty) {
      _lastError = tr('Hold or finish the current cart before recalling another one');
      notifyListeners();
      return null;
    }
    _lastError = null;
    try {
      final result = await api.recallOrder(orderId);
      currentOrder = result.order;
      _currentOrderIsLocal = false;
      attachedCustomerId = null;
      attachedCustomerName = null;
      loyaltyAvailablePoints = null;
      notifyListeners();
      return result;
    } on ApiException catch (e) {
      _lastError = e.message;
      notifyListeners();
      return null;
    }
  }

  /// Voids a *finalized* sale — see internal/sales/void.go. Enforced
  /// server-side by the `sales.void` permission; this client doesn't
  /// gate the button itself on `roles` (a POS User attempting it just
  /// gets a clear 403 back), matching how every other permission-gated
  /// action in this codebase surfaces denial.
  Future<bool> voidCurrentOrder(String reason) async {
    if (currentOrder == null || _currentOrderIsLocal || offlineMode) {
      _lastError = tr('Not available offline');
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
      _lastError = tr('Not available offline');
      notifyListeners();
      return false;
    }
    return printReceiptFor(currentOrder!.orderId, printer);
  }

  /// Reprints any server sale (Phase 13 history), not just the current one.
  Future<bool> printReceiptFor(String orderId, PrinterService printer) async {
    _lastError = null;
    try {
      final bytes = await api.printReceiptBytes(orderId);
      await printer.printBytes(bytes);
      return true;
    } catch (e) {
      _lastError = e is StateError ? e.message : tr('Could not print receipt: {0}', [e]);
      notifyListeners();
      return false;
    }
  }

  /// Where downloaded documents are written — the device's Downloads
  /// folder (lib/printing/document_saver.dart). Swappable in tests.
  Future<String> Function(String filename, List<int> bytes) saveDocument = doc_saver.saveDocument;

  /// Downloads the current sale's receipt as a PDF and saves it on the
  /// device; returns the saved path, or null with [lastError] set. A sale
  /// still queued offline has no server copy to render yet.
  Future<String?> downloadReceipt({String? format}) async {
    if (currentOrder == null || _currentOrderIsLocal) {
      _lastError = tr('The receipt PDF is available once this sale has synced');
      notifyListeners();
      return null;
    }
    return downloadReceiptFor(currentOrder!.orderId, format: format);
  }

  /// Any server sale's receipt PDF (Phase 13 history too). [format] 'a4' or
  /// 'thermal'; null = the branch's default paper.
  Future<String?> downloadReceiptFor(String orderId, {String? format}) async {
    _lastError = null;
    try {
      final file = await api.receiptPdf(orderId, format: format);
      return await saveDocument(file.filename, file.bytes);
    } on ApiException catch (e) {
      _lastError = e.message;
    } on SocketException {
      _lastError = tr('Can\'t reach the server — the receipt PDF needs a connection');
    } on http.ClientException {
      _lastError = tr('Can\'t reach the server — the receipt PDF needs a connection');
    } catch (e) {
      _lastError = tr('Could not download the receipt: {0}', [e]);
    }
    notifyListeners();
    return null;
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

  // ---------------------------------------------------------------------
  // Phase 13: sales history and returns. History and returns are
  // server-only. The device's own unsynced sales come from SQLite and are
  // shown as "queued"; they can't be returned until they've synced,
  // because until then the server has no order to return against.
  // ---------------------------------------------------------------------

  Future<List<QueuedSale>> queuedSales() => _offline.listQueuedSales();

  Future<List<SyncQueueItem>> syncQueue() => _offline.listSyncQueue();

  /// The server's open review items for this till (oversold offline
  /// sales); empty offline.
  Future<List<StockReviewItem>> openReviewItems() async {
    if (offlineMode) return [];
    try {
      return await api.listStockReview(terminalId: posTerminalId);
    } catch (e) {
      if (_isNetworkError(e)) {
        offlineMode = true;
        notifyListeners();
      }
      return [];
    }
  }

  /// Runs a live-only call; a network failure flips the terminal into
  /// offline mode (as every other live call does) and rethrows.
  Future<T> _live<T>(Future<T> Function() call) async {
    try {
      return await call();
    } catch (e) {
      if (_isNetworkError(e) && !offlineMode) {
        offlineMode = true;
        notifyListeners();
      }
      rethrow;
    }
  }

  Future<SalesHistoryPage> salesHistory({
    bool thisTerminalOnly = true,
    String? status,
    String? query,
    String? from,
    String? to,
    int page = 1,
  }) =>
      _live(() => api.listSalesOrders(
            terminalId: thisTerminalOnly ? posTerminalId : null,
            branchId: branchId,
            status: status,
            query: query,
            from: from,
            to: to,
            page: page,
          ));

  Future<SalesSummaryData> salesSummary({bool thisTerminalOnly = true, String? from, String? to}) =>
      _live(() => api.salesSummary(terminalId: thisTerminalOnly ? posTerminalId : null, branchId: branchId, from: from, to: to));

  Future<ReceiptData> receiptFor(String orderId) => _live(() => api.getReceipt(orderId));

  Future<ReturnableOrder> returnableFor(String orderId) => _live(() => api.getReturnable(orderId));

  Future<ReturnResult> createReturn(String orderId, ReturnRequest req) =>
      _live(() => api.createReturn(orderId: orderId, posTerminalId: posTerminalId, req: req));

  // ---------------------------------------------------------------------
  // Phase 13 shifts (erp-core-go internal/sales/shifts.go). Online-only:
  // opening, cash in/out and closing all need the server. The till keeps
  // selling offline; those sales count in the shift they were made in
  // once they sync (the server dates them by device time).
  // ---------------------------------------------------------------------

  /// The till's open shift, null when none (or not loaded yet).
  Shift? currentShift;

  /// Whether the merchant requires an open shift to take payment.
  bool shiftsRequired = false;

  /// Round-off waiver (Sales Policy): the most a sale may be settled below
  /// its total (0 = off), and whether the payment sheet starts with the
  /// paise waived. Last known values are kept for offline sales.
  double roundOffMax = 0;
  bool roundOffAuto = false;

  /// Sales Policy (migrations/063): this branch lists open-price items
  /// first on the grid. The server orders the live grid itself; this is
  /// kept for the offline grid.
  bool openPriceFirst = false;

  /// The branch's tax regime (Tax Settings, erp-core-go migrations/060):
  /// how a cart the server hasn't priced (offline) splits its tax. Last
  /// known values are kept for offline sales.
  TaxRegimeSettings taxRegime = const TaxRegimeSettings();

  /// The tax lines to show under a cart's subtotal: the server's split, or
  /// the same rule applied locally to an offline cart.
  List<TaxRow> taxRowsFor(OrderSummary order) =>
      order.taxBreakup?.rows.isNotEmpty == true
          ? order.taxBreakup!.rows
          : TaxBreakup.local(regime: taxRegime.regime, taxLabel: taxRegime.taxLabel, lineTaxes: order.lines.map((l) => l.taxAmount)).rows;

  /// The till's localization (migrations/064): language, text direction,
  /// cart side, the merchant's clock and the cash denominations. Loaded in
  /// [refreshShift], kept on the device ([restoreLocalization]) for the
  /// login screen and offline sales.
  PosLocalization localization = const PosLocalization();

  /// Bumped when the language, direction or cart side changes, so the app
  /// rebuilds every screen in the new language.
  int localeRevision = 0;

  static const _localizationPrefsKey = 'pos_localization';

  /// Applies [l] to the app-wide settings ([PosLocale], [PosClock]);
  /// returns whether the screens need to rebuild.
  static bool applyLocalization(PosLocalization l) {
    PosClock.utcOffsetMinutes = l.utcOffsetMinutes;
    return PosLocale.apply(language: l.language, textDirection: l.direction, cartSide: l.cartSide);
  }

  /// The last localization this device loaded, applied before anything is
  /// shown (the login screen speaks the till's language too).
  static Future<PosLocalization?> restoreLocalization() async {
    try {
      final raw = (await SharedPreferences.getInstance()).getString(_localizationPrefsKey);
      if (raw == null) return null;
      final l = PosLocalization.fromJson(jsonDecode(raw) as Map<String, dynamic>);
      applyLocalization(l);
      return l;
    } catch (_) {
      return null; // no storage (tests) or an unreadable entry: English
    }
  }

  Future<void> _loadLocalization() async {
    try {
      final l = await api.localization(posTerminalId);
      localization = l;
      if (applyLocalization(l)) localeRevision++;
      // Kept for the next start; never waited on, so a slow or missing
      // store can't hold up the refresh. Without storage it is simply
      // reloaded at the next sign-in.
      unawaited(SharedPreferences.getInstance()
          .then((p) => p.setString(_localizationPrefsKey, jsonEncode(l.toJson())))
          .then((_) {}, onError: (_) {}));
    } on ApiException {
      // an older server has no localization: keep English and INR notes
    }
  }

  /// Loads the open shift and the merchant's sales policy. Quietly does
  /// nothing offline; the status bar then shows the last known state.
  Future<void> refreshShift() async {
    if (offlineMode) return;
    try {
      final results = await Future.wait([api.currentShift(posTerminalId), api.salesSettings()]);
      currentShift = results[0] as Shift?;
      final settings = results[1] as SalesSettings;
      shiftsRequired = settings.shiftsRequired;
      roundOffMax = settings.roundOffMax;
      roundOffAuto = settings.roundOffAuto;
      openPriceFirst = settings.openPriceFirstAt(branchId);
      // An older server has no /tax/regime: keep the GST default.
      try {
        taxRegime = await api.taxRegime(branchId);
        PosCurrency.set(taxRegime.currencyCode, taxRegime.currencySymbol);
      } on ApiException {
        // keep the last known regime
      }
      await _loadLocalization();
      notifyListeners();
    } catch (e) {
      if (_isNetworkError(e)) {
        offlineMode = true;
        notifyListeners();
      }
    }
  }

  // ---------------------------------------------------------------------
  // Attendance (erp-core-go internal/hr/attendance.go). Kept here so the
  // status bar's check-in chip shows the cashier's state at a glance.
  // ---------------------------------------------------------------------

  /// The signed-in user's attendance, null when not loaded yet.
  MyAttendance? attendance;

  /// Loads the caller's attendance. Quietly does nothing offline; the
  /// status bar then shows the last known state.
  Future<void> refreshAttendance() async {
    if (offlineMode) return;
    try {
      attendance = await api.myAttendance();
      notifyListeners();
    } catch (e) {
      if (_isNetworkError(e)) {
        offlineMode = true;
        notifyListeners();
      }
    }
  }

  /// Sets the attendance the attendance screen just loaded or punched.
  void setAttendance(MyAttendance me) {
    attendance = me;
    notifyListeners();
  }

  Future<Shift> openShift(double openingFloat, String idempotencyKey) async {
    final s = await _live(() => api.openShift(terminalId: posTerminalId, openingFloat: openingFloat, idempotencyKey: idempotencyKey));
    currentShift = s;
    notifyListeners();
    return s;
  }

  Future<Shift> addCashMovement({
    required String kind,
    required double amount,
    required String reason,
    String? approverCode,
    String? approverPin,
    required String idempotencyKey,
  }) async {
    final s = await _live(() => api.addCashMovement(
          shiftId: currentShift!.shiftId,
          kind: kind,
          amount: amount,
          reason: reason,
          approverCode: approverCode,
          approverPin: approverPin,
          idempotencyKey: idempotencyKey,
        ));
    currentShift = s;
    notifyListeners();
    return s;
  }

  /// Returns the closed shift (with its Z report); the till then has no
  /// open shift.
  Future<Shift> closeShift(Map<double, int> denominations, {String? reason, String? approverCode, String? approverPin}) async {
    final closed = await _live(() => api.closeShift(
          shiftId: currentShift!.shiftId,
          denominations: denominations,
          reason: reason,
          approverCode: approverCode,
          approverPin: approverPin,
        ));
    currentShift = null;
    notifyListeners();
    return closed;
  }

  // ---------------------------------------------------------------------
  // X and Z reports (erp-core-go day_report.go): the X report is one shift
  // of a till, the Z report this branch's whole business day. Online only.
  // ---------------------------------------------------------------------

  Future<List<ShiftSummary>> listShifts({bool thisTerminalOnly = true, String? from, String? to}) =>
      _live(() => api.listShifts(branchId: branchId, terminalId: thisTerminalOnly ? posTerminalId : null, from: from, to: to));

  Future<Shift> loadShift(String shiftId) => _live(() => api.getShift(shiftId));

  Future<DayReport> loadDayReport(String date) => _live(() => api.zReport(branchId: branchId, date: date));

  Future<DayReport> closeDay(String date) => _live(() => api.closeDay(branchId: branchId, date: date));

  /// Prints a shift's X report on the receipt printer; false with
  /// [lastError] set when it couldn't.
  Future<bool> printShiftReport(String shiftId, PrinterService printer) =>
      _printReport(() => api.shiftReportBytes(shiftId), printer);

  /// Prints the branch's Z report for [date] on the receipt printer.
  Future<bool> printDayReport(String date, PrinterService printer) =>
      _printReport(() => api.zReportBytes(branchId: branchId, date: date), printer);

  Future<bool> _printReport(Future<List<int>> Function() bytes, PrinterService printer) async {
    _lastError = null;
    try {
      await printer.printBytes(await _live(bytes));
      return true;
    } catch (e) {
      _lastError = e is StateError
          ? e.message
          : e is ApiException
              ? e.message
              : tr('Could not print the report: {0}', [e]);
      notifyListeners();
      return false;
    }
  }

  // ---------------------------------------------------------------------
  // Phase 13 product lookup: the admin's own endpoints and permission
  // guards (pricing.manage, inventory.adjust), never a POS-only shortcut.
  // ---------------------------------------------------------------------

  /// The signed-in user's permissions; null until loaded (or when the
  /// server is unreachable), which [can] treats as "let the server decide".
  Set<String>? permissions;

  bool can(String code) => permissions == null || permissions!.contains(code);

  /// The merchant's manual-discount tiers (Phase 15 approval policy); the
  /// server's defaults until loaded. Only a hint: the server enforces them.
  List<DiscountTier> discountTiers = DiscountTier.defaults;

  Future<void> refreshPermissions() async {
    if (offlineMode) return;
    try {
      permissions = await api.myPermissions();
      notifyListeners();
    } catch (_) {
      // Unknown: actions stay enabled and the server's 403 is the answer.
    }
    try {
      discountTiers = await api.discountTiers();
      notifyListeners();
    } catch (_) {
      // Keep the defaults; the server still decides.
    }
  }

  Future<ProductDetailData> productDetail(String productId) => _live(() => api.productDetail(productId));

  Future<VariantStockData> variantStock(String variantId) => _live(() => api.variantStock(variantId, branchId));

  Future<void> updateSellingPrice(String variantId, double price, String reason, {bool override = false}) =>
      _live(() => api.updateSellingPrice(variantId: variantId, sellingPrice: price, reason: reason, override: override));

  Future<void> adjustStock(String variantId, double delta, String reason,
          {String? batchNo, String? expiryDate, String? approverCode, String? approverPin}) =>
      _live(() => api.adjustStock(
          variantId: variantId,
          branchId: branchId,
          delta: delta,
          reason: reason,
          batchNo: batchNo,
          expiryDate: expiryDate,
          approverCode: approverCode,
          approverPin: approverPin));

  /// Fetches the label's ESC/POS bytes and sends them to the configured
  /// network printer (the same one receipts use).
  Future<void> printLabel(PrinterService printer, String variantId, String template, int copies, {String? batchId}) async {
    final bytes = await _live(() => api.labelBytes(
          variantId: variantId,
          template: template,
          copies: copies,
          branchId: branchId,
          batchId: batchId,
        ));
    await printer.printBytes(bytes);
  }

  /// One key per return attempt; the return screen keeps it across retries.
  String newIdempotencyKey() => _newIdempotencyKey();

  bool _isNetworkError(Object e) =>
      e is SocketException || e is TimeoutException || e is http.ClientException;

  String _newIdempotencyKey() {
    final rand = Random().nextInt(1 << 31).toRadixString(16);
    return '${DateTime.now().microsecondsSinceEpoch}-$rand';
  }
}

/// An open-price item waiting for its price (Phase 13).
class OpenPricePrompt {
  final ProductLookup product;
  final OpenPriceRange range;
  final String suggestedPrice;

  OpenPricePrompt({required this.product, required this.range, required this.suggestedPrice});
}

/// The price the cashier typed, the approver when it needs one, and an
/// optional description of the item (erp-core-go migrations/062), printed
/// on the receipt and invoice instead of the catalog name.
class OpenPriceEntry {
  final double price;
  final String? approverCode;
  final String? approverPin;
  final String? description;

  OpenPriceEntry(this.price, {this.approverCode, this.approverPin, this.description});
}

/// One tile in the POS product grid (Phase 12). [available] is the cached
/// stock when the grid was filled from the offline catalog, null when it
/// came from the live search (which doesn't carry stock).
class GridProduct {
  final ProductLookup lookup;
  final String? categoryName;
  final double? available;

  /// An open-price item (the tile is marked and coloured, migrations/063).
  final bool openPrice;

  GridProduct({required this.lookup, required this.categoryName, required this.available, this.openPrice = false});
}
