import '../l10n/l10n.dart';

/// The branch's currency (Tax Settings, erp-core-go migrations/061), set
/// by `AppSession.refreshShift` and kept for offline sales. INR groups the
/// Indian way (₹1,23,456.00); any other currency in thousands ($123,456.00).
/// Amounts are never converted — this only names them.
class PosCurrency {
  static String code = 'INR';
  static String symbol = '₹';

  static void set(String newCode, String newSymbol) {
    if (newCode.isEmpty || newSymbol.isEmpty) return;
    code = newCode;
    symbol = newSymbol;
  }

  /// The symbol before an amount: a letter symbol gets a space ("AED 10.00").
  static String get prefix => RegExp(r'[A-Za-z]$').hasMatch(symbol) ? '$symbol ' : symbol;
}

/// The merchant's business clock (Localization, erp-core-go
/// migrations/064): times on the till read in the merchant's time zone,
/// whatever the device's own zone. Set by `AppSession.refreshShift` from the
/// zone's current offset and kept for the next start; unset, the device's
/// clock is used, as before.
class PosClock {
  static int? utcOffsetMinutes;

  /// [t] as wall-clock time on the merchant's clock (use its fields, not
  /// its zone).
  static DateTime toMerchant(DateTime t) {
    final off = utcOffsetMinutes;
    return off == null ? t.toLocal() : t.toUtc().add(Duration(minutes: off));
  }

  static DateTime now() => toMerchant(DateTime.now());

  /// An API timestamp on the merchant's clock; null when unparseable.
  static DateTime? parse(String? iso) {
    final t = iso == null ? null : DateTime.tryParse(iso);
    return t == null ? null : toMerchant(t);
  }
}

/// Money formatting for DISPLAY only, in the branch's currency. Money
/// arrives from the API as NUMERIC strings and goes back as given; this
/// never feeds a value into arithmetic that is sent to the server.
String formatRupees(String amount) {
  final v = double.tryParse(amount);
  if (v == null) return '${PosCurrency.prefix}$amount';
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
  } else if (PosCurrency.code != 'INR') {
    grouped = whole.replaceAllMapped(RegExp(r'\B(?=(\d{3})+(?!\d))'), (_) => ',');
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
  return '${negative ? '−' : ''}${PosCurrency.prefix}$grouped.${parts[1]}';
}

/// Quantities come back as "2.000"; show "2" for whole units.
String formatQty(String qty) {
  final v = double.tryParse(qty);
  if (v == null) return qty;
  return v == v.roundToDouble() ? v.toStringAsFixed(0) : v.toString();
}

/// Order status as cashiers say it.
String statusLabel(String status) => switch (status) {
      'finalized' => tr('Completed'),
      'voided' => tr('Voided'),
      'refunded' => tr('Refunded'),
      'partially_refunded' => tr('Partly refunded'),
      'held' => tr('On hold'),
      'held_voided' => tr('Released'),
      'cart' => tr('In progress'),
      _ => status,
    };
