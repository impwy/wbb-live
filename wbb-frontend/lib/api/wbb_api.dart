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

class AuthProviders {
  const AuthProviders({
    required this.googleEnabled,
    required this.evaluationEnabled,
  });
  final bool googleEnabled;
  final bool evaluationEnabled;
}

/// A start response for the publisher only. Never place this in public
/// [LiveSession] state or render the grant in a widget.
class PreparedSession {
  PreparedSession(this.session, this.publishGrant);
  final LiveSession session;
  final PublishGrant? publishGrant;
}

class PublishGrant {
  PublishGrant(this.token, this.whipUrl, this.expiresAt);
  final String token;
  final String whipUrl;
  final DateTime expiresAt;
}

class WhipResource {
  WhipResource(this.sessionId, this.location, this.etag, this.answerSdp);
  final String sessionId;
  final String location;
  final String? etag;
  final String answerSdp;
}

class PlaybackAccess {
  PlaybackAccess(this.sessionId, this.masterUrl);
  final String sessionId;
  final String masterUrl;
}

/// Thin HTTP client for the WBB public API.
///
/// - The access JWT lives in memory only (never localStorage).
/// - The refresh cookie is HttpOnly and sent by the browser (withCredentials).
/// - A 401 on an authenticated call triggers one single-flight refresh and a
///   single retry, serializing concurrent calls within this ApiClient instance.
class ApiClient {
  ApiClient({
    http.Client? client,
    String baseUrl = kApiBaseUrl,
    this.onSessionExpired,
  }) : _http = client ?? createPlatformClient(),
       _baseUrl = baseUrl.endsWith('/')
           ? baseUrl.substring(0, baseUrl.length - 1)
           : baseUrl;

  final http.Client _http;
  final String _baseUrl;

  Uri get googleStartUri => Uri.base.resolve('$_baseUrl/auth/google/start');

  /// Fired when a refresh definitively fails with 401 (session dead) so the
  /// app can switch to signed-out and prompt a re-login. Set by the
  /// AuthController.
  void Function()? onSessionExpired;

  String? _accessToken;
  Future<LoginResult>? _refreshInFlight;

  int _authEpoch = 0;
  bool _signingOut = false;
  bool _loginInFlight = false;
  Future<void>? _logoutInFlight;

  bool get hasAccessToken => _accessToken != null;

  set accessToken(String? token) {
    _authEpoch++;
    _accessToken = token;
  }

  Future<Map<String, dynamic>> _request(
    String method,
    String path, {
    Object? body,
    bool auth = false,
    bool allowRefreshRetry = true,
    bool guardAuthEpoch = true,
    Map<String, String>? headers,
  }) async {
    final requestEpoch = _authEpoch;
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
      streamed = await _http.send(request).timeout(const Duration(seconds: 12));
    } catch (_) {
      throw ApiException(
        0,
        'network_error',
        '네트워크 오류가 발생했습니다. 잠시 후 다시 시도해 주세요.',
      );
    }
    http.Response response;
    try {
      response = await http.Response.fromStream(streamed)
          .timeout(const Duration(seconds: 12));
    } catch (_) {
      throw ApiException(
        0,
        'network_error',
        '네트워크 오류가 발생했습니다. 잠시 후 다시 시도해 주세요.',
      );
    }

    if (auth &&
        guardAuthEpoch &&
        (requestEpoch != _authEpoch || _loginInFlight)) {
      throw ApiException(401, 'session_changed', '로그인 상태가 변경되었습니다.');
    }
    if (response.statusCode == 401 &&
        auth &&
        allowRefreshRetry &&
        _accessToken != null &&
        !_signingOut) {
      await refresh();
      if (requestEpoch != _authEpoch) {
        throw ApiException(401, 'session_changed', '로그인 상태가 변경되었습니다.');
      }
      return _request(
        method,
        path,
        body: body,
        auth: auth,
        allowRefreshRetry: false,
        headers: headers,
      );
    }

    if (response.statusCode == 204 || response.body.isEmpty) {
      if (response.statusCode >= 400) {
        throw ApiException(response.statusCode, 'error', '요청이 실패했습니다.');
      }
      return const {};
    }
    final Map<String, dynamic> decoded;
    try {
      decoded =
          jsonDecode(utf8.decode(response.bodyBytes)) as Map<String, dynamic>;
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

  Future<AuthProviders> authProviders() async {
    final data = await _request('GET', '/auth/providers');
    return AuthProviders(
      googleEnabled: data['googleEnabled'] == true,
      evaluationEnabled: data['evaluationEnabled'] == true,
    );
  }

  Future<bool> googleEnabled() async => (await authProviders()).googleEnabled;

  Future<User> register({
    required String email,
    required String password,
    required String displayName,
  }) async {
    final data = await _request(
      'POST',
      '/auth/register',
      body: {'email': email, 'password': password, 'displayName': displayName},
    );
    return User.fromJson(data['user'] as Map<String, dynamic>);
  }

  Future<LoginResult> login(String email, String password) =>
      _beginLogin('/auth/login', body: {'email': email, 'password': password});

  Future<LoginResult> evaluationLogin() =>
      _beginLogin('/auth/evaluation-login');

  Future<LoginResult> _beginLogin(String path, {Object? body}) async {
    final epoch = ++_authEpoch;
    _loginInFlight = true;
    try {
      final leaving = _logoutInFlight;
      if (leaving != null) {
        try {
          await leaving;
        } catch (_) {
          /* login may proceed after failed logout */
        }
      }
      if (epoch != _authEpoch) {
        throw ApiException(401, 'session_changed', '로그인 상태가 변경되었습니다.');
      }
      final data = await _request('POST', path, body: body);
      final result = _parseLogin(data);
      if (epoch != _authEpoch) {
        throw ApiException(401, 'session_changed', '로그인 상태가 변경되었습니다.');
      }
      _accessToken = result.accessToken;
      return result;
    } finally {
      if (epoch == _authEpoch) _loginInFlight = false;
    }
  }

  /// Refresh via the HttpOnly cookie. Single-flight within this client
  /// instance only (one browser tab); cross-tab races are made safe by the
  /// server's family revocation, which forces a re-login instead of
  /// silently keeping stale tokens alive.
  Future<LoginResult> refresh() {
    if (_signingOut || _loginInFlight) {
      return Future.error(
        ApiException(401, 'session_changed', '로그인 상태가 변경되었습니다.'),
      );
    }
    final inFlight = _refreshInFlight;
    if (inFlight != null) return inFlight;
    final epoch = _authEpoch;
    final future = _doRefresh(epoch)
        .whenComplete(() => _refreshInFlight = null);
    _refreshInFlight = future;
    return future;
  }

  Future<LoginResult> _doRefresh(int epoch) async {
    try {
      final data = await _request('POST', '/auth/refresh');
      final result = _parseLogin(data);
      if (epoch != _authEpoch || _signingOut || _loginInFlight) {
        throw ApiException(401, 'session_changed', '로그인 상태가 변경되었습니다.');
      }
      _accessToken = result.accessToken;
      return result;
    } on ApiException catch (e) {
      if (e.isUnauthorized && epoch == _authEpoch && !_signingOut) {
        // Session definitively dead: let the app switch to signed-out.
        _authEpoch++;
        _accessToken = null;
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
  Future<void> logout() {
    final existing = _logoutInFlight;
    if (existing != null) return existing;
    final work = _doLogout();
    _logoutInFlight = work;
    return work;
  }

  Future<void> _doLogout() async {
    final epoch = ++_authEpoch;
    _signingOut = true;
    _loginInFlight = false;
    try {
      await _request(
        'POST',
        '/auth/logout',
        auth: _accessToken != null,
        allowRefreshRetry: false,
        guardAuthEpoch: false,
      );
      if (epoch == _authEpoch) _accessToken = null;
    } on ApiException catch (e) {
      if (e.isUnauthorized) {
        if (epoch == _authEpoch) _accessToken = null;
        return;
      }
      rethrow;
    } finally {
      _signingOut = false;
      _logoutInFlight = null;
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
    final data = await _request(
      'GET',
      '/broadcasts?${Uri(queryParameters: query).query}',
    );
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

  Future<Broadcast> createBroadcast({
    required String title,
    String description = '',
  }) async {
    final data = await _request(
      'POST',
      '/broadcasts',
      body: {'title': title, 'description': description},
      auth: true,
    );
    return Broadcast.fromJson(data['broadcast'] as Map<String, dynamic>);
  }

  Future<Broadcast> updateBroadcast(
    String id, {
    required int version,
    String? title,
    String? description,
  }) async {
    final data = await _request(
      'PATCH',
      '/broadcasts/$id',
      body: {'version': version, 'title': ?title, 'description': ?description},
      auth: true,
    );
    return Broadcast.fromJson(data['broadcast'] as Map<String, dynamic>);
  }

  Future<void> deleteBroadcast(String id, {required int version}) async {
    await _request('DELETE', '/broadcasts/$id?version=$version', auth: true);
  }

  /// Prepares only a session; no capture or LIVE state is implied.
  Future<PreparedSession> prepareSession(
    String broadcastId, {
    required String source,
    required String idempotencyKey,
  }) async {
    final data = await _request(
      'POST',
      '/broadcasts/$broadcastId/sessions',
      body: {'source': source},
      auth: true,
      headers: {'Idempotency-Key': idempotencyKey},
    );
    final rawGrant = data['publishGrant'];
    PublishGrant? grant;
    if (rawGrant is Map<String, dynamic>) {
      grant = PublishGrant(
        rawGrant['token'] as String,
        rawGrant['whipUrl'] as String,
        DateTime.parse(rawGrant['expiresAt'] as String),
      );
    }
    return PreparedSession(
      LiveSession.fromJson(data['session'] as Map<String, dynamic>),
      grant,
    );
  }

  Uri _mediaUri(String raw, String expectedPath) {
    final base = Uri.parse(_baseUrl);
    final origin = base.hasScheme ? base.origin : Uri.base.origin;
    final uri = Uri.parse(raw);
    final resolved = uri.hasScheme ? uri : Uri.parse(origin).resolveUri(uri);
    if (resolved.origin != origin ||
        resolved.path != expectedPath ||
        resolved.hasQuery ||
        resolved.hasFragment ||
        resolved.userInfo.isNotEmpty) {
      throw ApiException(0, 'unsafe_media_url', '잘못된 미디어 주소입니다.');
    }
    return resolved;
  }

  Future<http.Response> _mediaRequest(
    String method,
    Uri uri, {
    String? bearer,
    String? body,
    String? contentType,
    String? etag,
    bool auth = false,
    bool retry = true,
  }) async {
    final epoch = _authEpoch;
    final request = http.Request(method, uri);
    final credential = auth ? _accessToken : bearer;
    if (credential != null) {
      request.headers['Authorization'] = 'Bearer $credential';
    }
    if (contentType != null) request.headers['Content-Type'] = contentType;
    if (etag != null) request.headers['If-Match'] = etag;
    if (body != null) request.body = body;
    try {
      final streamed = await _http
          .send(request)
          .timeout(const Duration(seconds: 14));
      final response = await http.Response.fromStream(streamed)
          .timeout(const Duration(seconds: 14));
      if (auth && (epoch != _authEpoch || _loginInFlight || _signingOut)) {
        throw ApiException(401, 'session_changed', '로그인 상태가 변경되었습니다.');
      }
      if (response.statusCode == 401 && auth && retry && !_signingOut) {
        await refresh();
        if (epoch != _authEpoch) {
          throw ApiException(401, 'session_changed', '로그인 상태가 변경되었습니다.');
        }
        return await _mediaRequest(
          method,
          uri,
          bearer: bearer,
          body: body,
          contentType: contentType,
          etag: etag,
          auth: true,
          retry: false,
        );
      }
      return response;
    } on ApiException {
      rethrow;
    } catch (_) {
      throw ApiException(0, 'media_network_error', '미디어 연결을 확인할 수 없습니다.');
    }
  }

  /// A POST with an unknown outcome is never retried automatically.
  Future<WhipResource> postWhip(
    PreparedSession prepared,
    String offerSdp,
  ) async {
    final grant = prepared.publishGrant;
    if (grant == null || grant.token.isEmpty) {
      throw ApiException(409, 'grant_unavailable', '송출 자격이 만료되었습니다.');
    }
    final sid = prepared.session.id;
    final uri = _mediaUri(grant.whipUrl, '/api/v1/live-sessions/$sid/whip');
    final response = await _mediaRequest(
      'POST',
      uri,
      bearer: grant.token,
      body: offerSdp,
      contentType: 'application/sdp',
    );
    if (response.statusCode != 201) {
      throw ApiException(response.statusCode, 'whip_failed', '송출 연결에 실패했습니다.');
    }
    final location = response.headers['location'] ?? '';
    final prefix = '/api/v1/live-sessions/$sid/whip/';
    final parsed = Uri.tryParse(location);
    if (parsed == null ||
        !parsed.path.startsWith(prefix) ||
        parsed.path.substring(prefix.length).contains('/') ||
        parsed.path.length == prefix.length ||
        response.headers['content-type']?.split(';').first.trim() !=
            'application/sdp' ||
        response.body.isEmpty) {
      throw ApiException(0, 'invalid_whip_response', '송출 응답이 올바르지 않습니다.');
    }
    _mediaUri(location, parsed.path);
    return WhipResource(sid, location, response.headers['etag'], response.body);
  }

  Future<void> deleteWhip(WhipResource resource) async {
    final path = Uri.parse(resource.location).path;
    final prefix = '/api/v1/live-sessions/${resource.sessionId}/whip/';
    if (!path.startsWith(prefix) ||
        path.length == prefix.length ||
        path.substring(prefix.length).contains('/')) {
      throw ApiException(0, 'unsafe_media_url', '잘못된 미디어 주소입니다.');
    }
    final response = await _mediaRequest(
      'DELETE',
      _mediaUri(resource.location, path),
      etag: resource.etag,
      auth: true,
    );
    // 202 accepts the stop request; only the session state can confirm ENDED.
    if (response.statusCode != 202 &&
        response.statusCode != 204 &&
        response.statusCode != 404) {
      throw ApiException(
        response.statusCode,
        'whip_delete_failed',
        '송출 연결 종료에 실패했습니다.',
      );
    }
  }

  Future<PlaybackAccess> playback(String broadcastId) async {
    final data = await _request('GET', '/broadcasts/$broadcastId/playback');
    return PlaybackAccess(
      data['sessionId'] as String,
      data['masterUrl'] as String,
    );
  }

  Future<LiveSession?> currentSession(String broadcastId) async {
    final data = await _request(
      'GET',
      '/broadcasts/$broadcastId/sessions/current',
    );
    final value = data['session'];
    return value == null
        ? null
        : LiveSession.fromJson(value as Map<String, dynamic>);
  }

  Future<LiveSession> cancelSession(
    String broadcastId,
    String sessionId,
  ) async {
    final data = await _request(
      'POST',
      '/broadcasts/$broadcastId/sessions/$sessionId/stop',
      auth: true,
    );
    return LiveSession.fromJson(data['session'] as Map<String, dynamic>);
  }
}
