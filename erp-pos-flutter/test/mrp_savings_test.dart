// MRP on cart lines and the savings rows (erp-core-go migrations/081):
// parsed from the server's additive fields, and absent from an older
// server's JSON without breaking anything.
import 'package:flutter_test/flutter_test.dart';

import 'package:erp_pos_app/api/api_client.dart';

Map<String, dynamic> _order(Map<String, dynamic> extra, Map<String, dynamic> lineExtra) => {
      'order_id': 'o1',
      'order_number': 'SO-1',
      'status': 'cart',
      'subtotal': '899.00',
      'discount_total': '50.00',
      'tax_total': '0.00',
      'grand_total': '849.00',
      'lines': [
        {
          'line_id': 'l1',
          'variant_id': 'v1',
          'sku': 'BAT',
          'product_name': 'Bat',
          'quantity': '1.000',
          'unit_price': '899.00',
          'discount_amount': '50.00',
          'tax_amount': '0.00',
          'line_total': '849.00',
          ...lineExtra,
        }
      ],
      ...extra,
    };

void main() {
  test('a line below MRP and the branch showing the savings', () {
    final o = OrderSummary.fromJson(_order(
      {'mrp_discount_total': '100.00', 'total_savings': '150.00', 'show_mrp_discount': true},
      {'mrp': '999.00', 'mrp_discount': '100.00'},
    ));
    expect(o.lines.single.mrp, '999.00');
    expect(o.lines.single.belowMrp, isTrue);
    expect(o.savings.visible, isTrue);
    expect(o.savings.hasMrpDiscount, isTrue);
    expect(o.savings.totalSavings, '150.00');
  });

  test('the branch not showing savings, and a line sold at MRP', () {
    final o = OrderSummary.fromJson(_order(
      {'mrp_discount_total': '0.00', 'total_savings': '50.00', 'show_mrp_discount': false},
      {'mrp': '899.00', 'mrp_discount': '0.00'},
    ));
    expect(o.lines.single.belowMrp, isFalse);
    expect(o.savings.visible, isFalse);
  });

  test('an older server sends none of it', () {
    final o = OrderSummary.fromJson(_order({}, {}));
    expect(o.lines.single.mrp, isNull);
    expect(o.lines.single.belowMrp, isFalse);
    expect(o.savings.visible, isFalse);
  });

  test('the receipt carries the customer mobile', () {
    final r = ReceiptData.fromJson({
      'order_number': 'SO-1',
      'status': 'finalized',
      'merchant_name': 'Acme',
      'branch_name': 'MG Road',
      'cashier_name': 'Ravi',
      'customer_name': 'Kumar',
      'customer_phone': '9876543210',
      'subtotal': '0.00',
      'discount_total': '0.00',
      'tax_total': '0.00',
      'grand_total': '0.00',
    });
    expect(r.customerPhone, '9876543210');
    expect(r.savings.visible, isFalse);
  });
}
