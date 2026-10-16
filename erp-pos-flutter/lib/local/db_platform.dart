// Picks the SQLite implementation for this platform before the offline
// store first opens (LocalDb). Plain sqflite only ships Android, iOS and
// macOS implementations; on a Windows or Linux till it has no database
// factory, so every local read threw — the sales history screen hung on
// its spinner (it reads the offline queue before asking the server) and
// the offline catalog cache never filled. Those platforms use the same
// FFI SQLite the tests already run on. Web has no dart:ffi, hence the
// conditional import.
export 'db_platform_stub.dart' if (dart.library.ffi) 'db_platform_ffi.dart';
