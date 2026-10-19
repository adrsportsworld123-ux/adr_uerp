// The tax split the till shows (erp-core-go migrations/060): the server's
// rows when it sends them, and the same rule applied to an offline cart.
import 'package:flutter_test/flutter_test.dart';

import 'package:erp_pos_app/api/api_client.dart';
import 'package:erp_pos_app/ui/format.dart';

void main() {
  test('an offline cart splits each line into CGST + SGST, the odd paisa to CGST', () {
    final b = TaxBreakup.local(regime: 'gst_split', taxLabel: 'GST', lineTaxes: ['1.21', '246.32', '0.05']);
    expect(b.rows.map((r) => '${r.label} ${r.amount}'), ['CGST 123.80', 'SGST 123.78']);
    // The halves always add back up to the tax.
    expect((double.parse(b.rows[0].amount) + double.parse(b.rows[1].amount)).toStringAsFixed(2), '247.58');
  });

  test('a single-tax merchant shows one line under its own label', () {
    final b = TaxBreakup.local(regime: 'single', taxLabel: 'VAT', lineTaxes: ['5.00', '2.50']);
    expect(b.rows.map((r) => '${r.label} ${r.amount}'), ['VAT 7.50']);
  });

  test("the server's breakup is read as sent, and an older server sends none", () {
    final o = OrderSummary.fromJson({
      'order_id': 'o', 'order_number': 'N', 'status': 'cart', 'subtotal': '24.29', 'discount_total': '0.00',
      'tax_total': '1.21', 'grand_total': '25.50',
      'tax_breakup': {
        'regime': 'gst_split', 'tax_label': 'GST', 'inter_state': true, 'place_of_supply': '29', 'place_of_supply_name': 'Karnataka',
        'rows': [
          {'label': 'IGST @ 5%', 'amount': '1.21'},
        ],
      },
    });
    expect(o.taxBreakup!.interState, isTrue);
    expect(o.taxBreakup!.rows.single.label, 'IGST @ 5%');
    final old = OrderSummary.fromJson({
      'order_id': 'o', 'order_number': 'N', 'status': 'cart', 'subtotal': '1', 'discount_total': '0', 'tax_total': '0', 'grand_total': '1',
    });
    expect(old.taxBreakup, isNull);
  });

  test('money is named in the branch currency, grouped the Indian way only for INR', () {
    expect(formatRupees('123456.5'), '₹1,23,456.50');
    PosCurrency.set('USD', r'$');
    expect(formatRupees('123456.5'), r'$123,456.50');
    expect(formatRupeesNum(-1234567), r'−$1,234,567.00');
    PosCurrency.set('AED', 'AED');
    expect(formatRupees('999.9'), 'AED 999.90');
    PosCurrency.set('INR', '₹');
    expect(formatRupees('2712.82'), '₹2,712.82');
    final r = TaxRegimeSettings.fromJson({'regime': 'single', 'tax_label': 'VAT', 'currency_code': 'AED', 'currency_symbol': 'AED'});
    expect(r.currencyCode, 'AED');
    expect(const TaxRegimeSettings().currencySymbol, '₹', reason: 'an older server: INR');
  });
}
