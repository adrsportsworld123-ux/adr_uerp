/// Indian-style rupee formatting for DISPLAY only (₹1,23,456.00). Money
/// arrives from the API as NUMERIC strings and goes back as given; this
/// never feeds a value into arithmetic that is sent to the server.
String formatRupees(String amount) {
  final v = double.tryParse(amount);
  if (v == null) return '₹$amount';
  return formatRupeesNum(v);
}

String formatRupeesNum(double v) {
  final negative = v < 0;
  final fixed = v.abs().toStringAsFixed(2);
  final parts = fixed.split('.');
  final whole = parts[0];
  String grouped;
  if (whole.length <= 3) {
    grouped = whole;
  } else {
    final last3 = whole.substring(whole.length - 3);
    var rest = whole.substring(0, whole.length - 3);
    final pairs = <String>[];
    while (rest.length > 2) {
      pairs.insert(0, rest.substring(rest.length - 2));
      rest = rest.substring(0, rest.length - 2);
    }
    if (rest.isNotEmpty) pairs.insert(0, rest);
    grouped = '${pairs.join(',')},$last3';
  }
  return '${negative ? '−' : ''}₹$grouped.${parts[1]}';
}

/// Quantities come back as "2.000"; show "2" for whole units.
String formatQty(String qty) {
  final v = double.tryParse(qty);
  if (v == null) return qty;
  return v == v.roundToDouble() ? v.toStringAsFixed(0) : v.toString();
}

/// Order status as cashiers say it.
String statusLabel(String status) => switch (status) {
      'finalized' => 'Completed',
      'voided' => 'Voided',
      'refunded' => 'Refunded',
      'partially_refunded' => 'Partly refunded',
      'held' => 'On hold',
      'cart' => 'In progress',
      _ => status,
    };
