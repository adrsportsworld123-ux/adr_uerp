/// Web build: there is no file system to save into.
Future<String> saveDocument(String filename, List<int> bytes) async =>
    throw UnsupportedError('Saving files is not supported on this platform');

/// Web build: nothing to open.
Future<void> openSavedDocument(String path) async {}

bool get canOpenSavedDocuments => false;
