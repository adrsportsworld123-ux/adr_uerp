import 'package:erp_pos_app/api/api_client.dart';
import 'package:flutter_test/flutter_test.dart';

// Phase 15 approval policy on the POS: the discount dialog's tier lookup
// and the return screen's refund threshold, as parsed from the server.
void main() {
  test('default discount tiers match the server defaults', () {
    const t = DiscountTier.defaults;
    expect(DiscountTier.forPercent(t, 5)!.approver, 'none');
    expect(DiscountTier.forPercent(t, 5.5)!.approver, 'branch_manager');
    expect(DiscountTier.forPercent(t, 15)!.approver, 'branch_manager');
    expect(DiscountTier.forPercent(t, 25)!.approver, 'merchant_admin');
    expect(DiscountTier.forPercent(t, 25.01), isNull);
  });

  test('a merchant policy replaces the tiers', () {
    final tiers = [
      DiscountTier.fromJson({'up_to': 10, 'approver': 'none'}),
      DiscountTier.fromJson({'up_to': 40, 'approver': 'merchant_admin'}),
    ];
    expect(DiscountTier.forPercent(tiers, 8)!.approver, 'none');
    expect(DiscountTier.forPercent(tiers, 30)!.approver, 'merchant_admin');
    expect(DiscountTier.forPercent(tiers, 41), isNull);
  });

  test('returnable carries the refund approval threshold', () {
    final base = {'order_id': 'o', 'order_number': 'S-1', 'status': 'finalized', 'requires_approval': true, 'lines': []};
    expect(ReturnableOrder.fromJson({...base, 'approval_above': 5000}).approvalAbove, 5000);
    expect(ReturnableOrder.fromJson({...base, 'approval_above': null}).approvalAbove, isNull);
    expect(ReturnableOrder.fromJson(base).approvalAbove, isNull); // an older server
  });
}
