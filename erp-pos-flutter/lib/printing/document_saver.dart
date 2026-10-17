// Saves a downloaded document (a receipt PDF) on the device. dart:io isn't
// available on web, hence the conditional import — the same split
// lib/local/db_platform.dart uses.
export 'document_saver_stub.dart' if (dart.library.io) 'document_saver_io.dart';
