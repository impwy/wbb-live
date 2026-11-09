import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

import '../config.dart';
import '../models/broadcast.dart';
import '../models/live_session.dart';
import '../models/user.dart';
import 'http_client.dart';

/// Error surfaced to the UI. Only safe fields (status/code/message) are kept.
class ApiException implements Exception {
  ApiException(this.status, this.code, this.message);

  final int status;
  final String code;
  final String message;

  bool get isUnauthorized => status == 401;

  @override
  String toString() => 'ApiException($status, $code, $message)';
}

class LoginResult {
  LoginResult({required this.accessToken, required this.user});

  final String accessToken;
  final User user;
}

/// Thin HTTP client for the WBB public API.
///
/// - The access JWT lives in memory only (never localStorage).
/// - The refresh cookie is HttpOnly and sent by the browser (withCredentials).
/// - A 401 on an authenticated call triggers one single-flight refresh and a
///   single retry, serializing concurrent calls within this ApiClient instance.
class ApiClient {
  ApiClient({http.Client? client, String baseUrl = kApiBaseUrl, this.onSessionExpired})
      : _http = client ?? createPlatformClient(),
        _baseUrl = baseUrl.endsWith('/') ? baseUrl.substring(0, baseUrl.length - 1) : baseUrl;

  final http.Client _http;
  final String _baseUrl;

  Uri get googleStartUri => Uri.base.resolve('$_baseUrl/auth/google/start');

  /// Fired when a refresh definitively fails with 401 (session dead) so the
  /// app can switch to signed-out and prompt a re-login. Set by the
  /// AuthController.
  void Function()? onSessionExpired;

  String? _accessToken;
  Future<LoginResult>? _refreshInFlight;

  set accessToken(String? token) => _accessToken = token;

  Future<Map<String, dynamic>> _request(
    String method,
    String path, {
    Object? body,
    bool auth = false,
    bool allowRefreshRetry = true,
    Map<String, String>? headers,
  }) async {
    final uri = Uri.parse('$_baseUrl$path');
    final request = http.Request(method, uri);
    if (headers != null) request.headers.addAll(headers);
    if (body != null) {
      request.headers['Content-Type'] = 'application/json';
      request.body = jsonEncode(body);
    }
    if (auth && _accessToken != null) {
      request.headers['Authorization'] = 'Bearer $_accessToken';
    }

    http.StreamedResponse streamed;
    try {
      streamed = await _http.send(request);
    } catch (_) {
      throw ApiException(0, 'network_error', '네트워크 오류가 발생했습니다. 잠시 후 다시 시도해 주세요.');
    }
    http.Response response;
    try {
      response = await http.Response.fromStream(streamed);
    } catch (_) {
      throw ApiException(0, 'network_error', '네트워크 오류가 발생했습니다. 잠시 후 다시 시도해 주세요.');
    }

    if (response.statusCode == 401 && auth && allowRefreshRetry && _accessToken != null) {
      final refreshed = await refresh();
      _accessToken = refreshed.accessToken;
      return _request(method, path, body: body, auth: auth, allowRefreshRetry: false, headers: headers);
    }

    if (response.statusCode == 204 || response.body.isEmpty) {
      if (response.statusCode >= 400) {
        throw ApiException(response.statusCode, 'error', '요청이 실패했습니다.');
      }
      return const {};
    }
    final Map<String, dynamic> decoded;
    try {
      decoded = jsonDecode(utf8.decode(response.bodyBytes)) as Map<String, dynamic>;
    } catch (_) {
      throw ApiException(0, 'invalid_response', '서버 응답을 해석할 수 없습니다.');
    }
    if (response.statusCode >= 400) {
      final err = decoded['error'] as Map<String, dynamic>?;
      throw ApiException(
        response.statusCode,
        err?['code'] as String? ?? 'error',
        err?['message'] as String? ?? 'Request failed.',
      );
    }
    return decoded;
  }

  // -- auth ----------------------------------------------------------------

  Future<bool> googleEnabled() async {
    final data = await _request('GET', '/auth/providers');
    return data['googleEnabled'] == true;
  }

  Future<User> register({
    required String email,
    required String password,
    required String displayName,
  }) async {
    final data = await _request('POST', '/auth/register', body: {
      'email': email,
      'password': password,
      'displayName': displayName,
    });
    return User.fromJson(data['user'] as Map<String, dynamic>);
  }

  Future<LoginResult> login(String email, String password) async {
    final data = await _request('POST', '/auth/login', body: {
      'email': email,
      'password': password,
    });
    final result = _parseLogin(data);
    _accessToken = result.accessToken;
    return result;
  }

  /// Refresh via the HttpOnly cookie. Single-flight within this client
  /// instance only (one browser tab); cross-tab races are made safe by the
  /// server's family revocation, which forces a re-login instead of
  /// silently keeping stale tokens alive.
  Future<LoginResult> refresh() {
    final inFlight = _refreshInFlight;
    if (inFlight != null) return inFlight;
    final future = _doRefresh().whenComplete(() => _refreshInFlight = null);
    _refreshInFlight = future;
    return future;
  }

  Future<LoginResult> _doRefresh() async {
    try {
      final data = await _request('POST', '/auth/refresh');
      final result = _parseLogin(data);
      _accessToken = result.accessToken;
      return result;
    } on ApiException catch (e) {
      if (e.isUnauthorized) {
        // Session definitively dead: let the app switch to signed-out.
        onSessionExpired?.call();
      }
      rethrow;
    }
  }

  LoginResult _parseLogin(Map<String, dynamic> data) => LoginResult(
        accessToken: data['accessToken'] as String,
        user: User.fromJson(data['user'] as Map<String, dynamic>),
      );

  /// Logout. 401 means the session is already dead server-side, so the local
  /// token is dropped and the call counts as success. Any other failure
  /// (403, 5xx, network) is surfaced to the caller and the local token is
  /// kept so the user can retry.
  Future<void> logout() async {
    try {
      await _request('POST', '/auth/logout', auth: _accessToken != null);
      _accessToken = null;
    } on ApiException catch (e) {
      if (e.isUnauthorized) {
        _accessToken = null;
        return;
      }
      rethrow;
    }
  }

  Future<User> me() async {
    final data = await _request('GET', '/auth/me', auth: true);
    return User.fromJson(data['user'] as Map<String, dynamic>);
  }

  // -- broadcasts ------------------------------------------------------------

  Future<BroadcastPage> listBroadcasts({String? cursor, int limit = 20}) async {
    final query = <String, String>{'limit': '$limit'};
    if (cursor != null && cursor.isNotEmpty) query['cursor'] = cursor;
    final data = await _request('GET', '/broadcasts?${Uri(queryParameters: query).query}');
    return BroadcastPage(
      items: (data['items'] as List<dynamic>)
          .map((e) => Broadcast.fromJson(e as Map<String, dynamic>))
          .toList(),
      nextCursor: (data['nextCursor'] as String?) ?? '',
    );
  }

  Future<Broadcast> getBroadcast(String id) async {
    final data = await _request('GET', '/broadcasts/$id');
    return Broadcast.fromJson(data['broadcast'] as Map<String, dynamic>);
  }

  Future<Broadcast> createBroadcast({required String title, String description = ''}) async {
    final data = await _request('POST', '/broadcasts',
        body: {'title': title, 'description': description}, auth: true);
    return Broadcast.fromJson(data['broadcast'] as Map<String, dynamic>);
  }

  Future<Broadcast> updateBroadcast(
    String id, {
    required int version,
    String? title,
    String? description,
  }) async {
    final data = await _request('PATCH', '/broadcasts/$id', body: {
      'version': version,
      'title': ?title,
      'description': ?description,
    }, auth: true);
    return Broadcast.fromJson(data['broadcast'] as Map<String, dynamic>);
  }

  Future<void> deleteBroadcast(String id, {required int version}) async {
    await _request('DELETE', '/broadcasts/$id?version=$version', auth: true);
  }

  /// Prepares only a session; no capture or LIVE state is implied.
  Future<LiveSession> prepareSession(String broadcastId, {required String source, required String idempotencyKey}) async {
    final data = await _request('POST', '/broadcasts/$broadcastId/sessions',
        body: {'source': source}, auth: true, headers: {'Idempotency-Key': idempotencyKey});
    return LiveSession.fromJson(data['session'] as Map<String, dynamic>);
  }

  Future<LiveSession?> currentSession(String broadcastId) async {
    final data = await _request('GET', '/broadcasts/$broadcastId/sessions/current');
    final value = data['session'];
    return value == null ? null : LiveSession.fromJson(value as Map<String, dynamic>);
  }

  Future<LiveSession> cancelSession(String broadcastId, String sessionId) async {
    final data = await _request('POST', '/broadcasts/$broadcastId/sessions/$sessionId/stop', auth: true);
    return LiveSession.fromJson(data['session'] as Map<String, dynamic>);
  }
}
