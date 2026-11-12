import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:provider/provider.dart';

import 'package:wbb_frontend/api/wbb_api.dart';
import 'package:wbb_frontend/main.dart';
import 'package:wbb_frontend/models/broadcast.dart';
import 'package:wbb_frontend/screens/broadcast_form_screen.dart';
import 'package:wbb_frontend/state/auth_controller.dart';

http.Response _json(Object body, [int status = 200]) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  status,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

http.Response _unauthorized() => _json({
  'error': {'code': 'unauthorized', 'message': 'Authentication required.'},
}, 401);

Map<String, dynamic> _loginBody(String token) => {
  'accessToken': token,
  'user': {'id': 'u1', 'email': 'a@b.c', 'displayName': 'Alice'},
};

Map<String, dynamic> _broadcastJson(int version) => {
  'id': 'b1',
  'ownerId': 'u1',
  'owner': {'id': 'u1', 'displayName': 'Alice'},
  'title': '서버의 최신 제목',
  'description': '',
  'version': version,
  'status': 'ready',
  'currentSessionId': null,
  'createdAt': '2026-11-06T10:00:00Z',
  'updatedAt': '2026-11-06T11:00:00Z',
};

Widget _app(http.Client client) => WbbApp(apiClient: ApiClient(client: client));

void main() {
  testWidgets('network failure shows an error with retry, and retry recovers', (
    tester,
  ) async {
    var failNext = true;
    final client = MockClient((request) async {
      if (request.url.path.endsWith('/auth/refresh')) return _unauthorized();
      if (request.method == 'GET' && request.url.path.endsWith('/broadcasts')) {
        if (failNext) {
          failNext = false;
          throw http.ClientException('connection reset');
        }
        return _json({
          'items': [
            {..._broadcastJson(1), 'title': '재시도 후 방송'},
          ],
          'nextCursor': '',
        });
      }
      fail('unexpected ${request.method} ${request.url.path}');
    });

    await tester.pumpWidget(_app(client));
    await tester.pumpAndSettle();

    // The spinner terminated and the normalized network error is visible.
    expect(find.byType(CircularProgressIndicator), findsNothing);
    expect(find.textContaining('불러오기 실패: 네트워크 오류'), findsOneWidget);
    expect(find.byKey(const Key('home-retry')), findsOneWidget);

    await tester.tap(find.byKey(const Key('home-retry')));
    await tester.pumpAndSettle();
    expect(find.text('재시도 후 방송'), findsOneWidget);
  });

  testWidgets('logout failure keeps the session and surfaces the error', (
    tester,
  ) async {
    final client = MockClient((request) async {
      if (request.url.path.endsWith('/auth/refresh'))
        return _json(_loginBody('at-1'));
      if (request.url.path.endsWith('/auth/logout')) {
        return _json({
          'error': {
            'code': 'session_store_unavailable',
            'message': 'The session store is temporarily unavailable.',
          },
        }, 503);
      }
      if (request.method == 'GET' && request.url.path.endsWith('/broadcasts')) {
        return _json({'items': [], 'nextCursor': ''});
      }
      fail('unexpected ${request.method} ${request.url.path}');
    });

    await tester.pumpWidget(_app(client));
    await tester.pumpAndSettle();
    expect(find.text('Alice'), findsOneWidget);

    await tester.tap(find.byTooltip('로그아웃'));
    await tester.pumpAndSettle();

    // Still signed in (retry possible) with the failure visible.
    expect(find.text('Alice'), findsOneWidget);
    expect(find.textContaining('로그아웃 실패'), findsOneWidget);
    expect(find.byTooltip('로그아웃'), findsOneWidget);
  });

  testWidgets('version conflict offers reload and the retry succeeds', (
    tester,
  ) async {
    final patchBodies = <String>[];
    final client = MockClient((request) async {
      if (request.url.path.endsWith('/auth/refresh'))
        return _json(_loginBody('at-1'));
      if (request.method == 'PATCH' &&
          request.url.path.endsWith('/broadcasts/b1')) {
        final body = jsonDecode(request.body) as Map<String, dynamic>;
        if ((body['version'] as num).toInt() == 1) {
          return _json({
            'error': {'code': 'version_conflict', 'message': 'stale version'},
          }, 409);
        }
        patchBodies.add(request.body);
        return _json({
          'broadcast': _broadcastJson((body['version'] as num).toInt() + 1),
        });
      }
      if (request.method == 'GET' &&
          request.url.path.endsWith('/broadcasts/b1')) {
        return _json({'broadcast': _broadcastJson(5)});
      }
      fail('unexpected ${request.method} ${request.url.path}');
    });

    final api = ApiClient(client: client);
    final auth = AuthController(api);
    await auth.restore();
    final existing = Broadcast.fromJson(_broadcastJson(1));

    await tester.pumpWidget(
      MultiProvider(
        providers: [
          Provider<ApiClient>.value(value: api),
          ChangeNotifierProvider<AuthController>.value(value: auth),
        ],
        child: const MaterialApp(home: BroadcastFormScreen(existing: null)),
      ),
    );
    await tester.pumpWidget(
      MultiProvider(
        providers: [
          Provider<ApiClient>.value(value: api),
          ChangeNotifierProvider<AuthController>.value(value: auth),
        ],
        child: MaterialApp(home: BroadcastFormScreen(existing: existing)),
      ),
    );
    await tester.pumpAndSettle();

    // Stale version 1 → 409 with the conflict notice and reload action.
    await tester.enterText(find.byKey(const Key('broadcast-title')), '내 수정');
    await tester.tap(find.byKey(const Key('broadcast-submit')));
    await tester.pumpAndSettle();
    expect(find.textContaining('다른 곳에서 수정되었습니다'), findsOneWidget);
    expect(find.byKey(const Key('broadcast-reload')), findsOneWidget);

    // Reload pulls version 5 + server text; the retry then succeeds.
    await tester.tap(find.byKey(const Key('broadcast-reload')));
    await tester.pumpAndSettle();
    expect(find.text('서버의 최신 제목'), findsOneWidget);

    await tester.enterText(find.byKey(const Key('broadcast-title')), '내 수정');
    await tester.tap(find.byKey(const Key('broadcast-submit')));
    await tester.pumpAndSettle();

    expect(patchBodies, hasLength(1));
    expect(
      (jsonDecode(patchBodies.single) as Map<String, dynamic>)['version'],
      5,
    );
  });

  test(
    'session expiry switches to signed-out with a re-login notice',
    () async {
      var refreshFails = false;
      final client = MockClient((request) async {
        if (request.url.path.endsWith('/auth/refresh')) {
          if (refreshFails) return _unauthorized();
          return _json(_loginBody('at-1'));
        }
        if (request.url.path.endsWith('/auth/login'))
          return _json(_loginBody('at-1'));
        if (request.url.path.endsWith('/auth/me')) {
          return _unauthorized(); // forces refresh → which now 401s
        }
        fail('unexpected ${request.method} ${request.url.path}');
      });

      final api = ApiClient(client: client);
      final auth = AuthController(api);
      await auth.restore();
      expect(auth.status, AuthStatus.authenticated);
      expect(auth.sessionExpired, isFalse);

      refreshFails = true;
      // Any authenticated call now hits 401 → refresh 401 → session expired.
      await expectLater(api.me(), throwsException);
      expect(auth.status, AuthStatus.unauthenticated);
      expect(auth.sessionExpired, isTrue);

      auth.clearSessionExpired();
      expect(auth.sessionExpired, isFalse);
    },
  );

  test(
    'startup restore failure ends signed-out without an expiry notice',
    () async {
      final client = MockClient((request) async => _unauthorized());
      final api = ApiClient(client: client);
      final auth = AuthController(api);
      await auth.restore();
      expect(auth.status, AuthStatus.unauthenticated);
      expect(auth.sessionExpired, isFalse); // visitors must not see the banner
    },
  );
}
