import 'package:flutter/material.dart';

/// ADR ERP design tokens for the POS — the same values as
/// `erp-web-admin/app/globals.css`, so a status reads the same on the till
/// and in the back office (Phase 11, `erp-core-go/docs/phased_roadmap.md`).
///
/// Geist ships as a bundled asset (`assets/fonts/`), not via google_fonts:
/// a terminal that boots offline must still render in the right face.
class AdrPalette {
  AdrPalette._();

  static const primaryLight = Color(0xFF1E56A0);
  static const primaryDark = Color(0xFF6E9EE6);
}

/// Status colors Material's [ColorScheme] has no slot for. Read with
/// `Theme.of(context).extension<PosColors>()!` (or [PosColorsX.pos]).
@immutable
class PosColors extends ThemeExtension<PosColors> {
  /// Money taken / completed / active / in stock. Only the Pay action is
  /// filled with this color on the till.
  final Color pay;
  final Color onPay;
  final Color paySoft;

  /// Pending / offline / queued / low stock.
  final Color warn;
  final Color warnSoft;

  /// Returned / needs approval.
  final Color plum;
  final Color plumSoft;

  /// Refunded / posted.
  final Color teal;
  final Color tealSoft;

  const PosColors({
    required this.pay,
    required this.onPay,
    required this.paySoft,
    required this.warn,
    required this.warnSoft,
    required this.plum,
    required this.plumSoft,
    required this.teal,
    required this.tealSoft,
  });

  static const light = PosColors(
    pay: Color(0xFF0A7752),
    onPay: Color(0xFFFFFFFF),
    paySoft: Color(0xFFDCF2E8),
    warn: Color(0xFF9B5B00),
    warnSoft: Color(0xFFFAEFD8),
    plum: Color(0xFF7445A8),
    plumSoft: Color(0xFFEFE6F8),
    teal: Color(0xFF0C7486),
    tealSoft: Color(0xFFDDF1F4),
  );

  static const dark = PosColors(
    pay: Color(0xFF39C08B),
    onPay: Color(0xFF062016),
    paySoft: Color(0xFF11302A),
    warn: Color(0xFFE5A63A),
    warnSoft: Color(0xFF33270F),
    plum: Color(0xFFB693E6),
    plumSoft: Color(0xFF2A2140),
    teal: Color(0xFF4EC1D0),
    tealSoft: Color(0xFF10303A),
  );

  @override
  PosColors copyWith({
    Color? pay,
    Color? onPay,
    Color? paySoft,
    Color? warn,
    Color? warnSoft,
    Color? plum,
    Color? plumSoft,
    Color? teal,
    Color? tealSoft,
  }) {
    return PosColors(
      pay: pay ?? this.pay,
      onPay: onPay ?? this.onPay,
      paySoft: paySoft ?? this.paySoft,
      warn: warn ?? this.warn,
      warnSoft: warnSoft ?? this.warnSoft,
      plum: plum ?? this.plum,
      plumSoft: plumSoft ?? this.plumSoft,
      teal: teal ?? this.teal,
      tealSoft: tealSoft ?? this.tealSoft,
    );
  }

  @override
  PosColors lerp(ThemeExtension<PosColors>? other, double t) {
    if (other is! PosColors) return this;
    return PosColors(
      pay: Color.lerp(pay, other.pay, t)!,
      onPay: Color.lerp(onPay, other.onPay, t)!,
      paySoft: Color.lerp(paySoft, other.paySoft, t)!,
      warn: Color.lerp(warn, other.warn, t)!,
      warnSoft: Color.lerp(warnSoft, other.warnSoft, t)!,
      plum: Color.lerp(plum, other.plum, t)!,
      plumSoft: Color.lerp(plumSoft, other.plumSoft, t)!,
      teal: Color.lerp(teal, other.teal, t)!,
      tealSoft: Color.lerp(tealSoft, other.tealSoft, t)!,
    );
  }
}

extension PosColorsX on ThemeData {
  PosColors get pos => extension<PosColors>() ?? PosColors.light;
}

/// Monospace style for SKUs, barcodes and invoice numbers.
const TextStyle kMonoStyle = TextStyle(fontFamily: 'GeistMono');

ThemeData posTheme(Brightness brightness) {
  final isLight = brightness == Brightness.light;
  final scheme = ColorScheme.fromSeed(
    seedColor: AdrPalette.primaryLight,
    brightness: brightness,
  ).copyWith(
    primary: isLight ? AdrPalette.primaryLight : AdrPalette.primaryDark,
    onPrimary: isLight ? Colors.white : const Color(0xFF0A1220),
    error: isLight ? const Color(0xFFB83729) : const Color(0xFFEF7262),
    surface: isLight ? Colors.white : const Color(0xFF151B24),
    onSurface: isLight ? const Color(0xFF131922) : const Color(0xFFE6EBF2),
    onSurfaceVariant: isLight ? const Color(0xFF5D6879) : const Color(0xFF97A3B5),
    outline: isLight ? const Color(0xFFDCE1E9) : const Color(0xFF2A3443),
    outlineVariant: isLight ? const Color(0xFFDCE1E9) : const Color(0xFF2A3443),
    surfaceContainerHighest: isLight ? const Color(0xFFEEF1F5) : const Color(0xFF1C2430),
  );
  final background = isLight ? const Color(0xFFF5F7FA) : const Color(0xFF0D1117);
  final radius = BorderRadius.circular(10);

  return ThemeData(
    useMaterial3: true,
    colorScheme: scheme,
    fontFamily: 'Geist',
    scaffoldBackgroundColor: background,
    dividerColor: scheme.outline,
    extensions: [isLight ? PosColors.light : PosColors.dark],
    // The POS app bar is filled with the brand color, so every action in
    // it reads as part of the terminal's status strip, not page content.
    appBarTheme: AppBarTheme(
      backgroundColor: scheme.primary,
      foregroundColor: scheme.onPrimary,
      elevation: 0,
      scrolledUnderElevation: 0,
      titleTextStyle: TextStyle(fontFamily: 'Geist', fontSize: 18, fontWeight: FontWeight.w600, color: scheme.onPrimary),
    ),
    cardTheme: CardThemeData(
      color: scheme.surface,
      elevation: 0,
      margin: EdgeInsets.zero,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14), side: BorderSide(color: scheme.outline)),
    ),
    // Touch-first: 48 dp minimum on every control (design rule "scan
    // first, tap second" — fingers, not a mouse).
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        minimumSize: const Size(64, 48),
        shape: RoundedRectangleBorder(borderRadius: radius),
        textStyle: const TextStyle(fontFamily: 'Geist', fontSize: 15, fontWeight: FontWeight.w600),
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        minimumSize: const Size(64, 48),
        shape: RoundedRectangleBorder(borderRadius: radius),
        side: BorderSide(color: scheme.outline),
        textStyle: const TextStyle(fontFamily: 'Geist', fontSize: 15, fontWeight: FontWeight.w500),
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(minimumSize: const Size(48, 48), shape: RoundedRectangleBorder(borderRadius: radius)),
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: scheme.surface,
      border: OutlineInputBorder(borderRadius: radius, borderSide: BorderSide(color: scheme.outline)),
      enabledBorder: OutlineInputBorder(borderRadius: radius, borderSide: BorderSide(color: scheme.outline)),
      focusedBorder: OutlineInputBorder(borderRadius: radius, borderSide: BorderSide(color: scheme.primary, width: 2)),
    ),
    dialogTheme: DialogThemeData(
      backgroundColor: scheme.surface,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(18)),
    ),
    snackBarTheme: const SnackBarThemeData(behavior: SnackBarBehavior.floating),
    listTileTheme: const ListTileThemeData(minVerticalPadding: 10),
  );
}
