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
  CachedProduct(this.lookup, this.cgstRate, this.sgstRate, this.igstRate, this.cessRate);
}

/// Everything needed to POST one order to /sync/push, read back out of
/// pending_orders/lines/payments.
class PendingOrderForSync {
  final String id;
  final String idempotencyKey;
  final String branchId;
  final String posTerminalId;
  final String deviceCreatedAt;
  final List<Map<String, dynamic>> lines;
  final List<Map<String, dynamic>> payments;
  PendingOrderForSync(this.id, this.idempotencyKey, this.branchId, this.posTerminalId,
      this.deviceCreatedAt, this.lines, this.payments);
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
    return CachedProduct(
      ProductLookup(
        productId: r['product_id'] as String,
        productName: r['product_name'] as String,
        variantId: r['variant_id'] as String,
        sku: r['sku'] as String,
        sellingPrice: r['selling_price'] as String,
        mrp: r['mrp'] as String,
      ),
      r['cgst_rate'] as double,
      r['sgst_rate'] as double,
      r['igst_rate'] as double,
      r['cess_rate'] as double,
    );
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

  Future<OrderSummary> addLine(String pendingOrderId, CachedProduct product, double quantity) async {
    final db = await LocalDb.instance.db;
    final unitPrice = double.parse(product.lookup.sellingPrice);
    final lineSubtotal = unitPrice * quantity;
    final taxRatePct = product.cgstRate + product.sgstRate + product.igstRate + product.cessRate;
    final taxAmount = _round2(lineSubtotal * taxRatePct / 100);
    final lineTotal = lineSubtotal + taxAmount;

    await db.insert('pending_order_lines', {
      'id': _newId(),
      'pending_order_id': pendingOrderId,
      'variant_id': product.lookup.variantId,
      'sku': product.lookup.sku,
      'product_name': product.lookup.productName,
      'quantity': quantity,
      'unit_price': unitPrice,
      'discount_amount': 0,
      'tax_amount': taxAmount,
      'line_total': lineTotal,
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
  Future<OrderSummary> checkout(String pendingOrderId, List<PaymentInput> payments) async {
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
    batch.update('pending_orders', {'status': 'pending_sync'},
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
      ));
    }
    return result;
  }

  Future<void> markSynced(String pendingOrderId, String serverOrderId) async {
    final db = await LocalDb.instance.db;
    await db.update('pending_orders', {'status': 'synced', 'synced_order_id': serverOrderId},
        where: 'id = ?', whereArgs: [pendingOrderId]);
  }

  Future<void> markSyncFailed(String pendingOrderId, String error) async {
    final db = await LocalDb.instance.db;
    await db.update('pending_orders', {'sync_error': error}, where: 'id = ?', whereArgs: [pendingOrderId]);
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
      subtotal += unitPrice * quantity;
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

    return OrderSummary(
      orderId: pendingOrderId,
      orderNumber: 'OFFLINE-${pendingOrderId.substring(0, 8)}',
      status: statusOverride ?? ((orderRow['status'] as String) == 'building' ? 'cart' : 'finalized'),
      subtotal: subtotal.toStringAsFixed(2),
      discountTotal: discountTotal.toStringAsFixed(2),
      taxTotal: taxTotal.toStringAsFixed(2),
      grandTotal: grandTotal.toStringAsFixed(2),
      lines: lines,
    );
  }

  double _round2(double v) => (v * 100).round() / 100;

  String _newId() {
    final rand = Random().nextInt(1 << 31).toRadixString(16);
    return '${DateTime.now().microsecondsSinceEpoch}-$rand';
  }
}
