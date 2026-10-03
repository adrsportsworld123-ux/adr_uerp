import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:erp_pos_app/api/api_client.dart';

/// Session-expiry handling in ApiClient's retrying transport, against a
/// scripted fake backend (package:http/testing's MockClient). Mirrors the
/// real auth middleware's responses: 401 INVALID_TOKEN for a bad access
/// token, POST /auth/refresh rotating tokens or 401-ing a revoked one.
void main() {
  const base = 'http://test';
  final loginBody = jsonEncode({
    'access_token': 'access-1',
    'refresh_token': 'refresh-1',
    'expires_in': 900,
    'user_id': 'u1',
    'roles': ['POS User'],
  });
  http.Response invalidToken() =>
      http.Response(jsonEncode({'error': {'code': 'INVALID_TOKEN', 'message': 'expired'}}), 401);
  http.Response product() => http.Response(
      jsonEncode({
        'product_id': 'p',
        'product_name': 'Bat',
        'variant_id': 'v',
        'sku': 'S',
        'selling_price': '10.00',
        'mrp': '10.00',
      }),
      200);

  Future<(ApiClient, List<SessionEndReason>)> loggedIn(MockClientHandler handler) async {
    final api = ApiClient(
      baseUrl: base,
      httpClient: MockClient((req) async {
        if (req.url.path == '/api/v1/auth/login') return http.Response(loginBody, 200);
        return handler(req);
      }),
    );
    final ended = <SessionEndReason>[];
    api.onSessionExpired = ended.add;
    await api.login(merchantCode: 'm', email: 'e', password: 'p');
    return (api, ended);
  }

  test('active user: a rejected token is refreshed once and the request replayed', () async {
    var refreshes = 0;
    final seenTokens = <String?>[];
    final (api, ended) = await loggedIn((req) async {
      if (req.url.path == '/api/v1/auth/refresh') {
        refreshes++;
        expect(jsonDecode(req.body)['refresh_token'], 'refresh-1');
        return http.Response(loginBody.replaceAll('access-1', 'access-2').replaceAll('refresh-1', 'refresh-2'), 200);
      }
      seenTokens.add(req.headers['Authorization']);
      return req.headers['Authorization'] == 'Bearer access-2' ? product() : invalidToken();
    });

    final p = await api.lookupBarcode('123');
    expect(p.sku, 'S');
    expect(refreshes, 1);
    expect(seenTokens, ['Bearer access-1', 'Bearer access-2']);
    expect(ended, isEmpty);
    expect(api.hasSession, isTrue);
  });

  test('refresh rejected: session ends with SESSION_EXPIRED', () async {
    final (api, ended) = await loggedIn((req) async {
      if (req.url.path == '/api/v1/auth/refresh') {
        return http.Response(jsonEncode({'error': {'code': 'INVALID_TOKEN', 'message': 'revoked'}}), 401);
      }
      return invalidToken();
    });

    await expectLater(
      api.lookupBarcode('123'),
      throwsA(isA<ApiException>().having((e) => e.code, 'code', 'SESSION_EXPIRED')),
    );
    expect(ended, [SessionEndReason.revoked]);
    expect(api.hasSession, isFalse);
  });

  test('idle user: no silent refresh — the session ends as idle', () async {
    var refreshes = 0;
    final (api, ended) = await loggedIn((req) async {
      if (req.url.path == '/api/v1/auth/refresh') refreshes++;
      return invalidToken();
    });
    api.canRefresh = () => false;

    await expectLater(api.lookupBarcode('123'), throwsA(isA<ApiException>()));
    expect(refreshes, 0);
    expect(ended, [SessionEndReason.idle]);
  });

  test('server unreachable during refresh: the session is kept (offline-first)', () async {
    final (api, ended) = await loggedIn((req) async {
      if (req.url.path == '/api/v1/auth/refresh') throw http.ClientException('connection refused');
      return invalidToken();
    });

    await expectLater(
      api.lookupBarcode('123'),
      throwsA(isA<ApiException>().having((e) => e.code, 'code', 'INVALID_TOKEN')),
    );
    expect(ended, isEmpty);
    expect(api.hasSession, isTrue);
  });

  test('a business-level 401 is never treated as an expired session', () async {
    var refreshes = 0;
    final (api, ended) = await loggedIn((req) async {
      if (req.url.path == '/api/v1/auth/refresh') refreshes++;
      return http.Response(jsonEncode({'error': {'code': 'SOMETHING_ELSE', 'message': 'no'}}), 401);
    });

    await expectLater(api.lookupBarcode('123'), throwsA(isA<ApiException>()));
    expect(refreshes, 0);
    expect(ended, isEmpty);
  });

  test('concurrent rejected requests share a single refresh (tokens rotate)', () async {
    var refreshes = 0;
    final (api, _) = await loggedIn((req) async {
      if (req.url.path == '/api/v1/auth/refresh') {
        refreshes++;
        await Future<void>.delayed(const Duration(milliseconds: 20));
        return http.Response(loginBody.replaceAll('access-1', 'access-2'), 200);
      }
      return req.headers['Authorization'] == 'Bearer access-2' ? product() : invalidToken();
    });

    await Future.wait([api.lookupBarcode('1'), api.lookupBarcode('2'), api.lookupBarcode('3')]);
    expect(refreshes, 1);
  });
}
