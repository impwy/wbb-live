import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:wbb_frontend/api/wbb_api.dart';
import 'package:wbb_frontend/state/auth_controller.dart';

http.Response jsonReply(Object value, [int status = 200]) => http.Response(
  jsonEncode(value),
  status,
  headers: {'content-type': 'application/json'},
);

void main() {
  test(
    'prepare retains the private grant and WHIP uses only that grant',
    () async {
      final requests = <http.Request>[];
      final api = ApiClient(
        baseUrl: 'https://live.test/api/v1',
        client: MockClient((request) async {
          requests.add(request);
          if (request.url.path.endsWith('/auth/login')) {
            return jsonReply({
              'accessToken': 'access',
              'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'A'},
            });
          }
          if (request.url.path.endsWith('/sessions')) {
            return jsonReply({
              'session': {
                'id': 's1',
                'broadcastId': 'b1',
                'state': 'starting',
                'version': 1,
                'startedAt': '2026-11-09T01:00:00Z',
              },
              'publishGrant': {
                'token': 'private-grant',
                'whipUrl': '/api/v1/live-sessions/s1/whip',
                'expiresAt': '2026-11-09T01:00:30Z',
              },
            }, 202);
          }
          if (request.url.path.endsWith('/whip')) {
            return http.Response(
              'v=0\r\nanswer',
              201,
              headers: {
                'content-type': 'application/sdp',
                'location': '/api/v1/live-sessions/s1/whip/r1',
                'etag': '"tag"',
              },
            );
          }
          if (request.method == 'DELETE') return http.Response('', 202);
          fail('unexpected ${request.method} ${request.url}');
        }),
      );
      await api.login('a@b.c', 'pw');
      final prepared = await api.prepareSession(
        'b1',
        source: 'screen',
        idempotencyKey: 'stable-key-0001',
      );
      expect(prepared.session.id, 's1');
      expect(prepared.publishGrant?.token, 'private-grant');
      final resource = await api.postWhip(prepared, 'v=0\r\noffer');
      expect(resource.answerSdp, 'v=0\r\nanswer');
      expect(requests[2].headers['Authorization'], 'Bearer private-grant');
      expect(requests[2].headers['Content-Type'], 'application/sdp');
      await api.deleteWhip(resource);
      expect(requests[3].headers['Authorization'], 'Bearer access');
      expect(requests[3].headers['If-Match'], '"tag"');
    },
  );

  test('late refresh cannot restore access after logout begins', () async {
    final refresh = Completer<http.Response>();
    final api = ApiClient(
      baseUrl: 'https://live.test/api/v1',
      client: MockClient((request) async {
        if (request.url.path.endsWith('/auth/login')) {
          return jsonReply({
            'accessToken': 'old',
            'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'A'},
          });
        }
        if (request.url.path.endsWith('/auth/refresh')) return refresh.future;
        if (request.url.path.endsWith('/auth/logout')) {
          return http.Response('', 204);
        }
        if (request.url.path.endsWith('/auth/me')) {
          return jsonReply({
            'error': {'code': 'unauthorized', 'message': 'expired'},
          }, 401);
        }
        fail('unexpected ${request.method} ${request.url}');
      }),
    );
    await api.login('a@b.c', 'pw');
    final pending = api.me();
    await Future<void>.delayed(Duration.zero);
    await api.logout();
    refresh.complete(
      jsonReply({
        'accessToken': 'late',
        'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'A'},
      }),
    );
    await expectLater(pending, throwsA(isA<ApiException>()));
    expect(api.hasAccessToken, isFalse);
  });

  test('old 401 during a new login cannot refresh the old cookie or replace the new user', () async {
    final oldMe = Completer<http.Response>();
    final entered = Completer<void>();
    final newLogin = Completer<http.Response>();
    var loginCalls = 0;
    var refreshCalls = 0;
    final api = ApiClient(
      baseUrl: 'https://live.test/api/v1',
      client: MockClient((request) async {
        if (request.url.path.endsWith('/auth/login')) {
          loginCalls++;
          if (loginCalls == 1) {
            return jsonReply({
              'accessToken': 'A',
              'user': {'id': 'A', 'email': 'a@b.c', 'displayName': 'A'},
            });
          }
          return newLogin.future;
        }
        if (request.url.path.endsWith('/auth/me')) {
          entered.complete();
          return oldMe.future;
        }
        if (request.url.path.endsWith('/auth/refresh')) {
          refreshCalls++;
          return jsonReply({
            'accessToken': 'A-refreshed',
            'user': {'id': 'A', 'email': 'a@b.c', 'displayName': 'A'},
          });
        }
        fail('unexpected ${request.method} ${request.url}');
      }),
    );
    final auth = AuthController(api);
    await auth.login('a@b.c', 'pw');
    final pendingMe = api.me();
    await entered.future;
    final pendingLogin = auth.login('b@b.c', 'pw');
    oldMe.complete(
      jsonReply({
        'error': {'code': 'unauthorized', 'message': 'expired'},
      }, 401),
    );
    await expectLater(pendingMe, throwsA(isA<ApiException>()));
    expect(refreshCalls, 0);
    newLogin.complete(
      jsonReply({
        'accessToken': 'B',
        'user': {'id': 'B', 'email': 'b@b.c', 'displayName': 'B'},
      }),
    );
    await pendingLogin;
    expect(auth.user?.id, 'B');
    expect(refreshCalls, 0);
  });

  test(
    'logout and new login serialize so a late logout cannot erase the new user',
    () async {
      final logoutReply = Completer<http.Response>();
      final logoutEntered = Completer<void>();
      var loginCalls = 0;
      final api = ApiClient(
        baseUrl: 'https://live.test/api/v1',
        client: MockClient((request) async {
          if (request.url.path.endsWith('/auth/login')) {
            loginCalls++;
            final name = loginCalls == 1 ? 'A' : 'B';
            return jsonReply({
              'accessToken': name,
              'user': {'id': name, 'email': '$name@b.c', 'displayName': name},
            });
          }
          if (request.url.path.endsWith('/auth/logout')) {
            logoutEntered.complete();
            return logoutReply.future;
          }
          fail('unexpected ${request.method} ${request.url}');
        }),
      );
      final auth = AuthController(api);
      await auth.login('a@b.c', 'pw');
      final leaving = auth.logout();
      await logoutEntered.future;
      final arriving = auth.login('b@b.c', 'pw');
      await Future<void>.delayed(Duration.zero);
      expect(
        loginCalls,
        1,
        reason: 'the new cookie must be issued after old logout responds',
      );
      logoutReply.complete(http.Response('', 204));
      await leaving;
      await arriving;
      expect(auth.user?.id, 'B');
      expect(api.hasAccessToken, isTrue);
    },
  );

  test('stale restore failure cannot clear a later login user', () async {
    final restoreReply = Completer<http.Response>();
    final restoreEntered = Completer<void>();
    final api = ApiClient(
      baseUrl: 'https://live.test/api/v1',
      client: MockClient((request) async {
        if (request.url.path.endsWith('/auth/refresh')) {
          restoreEntered.complete();
          return restoreReply.future;
        }
        if (request.url.path.endsWith('/auth/login')) {
          return jsonReply({
            'accessToken': 'B',
            'user': {'id': 'B', 'email': 'b@b.c', 'displayName': 'B'},
          });
        }
        fail('unexpected ${request.method} ${request.url}');
      }),
    );
    final auth = AuthController(api);
    final restoring = auth.restore();
    await restoreEntered.future;
    await auth.login('b@b.c', 'pw');
    restoreReply.complete(
      jsonReply({
        'error': {'code': 'failure', 'message': 'expired'},
      }, 500),
    );
    await restoring;
    expect(auth.user?.id, 'B');
    expect(auth.sessionExpired, isFalse);
    expect(api.hasAccessToken, isTrue);
  });

  test(
    'logout proceeds after bounded wait when capture stop HTTP never answers',
    () async {
      final neverStopped = Completer<void>();
      var localStops = 0;
      var logoutCalls = 0;
      final api = ApiClient(
        baseUrl: 'https://live.test/api/v1',
        client: MockClient((request) async {
          if (request.url.path.endsWith('/auth/login')) {
            return jsonReply({
              'accessToken': 'A',
              'user': {'id': 'A', 'email': 'a@b.c', 'displayName': 'A'},
            });
          }
          if (request.url.path.endsWith('/auth/logout')) {
            logoutCalls++;
            return http.Response('', 204);
          }
          fail('unexpected ${request.method} ${request.url}');
        }),
      );
      final auth = AuthController(
        api,
        beforeLogout: () {
          localStops++;
          return neverStopped.future;
        },
      );
      await auth.login('a@b.c', 'pw');
      await auth.logout().timeout(const Duration(seconds: 5));
      expect(localStops, 1);
      expect(logoutCalls, 1);
      expect(auth.user, isNull);
    },
  );
}
