import 'dart:io';

import 'package:path/path.dart' as p;
import 'package:path_provider/path_provider.dart';

/// Writes [bytes] into the device's Downloads folder (Windows/macOS/Linux:
/// the user's Downloads; Android: this app's own Downloads folder, which
/// needs no storage permission) and returns the full path. A file that
/// already exists is never overwritten: "receipt-X.pdf" becomes
/// "receipt-X (2).pdf", so a reprint can't silently replace one the
/// cashier already handed over.
Future<String> saveDocument(String filename, List<int> bytes) async {
  Directory? dir;
  try {
    dir = await getDownloadsDirectory();
  } catch (_) {
    dir = null; // not every platform has one
  }
  dir ??= await getApplicationDocumentsDirectory();
  await dir.create(recursive: true);

  final safe = filename.replaceAll(RegExp(r'[\/:*?"<>|]'), '-');
  final base = p.basenameWithoutExtension(safe);
  final ext = p.extension(safe);
  var path = p.join(dir.path, safe);
  for (var n = 2; await File(path).exists(); n++) {
    path = p.join(dir.path, '$base ($n)$ext');
  }
  await File(path).writeAsBytes(bytes, flush: true);
  return path;
}

/// Desktop tills can hand the saved file to the system's PDF viewer (to
/// print it on an A4 printer, or attach it to an email). Mobile can't
/// without another plugin, so the cashier is shown where it was saved.
bool get canOpenSavedDocuments => Platform.isWindows || Platform.isMacOS || Platform.isLinux;

Future<void> openSavedDocument(String path) async {
  if (Platform.isWindows) {
    await Process.start('cmd', ['/c', 'start', '', path], runInShell: false);
  } else if (Platform.isMacOS) {
    await Process.start('open', [path]);
  } else if (Platform.isLinux) {
    await Process.start('xdg-open', [path]);
  }
}
