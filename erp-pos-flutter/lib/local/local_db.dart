import 'package:path/path.dart' as p;
import 'package:sqflite/sqflite.dart';

/// The offline-sync local store: a cached catalog/stock snapshot (from
/// GET /sync/pull) and a queue of sales built while offline, waiting for
/// POST /sync/push. Opened lazily on first use, not at app startup — the
/// widget test never logs in or touches the cart, and sqflite has no
/// desktop/host test backend by default, so eager initialization would
/// break `flutter test` for no functional benefit.
class LocalDb {
  LocalDb._();
  static final LocalDb instance = LocalDb._();

  Database? _db;

  Future<Database> get db async {
    _db ??= await _open();
    return _db!;
  }

  Future<Database> _open() async {
    final path = p.join(await getDatabasesPath(), 'erp_pos_offline.db');
    return openDatabase(
      path,
      version: 1,
      onCreate: (db, version) async {
        await db.execute('''
          CREATE TABLE catalog (
            barcode TEXT PRIMARY KEY,
            product_id TEXT NOT NULL,
            product_name TEXT NOT NULL,
            hsn_code TEXT,
            variant_id TEXT NOT NULL,
            sku TEXT NOT NULL,
            selling_price TEXT NOT NULL,
            mrp TEXT NOT NULL,
            cgst_rate REAL NOT NULL,
            sgst_rate REAL NOT NULL,
            igst_rate REAL NOT NULL,
            cess_rate REAL NOT NULL
          )
        ''');
        await db.execute('''
          CREATE TABLE stock_cache (
            variant_id TEXT PRIMARY KEY,
            on_hand TEXT NOT NULL,
            reserved TEXT NOT NULL,
            available TEXT NOT NULL
          )
        ''');
        await db.execute('''
          CREATE TABLE sync_meta (
            key TEXT PRIMARY KEY,
            value TEXT NOT NULL
          )
        ''');
        await db.execute('''
          CREATE TABLE pending_orders (
            id TEXT PRIMARY KEY,
            idempotency_key TEXT NOT NULL,
            branch_id TEXT NOT NULL,
            pos_terminal_id TEXT NOT NULL,
            device_created_at TEXT NOT NULL,
            status TEXT NOT NULL DEFAULT 'building',
            subtotal REAL NOT NULL DEFAULT 0,
            discount_total REAL NOT NULL DEFAULT 0,
            tax_total REAL NOT NULL DEFAULT 0,
            grand_total REAL NOT NULL DEFAULT 0,
            synced_order_id TEXT,
            sync_error TEXT
          )
        ''');
        await db.execute('''
          CREATE TABLE pending_order_lines (
            id TEXT PRIMARY KEY,
            pending_order_id TEXT NOT NULL,
            variant_id TEXT NOT NULL,
            sku TEXT NOT NULL,
            product_name TEXT NOT NULL,
            quantity REAL NOT NULL,
            unit_price REAL NOT NULL,
            discount_amount REAL NOT NULL DEFAULT 0,
            tax_amount REAL NOT NULL,
            line_total REAL NOT NULL,
            FOREIGN KEY (pending_order_id) REFERENCES pending_orders (id)
          )
        ''');
        await db.execute('''
          CREATE TABLE pending_payments (
            id TEXT PRIMARY KEY,
            pending_order_id TEXT NOT NULL,
            method TEXT NOT NULL,
            amount REAL NOT NULL,
            FOREIGN KEY (pending_order_id) REFERENCES pending_orders (id)
          )
        ''');
      },
    );
  }
}
