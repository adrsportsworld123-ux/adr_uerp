import 'dart:math';

import 'package:sqflite/sqflite.dart';

import '../api/api_client.dart';
import 'local_db.dart';

/// One cached catalog row (from GET /sync/pull), keyed by barcode — the
/// same shape a live GET /products/barcode/{code} would resolve to, so
/// offline and online barcode lookups can share the same call site in
/// AppSession.
class CachedProduct {
  final ProductLookup lookup;
  final double cgstRate, sgstRate, igstRate, cessRate;
  // true = lookup.sellingPrice already includes tax (erp-core-go migrations/033)
  final bool priceIncludesTax;
  CachedProduct(this.lookup, this.cgstRate, this.sgstRate, this.igstRate, this.cessRate, {this.priceIncludesTax = false});
}

/// Everything needed to POST one order to /sync/push, read back out of
/// pending_orders/lines/payments.
/// A cached catalog row plus its cached available stock (null when the
/// stock cache has no row for it yet).
class CachedCatalogItem {
  final CachedProduct product;
  final double? available;

  CachedCatalogItem(this.product, this.available);
}

class PendingOrderForSync {
  final String id;
  final String idempotencyKey;
  final String branchId;
  final String posTerminalId;
  final String deviceCreatedAt;
  final List<Map<String, dynamic>> lines;
  final List<Map<String, dynamic>> payments;

  /// The checkout round-off waiver, signed (0 = none).
  final double roundOff;
  PendingOrderForSync(this.id, this.idempotencyKey, this.branchId, this.posTerminalId,
      this.deviceCreatedAt, this.lines, this.payments, [this.roundOff = 0]);
}

/// One row of the POS sync queue screen.
class SyncQueueItem {
  final String id;
  final String orderNumber;
  final String createdAt;
  final double grandTotal;
  final double itemCount;
  final bool synced;
  final String? syncError;
  final String? syncedAt;
  final bool needsReview;
  final String? serverOrderId;

  SyncQueueItem({
    required this.id,
    required this.orderNumber,
    required this.createdAt,
    required this.grandTotal,
    required this.itemCount,
    required this.synced,
    this.syncError,
    this.syncedAt,
    this.needsReview = false,
    this.serverOrderId,
  });

  String get state => synced ? (needsReview ? 'needs_review' : 'synced') : (syncError != null ? 'failed' : 'queued');
}

class QueuedSale {
  final String id;
  final String orderNumber;
  final String createdAt;
  final double grandTotal;
  final double itemCount;
  final String? syncError;

  QueuedSale({
    required this.id,
    required this.orderNumber,
    required this.createdAt,
    required this.grandTotal,
    required this.itemCount,
    required this.syncError,
  });
}

/// Owns all reads/writes to the local sqflite store — the offline
/// counterpart to ApiClient. AppSession decides which of the two to call;
/// this class doesn't know about connectivity at all.
class OfflineStore {
  Future<void> cacheCatalog(List<dynamic> entries) async {
    final db = await LocalDb.instance.db;
    final batch = db.batch();
    for (final e in entries) {
      batch.insert('catalog', {
        'barcode': e['barcode'],
        'product_id': e['product_id'],
        'product_name': e['product_name'],
        'hsn_code': e['hsn_code'],
        'variant_id': e['variant_id'],
        'sku': e['sku'],
        'selling_price': e['selling_price'],
        'mrp': e['mrp'],
        'cgst_rate': (e['cgst_rate'] as num).toDouble(),
        'sgst_rate': (e['sgst_rate'] as num).toDouble(),
        'igst_rate': (e['igst_rate'] as num).toDouble(),
        'cess_rate': (e['cess_rate'] as num).toDouble(),
        'price_includes_tax': (e['price_includes_tax'] as bool? ?? false) ? 1 : 0,
        'open_price_min': (e['open_price'] as Map<String, dynamic>?)?['min'],
        'open_price_max': (e['open_price'] as Map<String, dynamic>?)?['max'],
        'open_price_approval_above': (e['open_price'] as Map<String, dynamic>?)?['approval_above'],
      }, conflictAlgorithm: ConflictAlgorithm.replace);
    }
    await batch.commit(noResult: true);
  }

  Future<void> cacheStock(List<dynamic> entries) async {
    final db = await LocalDb.instance.db;
    final batch = db.batch();
    for (final e in entries) {
      batch.insert('stock_cache', {
        'variant_id': e['variant_id'],
        'on_hand': e['on_hand'],
        'reserved': e['reserved'],
        'available': e['available'],
      }, conflictAlgorithm: ConflictAlgorithm.replace);
    }
    await batch.commit(noResult: true);
  }

  Future<String?> getLastPullAt() async {
    final db = await LocalDb.instance.db;
    final rows = await db.query('sync_meta', where: 'key = ?', whereArgs: ['last_pull_at']);
    return rows.isEmpty ? null : rows.first['value'] as String;
  }

  Future<void> setLastPullAt(String serverTime) async {
    final db = await LocalDb.instance.db;
    await db.insert('sync_meta', {'key': 'last_pull_at', 'value': serverTime},
        conflictAlgorithm: ConflictAlgorithm.replace);
  }

  Future<CachedProduct?> lookupBarcode(String code) async {
    final db = await LocalDb.instance.db;
    final rows = await db.query('catalog', where: 'barcode = ?', whereArgs: [code]);
    if (rows.isEmpty) return null;
    final r = rows.first;
    return _productFromRow(r);
  }

  static CachedProduct _productFromRow(Map<String, Object?> r) {
    final min = r['open_price_min'] as String?;
    final max = r['open_price_max'] as String?;
    return CachedProduct(
      ProductLookup(
        productId: r['product_id'] as String,
        productName: r['product_name'] as String,
        variantId: r['variant_id'] as String,
        sku: r['sku'] as String,
        sellingPrice: r['selling_price'] as String,
        mrp: r['mrp'] as String,
        openPrice: min == null || max == null
            ? null
            : OpenPriceRange(min: min, max: max, approvalAbove: r['open_price_approval_above'] as String?),
      ),
      r['cgst_rate'] as double,
      r['sgst_rate'] as double,
      r['igst_rate'] as double,
      r['cess_rate'] as double,
      priceIncludesTax: (r['price_includes_tax'] as int? ?? 0) == 1,
    );
  }

  /// Name/SKU search over the cached catalog (Phase 12) — what lets the
  /// POS product grid and search keep working offline. One row per
  /// variant (the cache is keyed by barcode, so a variant with two
  /// barcodes appears twice there), with its cached available stock.
  /// An empty query returns the first [limit] products by name.
  Future<List<CachedCatalogItem>> searchCatalog(String query, {int limit = 40}) async {
    final db = await LocalDb.instance.db;
    final q = query.trim().toLowerCase();
    // instr() rather than LIKE: a typed % or _ is matched literally, with
    // no escaping rules to get wrong.
    final rows = await db.rawQuery(
      'SELECT c.*, s.available AS available '
      'FROM catalog c LEFT JOIN stock_cache s ON s.variant_id = c.variant_id '
      "WHERE (? = '' OR instr(lower(c.product_name), ?) > 0 OR instr(lower(c.sku), ?) > 0 OR c.barcode = ?) "
      'GROUP BY c.variant_id '
      'ORDER BY c.product_name, c.sku '
      'LIMIT ?',
      [q, q, q, query.trim(), limit],
    );
    return rows
        .map((r) => CachedCatalogItem(_productFromRow(r), double.tryParse(r['available'] as String? ?? '')))
        .toList();
  }

  Future<String> createPendingOrder(String branchId, String posTerminalId) async {
    final db = await LocalDb.instance.db;
    final id = _newId();
    await db.insert('pending_orders', {
      'id': id,
      'idempotency_key': _newId(),
      'branch_id': branchId,
      'pos_terminal_id': posTerminalId,
      'device_created_at': DateTime.now().toUtc().toIso8601String(),
      'status': 'building',
    });
    return id;
  }

  /// [unitPrice] is a typed open price (Phase 13); the caller has already
  /// checked it against the item's range.
  Future<OrderSummary> addLine(String pendingOrderId, CachedProduct product, double quantity, {double? unitPrice, String? description}) async {
    final db = await LocalDb.instance.db;
    final openPrice = unitPrice != null;
    unitPrice ??= double.parse(product.lookup.sellingPrice);
    final taxRatePct = product.cgstRate + product.sgstRate + product.igstRate + product.cessRate;
    // Mirrors erp-core-go's internal/taxcalc.ComputeLine exactly, so a sale
    // rung up offline bills the same total the server would have:
    // exclusive adds tax on top; inclusive extracts it from the line's
    // whole total (3 x 1000 incl. 18% = exactly 3000.00, tax 457.63).
    final gross = _round2(unitPrice * quantity);
    final double taxAmount;
    final double lineTotal;
    if (product.priceIncludesTax) {
      final taxable = _round2(gross * 100 / (100 + taxRatePct));
      taxAmount = _round2(gross - taxable);
      lineTotal = gross;
    } else {
      taxAmount = _round2(gross * taxRatePct / 100);
      lineTotal = _round2(gross + taxAmount);
    }

    await db.insert('pending_order_lines', {
      'id': _newId(),
      'pending_order_id': pendingOrderId,
      'variant_id': product.lookup.variantId,
      'sku': product.lookup.sku,
      // An open-price line's own description (migrations/062) is what the
      // cart and receipt show; it syncs as description.
      'product_name': (openPrice && description != null && description.trim().isNotEmpty) ? description.trim() : product.lookup.productName,
      'description': (openPrice && description != null && description.trim().isNotEmpty) ? description.trim() : null,
      'quantity': quantity,
      'unit_price': unitPrice,
      'discount_amount': 0,
      'tax_amount': taxAmount,
      'line_total': lineTotal,
      'price_includes_tax': product.priceIncludesTax ? 1 : 0,
      'open_price': openPrice ? 1 : 0,
    });

    return _recalcAndLoad(pendingOrderId);
  }

  Future<OrderSummary> removeLine(String pendingOrderId, String lineId) async {
    final db = await LocalDb.instance.db;
    await db.delete('pending_order_lines', where: 'id = ?', whereArgs: [lineId]);
    return _recalcAndLoad(pendingOrderId);
  }

  /// Marks the order ready to sync and records its payments. Status is set
  /// to 'finalized' immediately (not 'pending_sync') so the existing UI —
  /// which only ever checks `order.status == 'cart'` to decide whether
  /// Checkout is still available — treats a completed offline sale exactly
  /// like a completed online one, pending sync in the background.
  /// [roundOff]: the waiver taken (signed, 0 = none), synced with the sale.
  Future<OrderSummary> checkout(String pendingOrderId, List<PaymentInput> payments, {double roundOff = 0}) async {
    final db = await LocalDb.instance.db;
    final batch = db.batch();
    for (final p in payments) {
      batch.insert('pending_payments', {
        'id': _newId(),
        'pending_order_id': pendingOrderId,
        'method': p.method,
        'amount': p.amount,
      });
    }
    batch.update('pending_orders', {'status': 'pending_sync', 'round_off': roundOff},
        where: 'id = ?', whereArgs: [pendingOrderId]);
    await batch.commit(noResult: true);
    return _recalcAndLoad(pendingOrderId, statusOverride: 'finalized');
  }

  Future<List<PendingOrderForSync>> listUnsynced() async {
    final db = await LocalDb.instance.db;
    final orders = await db.query('pending_orders', where: 'status = ?', whereArgs: ['pending_sync']);
    final result = <PendingOrderForSync>[];
    for (final o in orders) {
      final id = o['id'] as String;
      final lines = await db.query('pending_order_lines', where: 'pending_order_id = ?', whereArgs: [id]);
      final payments = await db.query('pending_payments', where: 'pending_order_id = ?', whereArgs: [id]);
      result.add(PendingOrderForSync(
        id,
        o['idempotency_key'] as String,
        o['branch_id'] as String,
        o['pos_terminal_id'] as String,
        o['device_created_at'] as String,
        lines,
        payments,
        (o['round_off'] as num?)?.toDouble() ?? 0,
      ));
    }
    return result;
  }

  /// [needsReview]: the server accepted the sale but it oversold stock
  /// (NEGATIVE_STOCK), so it waits in the back office's Stock Review.
  Future<void> markSynced(String pendingOrderId, String serverOrderId, {bool needsReview = false}) async {
    final db = await LocalDb.instance.db;
    await db.update(
        'pending_orders',
        {
          'status': 'synced',
          'synced_order_id': serverOrderId,
          'sync_error': null,
          'synced_at': DateTime.now().toUtc().toIso8601String(),
          'needs_review': needsReview ? 1 : 0,
        },
        where: 'id = ?',
        whereArgs: [pendingOrderId]);
  }

  /// Phase 13 sync queue: everything waiting to sync (failed ones carry
  /// their error), then the most recent synced sales, newest first.
  Future<List<SyncQueueItem>> listSyncQueue({int syncedLimit = 30}) async {
    final db = await LocalDb.instance.db;
    const cols = '''
      o.id, o.device_created_at, o.grand_total, o.sync_error, o.status, o.synced_at, o.needs_review, o.synced_order_id,
      (SELECT COALESCE(SUM(quantity), 0) FROM pending_order_lines l WHERE l.pending_order_id = o.id) AS items''';
    final pending = await db.rawQuery("SELECT $cols FROM pending_orders o WHERE o.status = 'pending_sync' ORDER BY o.device_created_at DESC");
    final synced = await db.rawQuery(
        "SELECT $cols FROM pending_orders o WHERE o.status = 'synced' ORDER BY COALESCE(o.synced_at, o.device_created_at) DESC LIMIT ?",
        [syncedLimit]);
    return [...pending, ...synced]
        .map((r) => SyncQueueItem(
              id: r['id'] as String,
              orderNumber: 'OFFLINE-${(r['id'] as String).substring(0, 8)}',
              createdAt: r['device_created_at'] as String,
              grandTotal: ((r['grand_total'] as num?) ?? 0).toDouble(),
              itemCount: ((r['items'] as num?) ?? 0).toDouble(),
              synced: r['status'] == 'synced',
              syncError: r['sync_error'] as String?,
              syncedAt: r['synced_at'] as String?,
              needsReview: (r['needs_review'] as int? ?? 0) == 1,
              serverOrderId: r['synced_order_id'] as String?,
            ))
        .toList();
  }

  Future<void> markSyncFailed(String pendingOrderId, String error) async {
    final db = await LocalDb.instance.db;
    await db.update('pending_orders', {'sync_error': error}, where: 'id = ?', whereArgs: [pendingOrderId]);
  }

  /// Sales finished on this device that the server hasn't accepted yet
  /// (Phase 13 history: shown as "queued" above the server's list).
  Future<List<QueuedSale>> listQueuedSales() async {
    final db = await LocalDb.instance.db;
    final rows = await db.rawQuery('''
      SELECT o.id, o.device_created_at, o.grand_total, o.sync_error,
             (SELECT COALESCE(SUM(quantity), 0) FROM pending_order_lines l WHERE l.pending_order_id = o.id) AS items
      FROM pending_orders o WHERE o.status = 'pending_sync'
      ORDER BY o.device_created_at DESC''');
    return rows
        .map((r) => QueuedSale(
              id: r['id'] as String,
              orderNumber: 'OFFLINE-${(r['id'] as String).substring(0, 8)}',
              createdAt: r['device_created_at'] as String,
              grandTotal: ((r['grand_total'] as num?) ?? 0).toDouble(),
              itemCount: ((r['items'] as num?) ?? 0).toDouble(),
              syncError: r['sync_error'] as String?,
            ))
        .toList();
  }

  Future<int> countPendingSync() async {
    final db = await LocalDb.instance.db;
    final rows = await db.query('pending_orders', where: 'status = ?', whereArgs: ['pending_sync']);
    return rows.length;
  }

  Future<OrderSummary> _recalcAndLoad(String pendingOrderId, {String? statusOverride}) async {
    final db = await LocalDb.instance.db;
    final lineRows = await db.query('pending_order_lines', where: 'pending_order_id = ?', whereArgs: [pendingOrderId]);

    double subtotal = 0, discountTotal = 0, taxTotal = 0, grandTotal = 0;
    final lines = <OrderLine>[];
    for (final r in lineRows) {
      final unitPrice = r['unit_price'] as double;
      final quantity = r['quantity'] as double;
      final discountAmount = r['discount_amount'] as double;
      final taxAmount = r['tax_amount'] as double;
      final lineTotal = r['line_total'] as double;
      // Same subtotal definition as the server's recalcOrderTotals:
      // pre-discount taxable value, so subtotal - discount + tax = total
      // for an inclusive line too (where unit_price already contains tax).
      subtotal += (r['price_includes_tax'] as int? ?? 0) == 1 ? lineTotal - taxAmount + discountAmount : unitPrice * quantity;
      discountTotal += discountAmount;
      taxTotal += taxAmount;
      grandTotal += lineTotal;
      lines.add(OrderLine(
        lineId: r['id'] as String,
        variantId: r['variant_id'] as String,
        sku: r['sku'] as String,
        productName: r['product_name'] as String,
        quantity: quantity.toString(),
        unitPrice: unitPrice.toStringAsFixed(2),
        discountAmount: discountAmount.toStringAsFixed(2),
        taxAmount: taxAmount.toStringAsFixed(2),
        lineTotal: lineTotal.toStringAsFixed(2),
        openPrice: (r['open_price'] as int? ?? 0) == 1,
      ));
    }

    await db.update(
        'pending_orders',
        {
          'subtotal': subtotal,
          'discount_total': discountTotal,
          'tax_total': taxTotal,
          'grand_total': grandTotal,
        },
        where: 'id = ?',
        whereArgs: [pendingOrderId]);

    final orderRow = (await db.query('pending_orders', where: 'id = ?', whereArgs: [pendingOrderId])).first;

    final roundOff = (orderRow['round_off'] as num?)?.toDouble() ?? 0;
    return OrderSummary(
      orderId: pendingOrderId,
      orderNumber: 'OFFLINE-${pendingOrderId.substring(0, 8)}',
      status: statusOverride ?? ((orderRow['status'] as String) == 'building' ? 'cart' : 'finalized'),
      subtotal: subtotal.toStringAsFixed(2),
      discountTotal: discountTotal.toStringAsFixed(2),
      taxTotal: taxTotal.toStringAsFixed(2),
      grandTotal: grandTotal.toStringAsFixed(2),
      roundOff: roundOff.toStringAsFixed(2),
      payableTotal: _round2(grandTotal + roundOff).toStringAsFixed(2),
      lines: lines,
    );
  }

  double _round2(double v) => (v * 100).round() / 100;

  String _newId() {
    final rand = Random().nextInt(1 << 31).toRadixString(16);
    return '${DateTime.now().microsecondsSinceEpoch}-$rand';
  }
}
