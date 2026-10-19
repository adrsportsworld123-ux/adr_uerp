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
      // v2 (erp-core-go migrations/033): catalog.price_includes_tax and
      // pending_order_lines.price_includes_tax, so an offline sale prices
      // a GST-inclusive product exactly like the server would.
      // v3 (erp-core-go migrations/039): open-price range on the cached
      // catalog, and pending_order_lines.open_price for a typed price.
      // v4 (Phase 13 sync queue): when each sale synced, and whether the
      // server flagged it for review (NEGATIVE_STOCK).
      // v5 (erp-core-go migrations/059): pending_orders.round_off, the
      // checkout waiver an offline sale took (signed, 0 = none).
      // v6 (erp-core-go migrations/062): pending_order_lines.description,
      // an open-price line's own description, sent with /sync/push.
      // v7 (erp-core-go migrations/063): catalog.pos_color, the product's
      // tile colour; the next pull is a full one so every product gets it.
      version: 7,
      onUpgrade: (db, oldVersion, newVersion) async {
        if (oldVersion < 2) {
          await db.execute('ALTER TABLE catalog ADD COLUMN price_includes_tax INTEGER NOT NULL DEFAULT 0');
          await db.execute('ALTER TABLE pending_order_lines ADD COLUMN price_includes_tax INTEGER NOT NULL DEFAULT 0');
          // The catalog pull is incremental (since=last_pull_at), so rows
          // cached before this column existed would never be re-sent and
          // would keep defaulting to exclusive. Forget the watermark to
          // force one full re-pull.
          await db.delete('sync_meta', where: 'key = ?', whereArgs: ['last_pull_at']);
        }
        if (oldVersion < 3) {
          await db.execute('ALTER TABLE catalog ADD COLUMN open_price_min TEXT');
          await db.execute('ALTER TABLE catalog ADD COLUMN open_price_max TEXT');
          await db.execute('ALTER TABLE catalog ADD COLUMN open_price_approval_above TEXT');
          await db.execute('ALTER TABLE pending_order_lines ADD COLUMN open_price INTEGER NOT NULL DEFAULT 0');
          // Same reason as v2: re-pull once so cached rows get the range.
          await db.delete('sync_meta', where: 'key = ?', whereArgs: ['last_pull_at']);
        }
        if (oldVersion < 4) {
          await db.execute('ALTER TABLE pending_orders ADD COLUMN synced_at TEXT');
          await db.execute('ALTER TABLE pending_orders ADD COLUMN needs_review INTEGER NOT NULL DEFAULT 0');
        }
        if (oldVersion < 6) {
          await db.execute('ALTER TABLE pending_order_lines ADD COLUMN description TEXT');
        }
        if (oldVersion < 7) {
          await db.execute('ALTER TABLE catalog ADD COLUMN pos_color TEXT');
          await db.execute("DELETE FROM sync_meta WHERE key = 'last_pull_at'");
        }
        if (oldVersion < 5) {
          await db.execute('ALTER TABLE pending_orders ADD COLUMN round_off REAL NOT NULL DEFAULT 0');
        }
      },
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
            cess_rate REAL NOT NULL,
            price_includes_tax INTEGER NOT NULL DEFAULT 0,
            open_price_min TEXT,
            open_price_max TEXT,
            open_price_approval_above TEXT,
            pos_color TEXT
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
            sync_error TEXT,
            synced_at TEXT,
            needs_review INTEGER NOT NULL DEFAULT 0,
            round_off REAL NOT NULL DEFAULT 0
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
            price_includes_tax INTEGER NOT NULL DEFAULT 0,
            open_price INTEGER NOT NULL DEFAULT 0,
            description TEXT,
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
