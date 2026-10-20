import 'package:flutter/widgets.dart';

import 'strings_ar.dart';
import 'strings_es.dart';
import 'strings_fr.dart';
import 'strings_hi.dart';
import 'strings_kn.dart';
import 'strings_ml.dart';
import 'strings_ta.dart';
import 'strings_te.dart';

/// The till's language, text direction and cart side (erp-core-go
/// migrations/064): the merchant's localization with this terminal's
/// overrides, from GET /settings/localization/terminal. AppSession sets it
/// in refreshShift() and keeps it for the next start, so the login screen
/// and an offline till speak the same language.
///
/// Every on-screen string is written in English and looked up with [tr], so
/// English (the default) shows exactly the text written in the code, and a
/// string with no translation yet falls back to English instead of a key.
class PosLocale {
  PosLocale._();

  static const supported = ['en', 'ta', 'hi', 'ar', 'ml', 'te', 'kn', 'fr', 'es'];
  static const _rtl = {'ar'};

  static String _language = 'en';
  static TextDirection _direction = TextDirection.ltr;
  static bool _cartOnLeft = false;

  static String get language => _language;
  static TextDirection get direction => _direction;

  /// The cart panel's side on a wide till; the default is the right.
  static bool get cartOnLeft => _cartOnLeft;

  static Locale get locale => Locale(_language);

  /// Applies settings; [textDirection] is auto, ltr or rtl (auto follows
  /// the language), [cartSide] left or right. Returns whether anything
  /// changed, so the caller rebuilds only then.
  static bool apply({String? language, String? textDirection, String? cartSide}) {
    final lang = supported.contains(language) ? language! : 'en';
    final dir = switch (textDirection) {
      'rtl' => TextDirection.rtl,
      'ltr' => TextDirection.ltr,
      _ => _rtl.contains(lang) ? TextDirection.rtl : TextDirection.ltr,
    };
    final left = cartSide == 'left';
    final changed = lang != _language || dir != _direction || left != _cartOnLeft;
    _language = lang;
    _direction = dir;
    _cartOnLeft = left;
    return changed;
  }

  /// Back to English, left-to-right, cart on the right (tests).
  static void reset() => apply();
}

const Map<String, Map<String, String>> _dictionaries = {
  'ta': stringsTa,
  'hi': stringsHi,
  'ar': stringsAr,
  'ml': stringsMl,
  'te': stringsTe,
  'kn': stringsKn,
  'fr': stringsFr,
  'es': stringsEs,
};

final _placeholder = RegExp(r'\{(\d+)\}');

/// [en] in the till's language; `{0}`, `{1}` … in it are replaced by
/// [args], in that order (a translation may move them).
String tr(String en, [List<Object?> args = const []]) {
  final text = _dictionaries[PosLocale.language]?[en] ?? en;
  if (args.isEmpty) return text;
  return text.replaceAllMapped(_placeholder, (m) {
    final i = int.parse(m[1]!);
    return i < args.length ? '${args[i] ?? ''}' : m[0]!;
  });
}

/// Every string [tr] can translate, for the completeness test.
Iterable<String> translatedLanguages() => _dictionaries.keys;
Map<String, String> dictionaryFor(String language) => _dictionaries[language] ?? const {};
