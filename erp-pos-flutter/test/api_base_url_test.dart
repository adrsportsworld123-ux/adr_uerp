// The POS opened on the web from another device (LAN, VPN): a localhost
// API address follows the page's host; anything else is used as given.
import 'package:flutter_test/flutter_test.dart';

import 'package:erp_pos_app/main.dart';

void main() {
  test('a localhost API follows the page host on the web', () {
    expect(resolveApiBaseUrl('http://localhost:8080', isWeb: true, page: Uri.parse('http://10.242.1.193:60076/')), 'http://10.242.1.193:8080');
    expect(resolveApiBaseUrl('http://127.0.0.1:8080', isWeb: true, page: Uri.parse('http://192.168.1.20:60076/#/')), 'http://192.168.1.20:8080');
  });

  test('unchanged: opened on this PC, a real API address, or not on the web', () {
    expect(resolveApiBaseUrl('http://localhost:8080', isWeb: true, page: Uri.parse('http://localhost:60076/')), 'http://localhost:8080');
    expect(resolveApiBaseUrl('https://api.adrsw.com', isWeb: true, page: Uri.parse('http://10.242.1.193:60076/')), 'https://api.adrsw.com');
    expect(resolveApiBaseUrl('http://10.0.2.2:8080', isWeb: false, page: Uri.parse('file:///')), 'http://10.0.2.2:8080');
  });
}
