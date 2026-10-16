import 'dart:io';

import 'package:sqflite_common_ffi/sqflite_ffi.dart';

/// Windows and Linux: SQLite through FFI. Android, iOS and macOS keep
/// sqflite's own plugin.
void initLocalDbPlatform() {
  if (Platform.isWindows || Platform.isLinux) {
    sqfliteFfiInit();
    databaseFactory = databaseFactoryFfi;
  }
}
