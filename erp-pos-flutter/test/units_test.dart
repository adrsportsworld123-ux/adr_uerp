// Units at the till (erp-core-go migrations/076): a scan adds the barcode's
// pack (a tray of 30) and a line shows its unit. The offline catalog's
// pack is in offline_store_test.dart (it owns the local database).
import 'package:flutter_test/flutter_test.dart';

import 'package:erp_pos_app/api/api_client.dart';

void main() {
  test('a scan reads the pack and the unit', () {
    final tray = ProductLookup.fromJson({
      'product_id': 'p',
      'product_name': 'Eggs',
      'variant_id': 'v',
      'sku': 'EGG',
      'selling_price': '6.00',
      'mrp': '6.00',
      'pack_qty': '30.000',
      'pack_label': 'Tray of 30 pcs',
      'unit': {'unit_id': 'u', 'code': 'PCS', 'symbol': 'pcs', 'name': 'Pieces', 'kind': 'count', 'decimals': 0},
    });
    expect(tray.packQty, 30);
    expect(tray.unitSymbol, 'pcs');
    expect(tray.unitDecimals, 0);
    final old = ProductLookup.fromJson({'product_id': 'p', 'product_name': 'Bat', 'variant_id': 'v', 'sku': 'B', 'selling_price': '1.00', 'mrp': '1.00'});
    expect(old.packQty, 1, reason: 'a server without units: one, as before');
    expect(old.unitSymbol, isNull);
  });

  test('a line carries its unit; pieces print none', () {
    final line = OrderLine.fromJson({
      'line_id': 'l',
      'variant_id': 'v',
      'sku': 'RICE',
      'product_name': 'Rice',
      'quantity': '1.250',
      'unit_price': '60.00',
      'discount_amount': '0.00',
      'tax_amount': '0.00',
      'line_total': '75.00',
      'unit_symbol': 'kg',
      'unit_decimals': 3,
    });
    expect(line.unitSymbol, 'kg');
    expect(unitSuffix(line.unitSymbol), ' kg');
    expect(unitSuffix('pcs'), '');
    expect(unitSuffix(null), '');
  });
}
